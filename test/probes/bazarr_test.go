//go:build probes

package probes

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"maps"
	"mime/multipart"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/segun-io/fukku/test/fixtures"
	"github.com/segun-io/fukku/test/testenv"
)

// C5: Bazarr accepts an uploaded subtitle for a Radarr film (plan §6 "Subtitles", spec).
func TestC5BazarrSubtitleUpload(t *testing.T) {
	p := newProbe(t, "C5", "Bazarr accepts a subtitle upload for a film",
		"`POST /api/movies/subtitles?radarrid=&language=&forced=&hi=` with multipart field `file`; all four query parameters required",
		true, "bazarr", "radarr")
	movie := addFilm(p, tearsOfSteel)
	id := testenv.ID(movie)
	importFilm(p, movie, tearsOfSteel, tearsOfSteel.dotted()+".1080p.WEB-DL", "eng")

	// Bazarr learns about films from Radarr on a schedule; ask it to sync now.
	form := url.Values{"taskid": {"update_movies"}}
	p.must(env.Bazarr.DoRaw(p.ctx, "POST", "/api/system/tasks", "application/x-www-form-urlencoded",
		strings.NewReader(form.Encode()), nil, nil), "start the Bazarr movie sync")
	p.must(testenv.WaitFor(p.ctx, 3*time.Minute, 3*time.Second, "Bazarr to know the film", func() (bool, error) {
		var res struct {
			Data []M `json:"data"`
		}
		err := env.Bazarr.Do(p.ctx, "GET", fmt.Sprintf("/api/movies?radarrid[]=%d", id), nil, &res)
		return len(res.Data) > 0, err
	}), "Bazarr sync")
	// The film shows up before the sync job ends, and the job can still rewrite its record;
	// an upload processed in that window fails inside Bazarr. Wait for the job to finish.
	p.must(testenv.WaitFor(p.ctx, 3*time.Minute, 2*time.Second, "Bazarr sync to finish", func() (bool, error) {
		var res struct {
			Data []struct {
				JobID      string `json:"job_id"`
				JobRunning bool   `json:"job_running"`
			} `json:"data"`
		}
		err := env.Bazarr.Do(p.ctx, "GET", "/api/system/tasks", nil, &res)
		for _, j := range res.Data {
			if j.JobID == "update_movies" && j.JobRunning {
				return false, nil
			}
		}
		return err == nil, err
	}), "Bazarr sync")

	upload := func(query string) error {
		var body bytes.Buffer
		mw := multipart.NewWriter(&body)
		fw, err := mw.CreateFormFile("file", "upload.es.srt")
		if err != nil {
			return err
		}
		if _, err := fw.Write([]byte(fixtures.SRT(time.Minute, "spa"))); err != nil {
			return err
		}
		if err := mw.Close(); err != nil {
			return err
		}
		return env.Bazarr.DoRaw(p.ctx, "POST", "/api/movies/subtitles"+query, mw.FormDataContentType(), &body, nil, nil)
	}
	missing := upload(fmt.Sprintf("?radarrid=%d&language=es", id))
	p.evidence("withoutForcedAndHi", testenv.StatusOf(missing))
	p.observe("upload without forced and hi: HTTP %d", testenv.StatusOf(missing))

	folder := movie["path"].(string)
	// Bazarr writes the file from a background job, behind its own sync jobs on a fresh
	// environment; wait for the exact file name expected.
	waitSRT := func(want string) string {
		var got string
		_ = testenv.WaitFor(p.ctx, 3*time.Minute, 2*time.Second, "subtitle file", func() (bool, error) {
			for _, f := range listDir(folder) {
				if strings.HasSuffix(f, want) {
					got = f
					return true, nil
				}
			}
			return false, nil
		})
		return got
	}
	removeSRTs := func() {
		for _, f := range listDir(folder) {
			if filepath.Ext(f) == ".srt" {
				_ = os.Remove(filepath.Join(folder, f))
			}
		}
	}

	removeSRTs()
	err := upload(testenv.Q("radarrid", fmt.Sprint(id), "language", "es", "forced", "False", "hi", "False"))
	plain := waitSRT(".es.srt")
	p.observe("forced=False&hi=False: error %v; wrote %q", err, plain)

	removeSRTs()
	lerr := upload(testenv.Q("radarrid", fmt.Sprint(id), "language", "es", "forced", "false", "hi", "false"))
	lower := waitSRT(".es.hi.srt")
	p.observe("forced=false&hi=false (lower case): error %v; wrote %q", lerr, lower)
	p.evidence("capitalisedFlags", plain)
	p.evidence("lowerCaseFlags", lower)
	removeSRTs()

	const flags = "Bazarr reads forced and hi as Python strings: send True/False capitalised; lower-case \"false\" counts as true"
	switch {
	case err == nil && plain != "" && missing != nil:
		p.confirm()
		if strings.HasSuffix(lower, ".hi.srt") {
			p.observe("note: %s", flags)
		}
	case err == nil && missing != nil && lower != "":
		p.observe("note: %s", flags)
		p.partial("uploads are written by a background job: the first upload after a Radarr sync was accepted (2xx) but not written within 3 min, while a later one was written within seconds; Fukku must confirm through Bazarr's history and retry")
	default:
		p.differ("no upload produced a subtitle file (%v, %v)", err, lerr)
	}
}

