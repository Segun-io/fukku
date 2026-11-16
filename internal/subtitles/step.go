package subtitles

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/segun-io/fukku/internal/arr"
	"github.com/segun-io/fukku/internal/config"
	"github.com/segun-io/fukku/internal/flows"
	"github.com/segun-io/fukku/internal/jobdir"
	"github.com/segun-io/fukku/internal/store"
)

// StepName is the name the step is recorded under (store.PostStep.Name).
const StepName = "subtitles"

// Files is the part of the store the step reads.
type Files interface {
	Files(ctx context.Context, jobID string) ([]store.File, error)
}

var _ flows.PostStep = (*Step)(nil)

// Step is the post-processing step "subtitles" (the engine's PostStep contract, #40):
// after the import it gives the job's accepted subtitle files to the subtitle manager.
//
// For each file it finds the video with the same base name, takes the language and the
// forced and hearing-impaired flags from the file name (".es.srt", ".es.forced.srt",
// ".en.hi.srt") unless the user chose them in the UI, uploads it, waits for the manager's
// history to show it written, and uploads again with a growing wait if it does not (decision
// #23). A file that was not handed over fails the step with its name; the import stays.
type Step struct {
	Manager    SubtitleManager // nil with provider none
	Store      Files
	IntakeRoot string
	Config     config.Subtitles

	// Sleep waits d, or returns the context's error. Nil: a timer. Tests replace it.
	Sleep func(ctx context.Context, d time.Duration) error

	mu      sync.Mutex
	handled map[string]bool // job + file path already confirmed written by this process
}

func (s *Step) Name() string { return StepName }

// Run hands the files over. It is safe to repeat: files confirmed written are not sent
// again by the same process, and a repeat after a restart only rewrites the same file (probe
// C5e). With no subtitle manager, the hand-over switched off or no subtitle file, it is
// skipped (flows.Skip). A file that was not written fails it with flows.GiveUp, naming the
// file: the step has retried each upload itself (subtitles.attempts), so the engine does
// not run it again.
func (s *Step) Run(ctx context.Context, job store.Job, target store.Target, rows []store.Row) (bool, string, error) {
	switch {
	case s.Config.Provider != "bazarr" || s.Manager == nil:
		return true, "", flows.Skip("no subtitle manager is configured")
	case !s.Config.HandOverUploads:
		return true, "", flows.Skip("handing uploads over is switched off")
	}
	files, err := s.Store.Files(ctx, job.ID)
	if err != nil {
		return false, "", err
	}
	items := plan(files, rows, target)
	if len(items) == 0 {
		return true, "", flows.Skip("no subtitle files")
	}
	var failed []string
	sent := 0
	for _, it := range items {
		if s.isHandled(job.ID, it.file.Path) {
			sent++
			continue
		}
		err := it.problem
		if err == nil {
			err = s.handOver(ctx, job, it)
		}
		if ctx.Err() != nil {
			return false, "", ctx.Err()
		}
		if err != nil {
			failed = append(failed, fmt.Sprintf("%s (%v)", path.Base(it.file.Path), err))
			continue
		}
		s.markHandled(job.ID, it.file.Path)
		sent++
	}
	if len(failed) > 0 {
		return false, "", flows.GiveUp(fmt.Errorf("not written by the subtitle manager: %s", strings.Join(failed, "; ")))
	}
	return true, fmt.Sprintf("%d subtitle file(s) handed over", sent), nil
}

func (s *Step) isHandled(jobID, p string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.handled[jobID+"/"+p]
}

func (s *Step) markHandled(jobID, p string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.handled == nil {
		s.handled = map[string]bool{}
	}
	s.handled[jobID+"/"+p] = true
}

// handOver uploads one file and confirms it.
func (s *Step) handOver(ctx context.Context, job store.Job, it item) error {
	data, err := s.read(job.ID, it.file)
	if err != nil {
		return err
	}
	// A file for several episodes is handed over for each of them.
	for _, u := range it.uploads {
		if err := s.confirmed(ctx, u, data); err != nil {
			return err
		}
	}
	return nil
}