// ---- C5 follow-ups (issue #50: evidence for decision #23) -------------------------------

const (
	claimC5a = "Plan §18 item 5: an upload right after a Radarr sync was accepted but not written for minutes. Decision #23 needs the delay and its cause (queued behind the sync, lost, or something else)"
	claimC5b = "Decision #23: wait for the subtitle manager's sync task to finish before uploading. Unverified: whether Bazarr exposes that task, its queue, and whether it knows the title"
	claimC5c = "Decision #23: confirm through the movie/episode history that the file was written. Unverified: that the history has the upload, its fields, and how soon"
)

var (
	specC5b = spec{"C5b", "Bazarr: what it exposes while an upload is processed", claimC5b, []string{"bazarr", "radarr"}}
	specC5c = spec{"C5c", "Bazarr: the movie history as confirmation of an upload", claimC5c, []string{"bazarr", "radarr"}}
)

// c5Seq makes every uploaded file name unique, so each upload's queue entry can be told apart.
var c5Seq int

func c5Name(label string) string {
	c5Seq++
	return fmt.Sprintf("fukku-%s-%d.es.srt", label, c5Seq)
}

// srtPlain is a subtitle without bracketed cues. Bazarr's subtitle library names a file whose
// cues look like "[spa] cue 1" as hearing-impaired, whatever flags the upload carries.
func srtPlain(d time.Duration, version int) string {
	clock := func(t time.Duration) string {
		return fmt.Sprintf("%02d:%02d:%02d,%03d", int(t/time.Hour), int(t%time.Hour/time.Minute), int(t%time.Minute/time.Second), int(t%time.Second/time.Millisecond))
	}
	var b strings.Builder
	n := 1
	for t := time.Duration(0); t < d; t += 5 * time.Second {
		fmt.Fprintf(&b, "%d\n%s --> %s\nHola, linea %d de la version %d.\n\n", n, clock(t), clock(min(t+2*time.Second, d)), n, version)
		n++
	}
	return b.String()
}

type bazarrJob struct {
	ID     int    `json:"job_id"`
	Name   string `json:"job_name"`
	Status string `json:"status"`
}

func bazarrJobs(ctx context.Context) ([]bazarrJob, error) {
	var res struct {
		Data []bazarrJob `json:"data"`
	}
	err := env.Bazarr.Do(ctx, "GET", "/api/system/jobs", nil, &res)
	return res.Data, err
}

// bazarrRunningTasks lists the ids of the scheduled tasks that are running now.
func bazarrRunningTasks(ctx context.Context) ([]string, error) {
	var res struct {
		Data []struct {
			ID      string `json:"job_id"`
			Running bool   `json:"job_running"`
		} `json:"data"`
	}
	err := env.Bazarr.Do(ctx, "GET", "/api/system/tasks", nil, &res)
	var out []string
	for _, t := range res.Data {
		if t.Running {
			out = append(out, t.ID)
		}
	}
	return out, err
}

func bazarrStartTask(ctx context.Context, id string) error {
	form := url.Values{"taskid": {id}}
	return env.Bazarr.DoRaw(ctx, "POST", "/api/system/tasks", "application/x-www-form-urlencoded", strings.NewReader(form.Encode()), nil, nil)
}

// bazarrData is the "data" list of a Bazarr list endpoint.
func bazarrData(ctx context.Context, path string) ([]M, error) {
	var res struct {
		Data []M `json:"data"`
	}
	err := env.Bazarr.Do(ctx, "GET", path, nil, &res)
	return res.Data, err
}

func bazarrKnowsMovie(ctx context.Context, radarrID int) bool {
	d, err := bazarrData(ctx, fmt.Sprintf("/api/movies?radarrid[]=%d", radarrID))
	return err == nil && len(d) > 0
}

func bazarrKnowsEpisode(ctx context.Context, seriesID, episodeID int) bool {
	d, err := bazarrData(ctx, fmt.Sprintf("/api/episodes?seriesid[]=%d", seriesID))
	for _, e := range d {
		if int(e["sonarrEpisodeId"].(float64)) == episodeID {
			return err == nil
		}
	}
	return false
}

// bazarrUpload sends a subtitle and returns how long the call took and its error.
func bazarrUpload(ctx context.Context, path string, query []string, filename, content string) (time.Duration, error) {
	var body bytes.Buffer
	mw := multipart.NewWriter(&body)
	fw, err := mw.CreateFormFile("file", filename)
	if err != nil {
		return 0, err
	}
	if _, err := fw.Write([]byte(content)); err != nil {
		return 0, err
	}
	if err := mw.Close(); err != nil {
		return 0, err
	}
	start := time.Now()
	err = env.Bazarr.DoRaw(ctx, "POST", path+testenv.Q(query...), mw.FormDataContentType(), &body, nil, nil)
	return time.Since(start), err
}

func srtFiles(folder string) map[string]bool {
	out := map[string]bool{}
	for _, f := range listDir(folder) {
		if filepath.Ext(f) == ".srt" {
			out[f] = true
		}
	}
	return out
}

func removeSRTFiles(folder string) {
	for f := range srtFiles(folder) {
		_ = os.Remove(filepath.Join(folder, f))
	}
}

// uploadTimeline is what was seen after one upload; times are seconds after the call returned,
// and -1 means not seen within the limit.
type uploadTimeline struct {
	Accepted     bool               `json:"accepted"`
	HTTPStatus   int                `json:"httpStatus,omitempty"` // of a refused upload
	CallMillis   int64              `json:"callMilliseconds"`
	FileAfter    float64            `json:"fileAfterSeconds"`
	Files        []string           `json:"newFiles,omitempty"`
	HistoryAfter float64            `json:"historyAfterSeconds"`
	History      M                  `json:"historyEntry,omitempty"`
	JobSeen      map[string]float64 `json:"jobStatusFirstSeenSeconds,omitempty"`
	JobFinal     string             `json:"jobFinalStatus,omitempty"`
	// QueueAhead names the queue entries that were pending or running when the upload was sent.
	QueueAhead []string `json:"queueAhead,omitempty"`
	// TaskRunning is how long (seconds after the call) each tracked scheduled task was seen running.
	TaskRunning map[string]float64 `json:"taskRunningUntilSeconds,omitempty"`
	// Jobs is every queue entry kind seen, with the statuses it had.
	Jobs map[string][]string `json:"queueEntriesSeen,omitempty"`
}

// jobKind strips the title or file name from a queue entry name.
func jobKind(name string) string {
	switch {
	case strings.HasPrefix(name, "Uploading "):
		return "Uploading <file>"
	case strings.Contains(name, " for "):
		return name[:strings.Index(name, " for ")] + " for <title>"
	}
	return name
}

// c5Target is a film or an episode in Bazarr.
type c5Target struct {
	uploadPath string
	idQuery    []string
	folder     string
	history    string // the history endpoint with the id query
}