// confirmed uploads one file for one title and waits for the history, up to Config.Attempts times.
func (s *Step) confirmed(ctx context.Context, u Upload, data []byte) error {
	polls := int(s.Config.ConfirmTimeout / max(s.Config.PollInterval, 1))
	polls = max(polls, 1)
	backoff := time.Duration(s.Config.RetryBackoff)
	var last error
	for attempt := 1; attempt <= max(s.Config.Attempts, 1); attempt++ {
		if attempt > 1 {
			if err := s.sleep(ctx, backoff); err != nil {
				return err
			}
			backoff = min(backoff*2, 5*time.Minute)
		}
		u.Content = strings.NewReader(string(data))
		if err := s.Manager.Upload(ctx, u); err != nil {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			last = fmt.Errorf("upload refused: %w", err)
			continue
		}
		last = errors.New("the history did not show it written")
		for range polls {
			if err := s.sleep(ctx, time.Duration(s.Config.PollInterval)); err != nil {
				return err
			}
			ok, err := s.Manager.Written(ctx, u)
			if err != nil {
				last = fmt.Errorf("history unreadable: %w", err)
				continue
			}
			if ok {
				return nil
			}
		}
	}
	return last
}

func (s *Step) sleep(ctx context.Context, d time.Duration) error {
	if s.Sleep != nil {
		return s.Sleep(ctx, d)
	}
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}

// read returns a subtitle file's bytes. This is the one place the web process reads an
// uploaded file: only a file the worker's text check accepted (status ok, kind subtitle)
// is read, inside the job's own files directory, as a regular file no larger than
// subtitles.maxFileSize. The bytes go to the subtitle manager as they are; Fukku never
// parses them.
func (s *Step) read(jobID string, f store.File) (data []byte, err error) {
	if f.Kind != store.KindSubtitle || f.Status != store.FileOK {
		return nil, errors.New("not an accepted subtitle file")
	}
	rel := path.Clean(f.Path)
	if !strings.HasPrefix(rel, jobdir.Files+"/") || strings.Contains(rel, "..") {
		return nil, errors.New("not in the job's files")
	}
	full := filepath.Join(jobdir.Dir(s.IntakeRoot, jobID), filepath.FromSlash(rel))
	info, err := os.Lstat(full)
	if err != nil {
		return nil, errors.New("the file is gone")
	}
	limit := int64(s.Config.MaxFileSize)
	if !info.Mode().IsRegular() || info.Size() > limit {
		return nil, errors.New("not a regular file within the size limit")
	}
	fh, err := os.Open(full) //nolint:gosec // inside the job directory, checked above
	if err != nil {
		return nil, errors.New("the file cannot be read")
	}
	defer func() { _ = fh.Close() }()
	data, err = io.ReadAll(io.LimitReader(fh, limit+1))
	if err != nil {
		return nil, errors.New("the file cannot be read")
	}
	if int64(len(data)) > limit {
		return nil, errors.New("larger than the size limit")
	}
	return data, nil
}

// item is one subtitle file and what to do with it: an upload ready to send, or the reason
// it cannot be sent.
type item struct {
	file    store.File
	uploads []Upload // one, or one per episode of a file that covers several
	problem error
}

var langRE = regexp.MustCompile(`^[a-z]{2,3}(-[A-Za-z]{2,4})?$`)

// plan pairs every accepted subtitle file with its video and works out its upload.
func plan(files []store.File, rows []store.Row, target store.Target) []item {
	// What the user picked at the confirmation, by file id, overrides the file name.
	choices := flows.SubtitleChoices(target)
	rowOf := map[int64]store.Row{}
	for _, r := range rows {
		rowOf[r.FileID] = r
	}
	videos := map[string]store.File{} // dir + "/" + stem
	for _, f := range files {
		if f.Kind == store.KindVideo && f.Status == store.FileOK {
			videos[stemKey(f.Path)] = f
		}
	}
	var out []item
	for _, f := range files {
		if f.Kind != store.KindSubtitle || f.Status != store.FileOK {
			continue
		}
		it := item{file: f}
		base := Upload{App: target.App, TitleID: target.TitleID, Name: path.Base(f.Path)}
		video, lang, forced, hi, ok := match(f.Path, videos)
		if ch, ok := choices[f.ID]; ok {
			if ch.Language != "" {
				lang = ch.Language
			}
			forced, hi = ch.Forced, ch.HearingImpaired
		}
		base.Language, base.Forced, base.HI = lang, forced, hi
		switch {
		case !ok:
			it.problem = errors.New("no video with the same name")
		case lang == "" || !langRE.MatchString(lang):
			it.problem = errors.New("language unknown: name it like Film.es.srt or choose it")
		default:
			it.uploads, it.problem = uploadsFor(base, rowOf[video.ID], target)
		}
		out = append(out, it)
	}
	return out
}