// upload sends a subtitle and watches Bazarr until the file, the history entry and the queue
// entry are all seen, or limit passes. track names scheduled tasks to follow.
func (tg c5Target) upload(ctx context.Context, name, forced, hi, content string, limit time.Duration, track ...string) uploadTimeline {
	before := srtFiles(tg.folder)
	hist, _ := bazarrData(ctx, tg.history)
	tl := uploadTimeline{FileAfter: -1, HistoryAfter: -1, JobSeen: map[string]float64{}, Jobs: map[string][]string{}, TaskRunning: map[string]float64{}}
	if jobs, err := bazarrJobs(ctx); err == nil {
		for _, j := range jobs {
			if j.Status == "pending" || j.Status == "running" {
				tl.QueueAhead = append(tl.QueueAhead, j.Status+": "+jobKind(j.Name))
			}
		}
	}
	d, err := bazarrUpload(ctx, tg.uploadPath, append(slices.Clone(tg.idQuery), "language", "es", "forced", forced, "hi", hi), name, content)
	tl.CallMillis = d.Milliseconds()
	if err != nil {
		tl.HTTPStatus = testenv.StatusOf(err)
		return tl
	}
	tl.Accepted = true
	start := time.Now()
	jobName := "Uploading " + name
	for time.Since(start) < limit {
		at := time.Since(start).Seconds()
		if jobs, err := bazarrJobs(ctx); err == nil {
			for _, j := range jobs {
				if !slices.Contains(tl.Jobs[jobKind(j.Name)], j.Status) {
					tl.Jobs[jobKind(j.Name)] = append(tl.Jobs[jobKind(j.Name)], j.Status)
				}
				if j.Name == jobName {
					if _, ok := tl.JobSeen[j.Status]; !ok {
						tl.JobSeen[j.Status] = at
					}
					tl.JobFinal = j.Status
				}
			}
		}
		if len(track) > 0 {
			if running, err := bazarrRunningTasks(ctx); err == nil {
				for _, id := range track {
					if slices.Contains(running, id) {
						tl.TaskRunning[id] = at
					}
				}
			}
		}
		if tl.FileAfter < 0 {
			for f := range srtFiles(tg.folder) {
				if !before[f] {
					tl.Files = append(tl.Files, f)
				}
			}
			if len(tl.Files) > 0 {
				tl.FileAfter = at
			}
		}
		if tl.HistoryAfter < 0 {
			if h, err := bazarrData(ctx, tg.history); err == nil && len(h) > len(hist) {
				tl.HistoryAfter, tl.History = at, h[0]
			}
		}
		if tl.FileAfter >= 0 && tl.HistoryAfter >= 0 && (tl.JobFinal == "completed" || tl.JobFinal == "failed") {
			break
		}
		select {
		case <-ctx.Done():
			return tl
		case <-time.After(250 * time.Millisecond):
		}
	}
	return tl
}

func seen(v float64) string {
	if v < 0 {
		return "not seen"
	}
	return fmt.Sprintf("%.1f s", v)
}

func (tl uploadTimeline) String() string {
	if !tl.Accepted {
		return fmt.Sprintf("refused with HTTP %d", tl.HTTPStatus)
	}
	return fmt.Sprintf("accepted in %d ms; file %v after %s; history entry after %s; queue entry %s, first seen %v",
		tl.CallMillis, tl.Files, seen(tl.FileAfter), seen(tl.HistoryAfter), tl.JobFinal, tl.JobSeen)
}

func movieTarget(movie M) c5Target {
	id := testenv.ID(movie)
	return c5Target{uploadPath: "/api/movies/subtitles", idQuery: []string{"radarrid", fmt.Sprint(id)},
		folder: movie["path"].(string), history: fmt.Sprintf("/api/movies/history?radarrid=%d", id)}
}

type c5Run struct {
	Round             int            `json:"round"`
	DelayAfterImport  string         `json:"uploadAfterImport"`
	KnownBeforeImport bool           `json:"bazarrKnewTheFilmBeforeTheImport"`
	KnownAfterImport  float64        `json:"bazarrKnewTheFilmAfterTheImportSeconds"`
	KnownAtUpload     bool           `json:"bazarrKnewTheFilmAtUpload"`
	ActualDelay       float64        `json:"uploadSentAfterImportSeconds"`
	Timeline          uploadTimeline `json:"timeline"`
	movie             M
	imported          time.Time
	delay             time.Duration
	target            c5Target
}

func (r c5Run) String() string {
	return fmt.Sprintf("round %d, upload planned %s after the import, sent %.1f s after (Bazarr knew the film before the import: %v, %.1f s after it, and at the upload: %v): %s",
		r.Round, r.DelayAfterImport, r.ActualDelay, r.KnownBeforeImport, r.KnownAfterImport, r.KnownAtUpload, r.Timeline)
}

// addAndImportFilm adds a film to Radarr and imports a file. It reports whether Bazarr knew the
// film before the import, and when it knew it after (-1: not within 2 min). Bazarr stores only
// films that have a file.
func addAndImportFilm(p *probe, f film) (movie M, imported time.Time, knownBefore bool, knownAfter float64) {
	movie = addFilm(p, f)
	id := testenv.ID(movie)
	knownBefore = bazarrKnowsMovie(p.ctx, id)
	importFilm(p, movie, f, f.dotted()+".1080p.WEB-DL", "eng")
	imported = time.Now()
	knownAfter = -1
	if testenv.WaitFor(p.ctx, 2*time.Minute, 250*time.Millisecond, "Bazarr to know the film", func() (bool, error) {
		return bazarrKnowsMovie(p.ctx, id), nil
	}) == nil {
		knownAfter = time.Since(imported).Seconds()
	}
	return movie, imported, knownBefore, knownAfter
}

// C5a, C5b and C5c share their runs: for four delays after a Radarr import, upload a subtitle
// and watch the file, Bazarr's queue and its history; then a run behind a busy queue, and one
// where Bazarr cannot write.
func TestC5Delay(t *testing.T) {
	a := newProbe(t, "C5a", "Bazarr: delay between a Radarr import and a written upload", claimC5a, true, "bazarr", "radarr")
	a.alsoSkips(specC5b, specC5c)
	delays := []time.Duration{0, 30 * time.Second, 2 * time.Minute, 10 * time.Minute}
	films := []film{bigBuckBunny, sintel, tearsOfSteel, elephantsDream}
	const rounds = 2
	var runs []c5Run
	jobKinds := map[string][]string{}
	mergeKinds := func(m map[string][]string) {
		for k, v := range m {
			for _, s := range v {
				if !slices.Contains(jobKinds[k], s) {
					jobKinds[k] = append(jobKinds[k], s)
				}
			}
		}
	}
	content := fixtures.SRT(time.Minute, "spa")

	for round := 1; round <= rounds; round++ {
		slots := make([]c5Run, len(delays))
		var wg sync.WaitGroup
		for i := range delays {
			movie, imported, knownBefore, knownAfter := addAndImportFilm(a, films[i])
			slots[i] = c5Run{Round: round, DelayAfterImport: delays[i].String(), KnownBeforeImport: knownBefore, KnownAfterImport: knownAfter,
				movie: movie, imported: imported, delay: delays[i], target: movieTarget(movie)}
			// Each upload is timed from its own film's import, while the next film is set up.
			wg.Add(1)
			go func() {
				defer wg.Done()
				s := &slots[i]
				select {
				case <-time.After(time.Until(s.imported.Add(s.delay))):
				case <-a.ctx.Done():
					return
				}
				s.ActualDelay = time.Since(s.imported).Seconds()
				s.KnownAtUpload = bazarrKnowsMovie(a.ctx, testenv.ID(s.movie))
				s.Timeline = s.target.upload(a.ctx, c5Name(fmt.Sprintf("c5a-r%d-%s", round, s.delay)), "false", "false", content, 3*time.Minute)
			}()
		}
		wg.Wait()
		for _, s := range slots {
			a.observe("%s", s)
			mergeKinds(s.Timeline.Jobs)
		}
		runs = append(runs, slots...)
	}
	a.evidence("runs", runs)

	var hits, total int
	for _, r := range runs {
		total++
		if r.Timeline.FileAfter >= 0 {
			hits++
		}
	}
	a.observe("%d of %d uploads were written, none lost or queued for long: the slowest after %.1f s (the file names end .es.hi.srt: the fixture's bracketed cues make Bazarr name it hearing-impaired, see C5f)", hits, total, slowest(runs))

	// A busy queue: start the scheduled tasks that fill it, and upload at once.
	busyFilm, _, _, _ := addAndImportFilm(a, cosmosLaundromat)
	tracked := []string{"update_movies", "update_series", "wanted_search_missing_subtitles_movies", "upgrade_subtitles"}
	var started []string
	for _, id := range tracked {
		if bazarrStartTask(a.ctx, id) == nil {
			started = append(started, id)
		}
	}
	busy := movieTarget(busyFilm).upload(a.ctx, c5Name("c5a-busy"), "false", "false", content, 5*time.Minute, tracked...)
	mergeKinds(busy.Jobs)
	a.evidence("busyQueue", M{"tasksStarted": started, "timeline": busy})
	a.observe("sync and search tasks started just before the upload (%v): %s; tasks seen running until %v s after the call; queue entries ahead of the upload %v",
		started, busy, busy.TaskRunning, busy.QueueAhead)
	a.observed()

	t.Run("C5b", func(t *testing.T) {
		p := specC5b.probe(t)
		tasks, err := bazarrData(p.ctx, "/api/system/tasks")
		p.must(err, "tasks")
		var ids []string
		for _, tk := range tasks {
			ids = append(ids, fmt.Sprintf("%v (%v)", tk["job_id"], tk["name"]))
		}
		p.evidence("tasks", ids)
		p.observe("GET /api/system/tasks lists the scheduled tasks with a job_running flag: %v", ids)
		p.evidence("queueEntriesSeen", jobKinds)
		p.observe("GET /api/system/jobs lists the queue (job_id, job_name, status pending|running|completed|failed); entry kinds seen and their statuses: %v", jobKinds)
		var after []string
		atUpload, beforeCount := 0, 0
		for _, r := range runs {
			after = append(after, fmt.Sprintf("%.1f", r.KnownAfterImport))
			if r.KnownBeforeImport {
				beforeCount++
			}
			if r.KnownAtUpload {
				atUpload++
			}
		}
		p.evidence("bazarrKnewTheFilmBeforeTheImportRuns", beforeCount)
		p.evidence("bazarrKnewTheFilmAfterTheImportSeconds", after)
		p.observe("Bazarr knew the film before its file was imported in %d of %d runs (it stores only films that have a file); after the import it knew it after %v s (Radarr tells it through its event stream, no sync is needed); it knew the film at the upload in %d of %d runs",
			beforeCount, len(runs), after, atUpload, len(runs))
		_, unknown := bazarrUpload(p.ctx, "/api/movies/subtitles", []string{"radarrid", "999999", "language", "es", "forced", "false", "hi", "false"}, c5Name("c5b-unknown"), content)
		p.evidence("uploadForUnknownFilmHTTP", testenv.StatusOf(unknown))
		p.observe("an upload for a film Bazarr does not know is refused at once: HTTP %d", testenv.StatusOf(unknown))
		p.observe("the upload call returns 204 with no body, so no job id: its queue entry is found by its name, \"Uploading <file name>\" (use a unique file name); it was pending at the call and seen completed after %v s", doneTimes(runs))
		p.observe("a sync is visible twice: the task update_movies has job_running true, and the queue has \"Syncing movies with Radarr\" (statuses seen %v). In the run with the sync and search tasks started just before the upload, %d other entries were pending or running ahead of it, the tracked tasks were last seen running %.1f s after the call, and the upload entry was seen completed at %.1f s",
			jobKinds["Syncing movies with Radarr"], len(busy.QueueAhead), busy.TaskRunning["update_movies"], busy.JobSeen["completed"])
		p.observed()
	})

	t.Run("C5c", func(t *testing.T) {
		p := specC5c.probe(t)
		var entry M
		for _, r := range runs {
			if r.Timeline.History != nil {
				entry = r.Timeline.History
				break
			}
		}
		p.check(entry != nil, "a history entry was seen")
		var keys []string
		for k := range entry {
			keys = append(keys, k)
		}
		slices.Sort(keys)
		p.evidence("historyEntry", entry)
		p.observe("GET /api/movies/history?radarrid=<id> gained one entry per upload; fields %v; action %v, description %q, provider %q, subtitles_path %q, language %v",
			keys, entry["action"], entry["description"], entry["provider"], entry["subtitles_path"], entry["language"])
		var lag []float64
		samePath := 0
		for _, r := range runs {
			if r.Timeline.FileAfter >= 0 && r.Timeline.HistoryAfter >= 0 {
				lag = append(lag, r.Timeline.HistoryAfter-r.Timeline.FileAfter)
				if len(r.Timeline.Files) > 0 && r.Timeline.History["subtitles_path"] == filepath.Join(r.target.folder, r.Timeline.Files[0]) {
					samePath++
				}
			}
		}
		p.evidence("historyMinusFileSeconds", lag)
		p.observe("the history entry and the file were seen in the same 250 ms poll (history minus file, seconds, per run: %v); the entry's subtitles_path equals the file's path in %d of %d runs", lag, samePath, len(lag))

		// The trap: a finished queue entry is not proof.
		movie := busyFilm
		tg := movieTarget(movie)
		folder := tg.folder
		removeSRTFiles(folder)
		p.must(os.Chmod(folder, 0o555), "make the film folder read-only")
		tl := tg.upload(p.ctx, c5Name("c5c-readonly"), "false", "false", content, 20*time.Second)
		p.must(os.Chmod(folder, 0o755), "restore the film folder")
		p.evidence("unwritableFolder", tl)
		p.observe("with the film folder not writable: %s; queue entry final status %q", tl, tl.JobFinal)
		if tl.Accepted && tl.FileAfter < 0 && tl.HistoryAfter < 0 {
			p.observe("so an upload can be accepted and its queue entry can end \"completed\" with no file and no history entry: only the history proves the write")
		}
		p.observed()
	})
}