// uploadsFor checks that the video was imported and returns the uploads: one for a film,
// one per episode the video covers for a show.
func uploadsFor(base Upload, row store.Row, target store.Target) ([]Upload, error) {
	// With the season-dub policy subtitlesOnly (#57) nothing is imported: the subtitles go
	// to the episodes that already have a file.
	if only, ok := flows.SubtitlesOnlyRows(target); ok {
		if row.JobID == "" || !slices.Contains(only, row.ID) {
			return nil, errors.New("its episode has no file in the library")
		}
	} else if row.JobID == "" || row.Skip || row.Result != store.ResultImported {
		return nil, errors.New("its video was not imported")
	}
	if target.App != "sonarr" {
		return []Upload{base}, nil
	}
	var p arr.Row
	if err := json.Unmarshal(row.Proposal, &p); err != nil || len(p.EpisodeIDs) == 0 {
		return nil, errors.New("its episode is unknown")
	}
	var out []Upload
	for _, id := range p.EpisodeIDs {
		u := base
		u.EpisodeID = id
		out = append(out, u)
	}
	return out, nil
}

func stemKey(p string) string {
	return strings.TrimSuffix(p, path.Ext(p))
}

var languages3 = map[string]string{
	"eng": "en", "spa": "es", "fra": "fr", "fre": "fr", "deu": "de", "ger": "de", "ita": "it",
	"por": "pt", "rus": "ru", "jpn": "ja", "kor": "ko", "zho": "zh", "chi": "zh", "nld": "nl",
	"dut": "nl", "pol": "pl", "tur": "tr", "swe": "sv", "nor": "no", "dan": "da", "fin": "fi",
	"ces": "cs", "cze": "cs", "ell": "el", "gre": "el", "heb": "he", "ara": "ar", "hin": "hi",
	"hun": "hu", "ron": "ro", "rum": "ro", "ukr": "uk", "vie": "vi", "tha": "th", "cat": "ca",
}

// match finds the video of the subtitle at p (same directory, same name apart from the
// trailing language and flag tokens) and reads those tokens. The fewest tokens that lead to
// a video win, so a film named "Movie.it" still matches "Movie.it.srt".
func match(p string, videos map[string]store.File) (video store.File, lang string, forced, hi, ok bool) {
	tokens := strings.Split(stemKey(p), ".")
	for k := 0; k <= 3 && k < len(tokens); k++ {
		v, found := videos[strings.Join(tokens[:len(tokens)-k], ".")]
		if !found {
			continue
		}
		l, f, h, valid := readTokens(tokens[len(tokens)-k:])
		if valid {
			return v, l, f, h, true
		}
	}
	return store.File{}, "", false, false, false
}

// readTokens reads ["es", "forced"], ["hi", "en"] and so on: at most one language and the
// flags in any order. A token that is neither makes the whole run invalid.
func readTokens(tokens []string) (lang string, forced, hi, valid bool) {
	for _, t := range tokens {
		switch t = strings.ToLower(t); {
		case t == "forced":
			forced = true
		case t == "hi" || t == "sdh" || t == "cc":
			hi = true
		case lang == "" && len(t) == 2:
			lang = t
		case lang == "" && languages3[t] != "":
			lang = languages3[t]
		default:
			return "", false, false, false
		}
	}
	return lang, forced, hi, true
}