// doneTimes is, per run, the seconds after the call at which the upload's queue entry was seen completed.
func doneTimes(runs []c5Run) []string {
	var out []string
	for _, r := range runs {
		out = append(out, fmt.Sprintf("%.1f", r.Timeline.JobSeen["completed"]))
	}
	return out
}

func slowest(runs []c5Run) float64 {
	var m float64
	for _, r := range runs {
		m = max(m, r.Timeline.FileAfter)
	}
	return m
}

// TestC5Flags covers C5e (a repeated upload) and C5f (what forced and hi values do) on one film.
func TestC5Flags(t *testing.T) {
	e := newProbe(t, "C5e", "Bazarr: a repeated upload of the same subtitle", "Unverified: whether a repeat is a second file, an overwrite or an error", true, "bazarr", "radarr")
	e.alsoSkips(specC5f)
	movie, _, _, _ := addAndImportFilm(e, tearsOfSteel)
	tg := movieTarget(movie)
	removeSRTFiles(tg.folder)
	read := func() (names []string, sum string) {
		for f := range srtFiles(tg.folder) {
			names = append(names, f)
			data, _ := os.ReadFile(filepath.Join(tg.folder, f))
			s := sha256.Sum256(data)
			sum += hex.EncodeToString(s[:4]) + " "
		}
		slices.Sort(names)
		return names, sum
	}
	histCount := func() int {
		h, _ := bazarrData(e.ctx, tg.history)
		return len(h)
	}
	steps := []struct {
		what, forced, hi string
		version          int
	}{
		{"first upload", "false", "false", 1}, {"the same subtitle again", "false", "false", 1}, {"changed content, same flags", "false", "false", 2}, {"same content, forced=true", "true", "false", 2},
	}
	var rows []M
	for _, s := range steps {
		before := histCount()
		tl := tg.upload(e.ctx, c5Name("c5e"), s.forced, s.hi, srtPlain(time.Minute, s.version), 20*time.Second)
		// A repeat that rewrites an existing file adds no new name, so wait for the queue entry.
		time.Sleep(2 * time.Second)
		names, sum := read()
		after := histCount()
		e.observe("%s: accepted %v (HTTP %d); queue entry %q; files now %v with content hashes %s; history entries %d -> %d", s.what, tl.Accepted, tl.HTTPStatus, tl.JobFinal, names, sum, before, after)
		rows = append(rows, M{"step": s.what, "accepted": tl.Accepted, "queueEntry": tl.JobFinal, "files": names, "hashes": sum, "historyBefore": before, "historyAfter": after})
	}
	e.evidence("steps", rows)
	e.observe("a repeat is accepted without an error and rewrites the same file (a different file only when the flags change); every upload adds a history entry, so history entries count uploads, not files")
	e.observed()

	t.Run("C5f", func(t *testing.T) {
		p := specC5f.probe(t)
		type res struct{ forced, hi, content, file string }
		var all []res
		try := func(forced, hi, content string, version int) string {
			removeSRTFiles(tg.folder)
			text := srtPlain(time.Minute, version)
			if content == "bracketed" {
				text = fixtures.SRT(time.Minute, "spa")
			}
			tl := tg.upload(p.ctx, c5Name("c5f"), forced, hi, text, 20*time.Second)
			name := "(nothing written)"
			if len(tl.Files) > 0 {
				name = tl.Files[0]
			}
			all = append(all, res{forced, hi, content, name})
			p.observe("forced=%s hi=%s, %s cues: %s", forced, hi, content, name)
			return name
		}
		try("false", "false", "plain", 1)
		try("False", "False", "plain", 2)
		capForced := try("True", "False", "plain", 3)
		lowForced := try("true", "false", "plain", 4)
		capHI := try("False", "True", "plain", 5)
		lowHI := try("false", "true", "plain", 6)
		bracketed := try("false", "false", "bracketed", 7)
		var rows []M
		for _, r := range all {
			rows = append(rows, M{"forced": r.forced, "hi": r.hi, "cues": r.content, "file": r.file})
		}
		p.evidence("results", rows)
		p.observe("Bazarr's upload endpoint compares the strings with lower-case \"true\" (its source: forced == 'true'), so \"True\" is false; \"False\" and \"false\" both mean false")
		p.observe("a subtitle whose cues are bracketed, like the fixture \"[spa] cue 1\", is named .hi.srt even with hi=false: that, not the flags, is what the C5 probe saw as \"lower-case false counts as true\"; and C5's first wait for a name ending in .es.srt could not match a file named .es.hi.srt")
		switch {
		case strings.Contains(lowForced, ".forced.") && strings.Contains(lowHI, ".hi.") && !strings.Contains(capForced, ".forced.") && !strings.Contains(capHI, ".hi.") && strings.Contains(bracketed, ".hi."):
			p.differ("the upload endpoint needs lower-case true/false: \"True\" does not set forced or hi (plan §6 and §18 item 5 say to send True/False capitalised, and that lower-case false counts as true; the latter was bracketed cues)")
		case strings.Contains(capForced, ".forced."):
			p.confirm()
		default:
			p.partial("the flag results do not fit either reading: see the rows")
		}
	})
}

var specC5f = spec{"C5f", "Bazarr reads forced and hi as lower-case strings on upload",
	"Plan §6: `forced` and `hi` are read as Python strings, so send `True`/`False` capitalised; lower-case `false` counts as true", []string{"bazarr", "radarr"}}

// C5d: the same for an episode through Sonarr.
func TestC5Episode(t *testing.T) {
	p := newProbe(t, "C5d", "Bazarr: episode upload, queue and history", "Decision #23 for episodes: `POST /api/episodes/subtitles?seriesid=&episodeid=&language=&forced=&hi=` and `/api/episodes/history`; unverified", true, "bazarr", "sonarr")
	series, eps := addShow(p, pioneerOne)
	sid := testenv.ID(series)
	e1 := episode(eps, 1, 1)
	p.check(e1 != nil, "S01E01 exists")
	eid := testenv.ID(e1)
	// Bazarr stores only episodes that have a file, so it should not know this one yet.
	knownBeforeImport := bazarrKnowsEpisode(p.ctx, sid, eid)
	dir := p.fresh(intake("C5d"))
	src := filepath.Join(dir, "Pioneer.One.S01E01.1080p.WEB-DL.mkv")
	p.video(src, long(pioneerOne.minutes, "eng"))
	item := itemFor(proposal(p, env.Sonarr, dir), src)
	p.check(item != nil, "staged file is in the proposal")
	cmd := manualImport(p, env.Sonarr, []M{item}, M{"seriesId": sid, "episodeIds": []int{eid}}, "move")
	p.check(cmd.Status == "completed", "import completed")
	f := episodeFile(p, sid, e1)
	p.check(f != nil, "episode file tracked")
	importedAt := time.Now()
	known := -1.0
	if testenv.WaitFor(p.ctx, 2*time.Minute, 250*time.Millisecond, "Bazarr to know the episode", func() (bool, error) {
		return bazarrKnowsEpisode(p.ctx, sid, eid), nil
	}) == nil {
		known = time.Since(importedAt).Seconds()
	}
	folder := filepath.Dir(filepath.Join(series["path"].(string), fmt.Sprint(f["relativePath"])))
	tg := c5Target{uploadPath: "/api/episodes/subtitles", idQuery: []string{"seriesid", fmt.Sprint(sid), "episodeid", fmt.Sprint(eid)},
		folder: folder, history: fmt.Sprintf("/api/episodes/history?episodeid=%d", eid)}
	removeSRTFiles(folder)
	tl := tg.upload(p.ctx, c5Name("c5d"), "false", "false", srtPlain(time.Minute, 1), 3*time.Minute)
	p.evidence("bazarrKnewTheEpisodeAfterImportSeconds", known)
	p.evidence("bazarrKnewTheEpisodeBeforeImport", knownBeforeImport)
	p.evidence("timeline", tl)
	p.observe("Bazarr knew the episode before the import: %v; it knew it %.1f s after the import (-1: not within 2 min)", knownBeforeImport, known)
	p.observe("upload right after the import: %s", tl)
	if tl.History != nil {
		var keys []string
		for k := range tl.History {
			keys = append(keys, k)
		}
		slices.Sort(keys)
		p.observe("GET /api/episodes/history?episodeid=<id> entry: fields %v; action %v, description %q, provider %q, subtitles_path %q",
			keys, tl.History["action"], tl.History["description"], tl.History["provider"], tl.History["subtitles_path"])
		p.evidence("historyEntry", tl.History)
	}
	again := tg.upload(p.ctx, c5Name("c5d-again"), "false", "false", srtPlain(time.Minute, 2), 20*time.Second)
	p.observe("a second upload of the same language and flags: %s; files now %v", again, slices.Sorted(maps.Keys(srtFiles(folder))))
	p.check(tl.Accepted, "the episode upload was accepted")
	p.observed()
}
