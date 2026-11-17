package flows

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"slices"
	"strconv"
	"strings"

	"golang.org/x/text/language"
	"golang.org/x/text/language/display"

	"github.com/segun-io/fukku/internal/arr"
	"github.com/segun-io/fukku/internal/config"
	"github.com/segun-io/fukku/internal/jobdir"
	"github.com/segun-io/fukku/internal/media"
	"github.com/segun-io/fukku/internal/merge"
	"github.com/segun-io/fukku/internal/runner"
	"github.com/segun-io/fukku/internal/spool"
	"github.com/segun-io/fukku/internal/store"
)

// Flow 2a, the merge (#55, plan §8 2a, decision B6). The upload is the same cut as the
// film's library file and the user has watched the sync check's clips (#53): its new audio
// (and subtitle) tracks are added to a copy of the library file by the worker (the merge
// task, #54, package merge), and Radarr replaces its file with the verified copy through
// Manual Import. The library file itself is never written: only Radarr replaces it.
//
// The confirmation (POST /api/jobs/{id}/confirm):
//
//	{"flow2": "merge", "merge": {"audioTracks": [2], "subtitleTracks": [], "offsetMs": -800,
//	                             "languageTag": "es-419", "trackName": "Español (Latino)",
//	                             "keepOriginalDefault": true}}
//
// Every field of "merge" is optional: audioTracks is the preview's track, subtitleTracks
// none, offsetMs the preview's, and the other three merge.languageTag, merge.trackName and
// merge.keepOriginalDefault. Tracks are the upload's ffprobe stream indexes
// (flow2.upload.audio and .subtitles). "subtitles" (the job's subtitle files, as for an
// import) may come with it; overrides and seasonDub may not. In this order:
//
//   - The job must be a Radarr flow 2 job awaiting confirmation, its library file probed,
//     and "merge" among the offers (merge.enabled, the finding match); else
//     merge_not_offered (or wrong_state).
//   - The sync check: the job's last preview (PreviewOf) must be ready, of the same upload,
//     with the merge's offset, and its audio track among the merge's; else
//     preview_required. Every added track gets the offset (mkvmerge --sync, positive plays
//     later), subtitles too, as the preview showed it.
//   - The tracks exist in the upload, at least one audio track, each once; the language
//     tag is canonical BCP 47; the track name valid (an empty one only when
//     merge.trackName is empty, since the worker would fill in its own); else bad_request.
//   - The filesystem checks of the import (storage_unavailable).
//   - Radarr's tracked file must still be the match's (movie.movieFile's id and size): if
//     it changed, the job goes back to probed (rescan) and the confirmation is refused
//     with library_changed.
//   - Radarr's language for the tag (arrLanguage; none when Radarr has no such language:
//     the file then keeps the existing file's languages only).
//   - Then, under the job's lock, the target is read again: a preview requested meanwhile
//     refuses the confirmation (preview_required), so a newer preview is never written
//     over; one transaction stores the choice (Target.Options["merge"]), the audit entry
//     merge.confirmed and the move to importing. Then the preview's task slots are
//     cancelled (all of runner.PreviewSlots).
//
// The importing step (mergeStep, before the import engine's own) is safe to repeat at
// every point, like the import:
//
//   - The merge task (runner.MergeID, one per attempt, into runner.MergeDest: merged/<attempt>/).
//     Before it is submitted in an attempt: the library file must be under
//     paths.libraryRoots (library_unreadable), the upload still in the job, the sentinels
//     there (merge_failed), and Radarr's tracked file still the match's (else back to
//     probed). The earlier attempts' merge tasks and the preview slots are cancelled and
//     their results acknowledged. The attempt is recorded, then the task is submitted
//     (again on each pass until it has a result; Submit is idempotent). The job stalls
//     with worker_unavailable while the worker's heartbeat is too old.
//   - Its result: a refusal fails the job with merge_failed (the merge view's refusal says
//     which, merge.Refusal*); library_changed (the file changed while it was read) and a
//     verified output from a file of another size than the tracked one send the job back to
//     probed; a task error fails it with task_failed or task_timeout; a cancelled run and
//     busy are submitted again. A verified output must be where the attempt's task writes,
//     with the language tag, the name and the default flag that were confirmed, and on
//     disk at its size. It is registered as a file of the job (a video under merged/) and
//     in the choice, then the result is acknowledged.
//   - The merged file's row: Radarr's proposal of its folder (never with a title id), the
//     item re-checked with the film's id, the existing file's quality and languages, plus
//     Radarr's language for the added tracks. A rejection other than Radarr's refusal to
//     replace the file it has ("not an upgrade", which the confirmation answers) fails the
//     job with merge_rejected, as does a file Radarr does not offer. The row is stored.
//   - The import engine (import.go) imports the row: Radarr replaces its file (probe
//     M3-merge). Before each submission Radarr's tracked file is checked once more; if it
//     changed, the job goes back to probed and nothing is imported.
//
// Every failure before the import leaves the library as it was. A merged file that is not
// imported is removed once its task has ended (a refused or unusable result, going back to
// probed, an earlier attempt's late result), and merged/ is emptied when the job is done; a
// verified file stays while the job fails after it, for the retry. The imported step
// unmonitors the film when merge.unmonitorAfterMerge is on (B6), records it in the job's
// result (unmonitorReason merged) and says what can still replace the file (the merge
// result's warnings). The upload and the merged file stay in the job directory until the
// job is removed; with importMode move Radarr takes the merged file.
//
// JobDetail shows the merge in "import.merge" (MergeView) while and after it runs, and
// "result.merge" (MergeResult) once the file is imported.

// Codes of the merge.
const (
	// CodePreviewRequired refuses a merge whose sync check is missing: the job's preview is
	// not ready, is of another upload, or has another offset or audio track (or a new one
	// was requested meanwhile).
	CodePreviewRequired = "preview_required"
	// A changed tracked file refuses the confirmation with CodeLibraryChanged (version.go),
	// as for a second version.

	// CodeMergeFailed fails a job whose merge could not be made: the merge task refused
	// (MergeView.Refusal says why), or a check before it failed.
	CodeMergeFailed = "merge_failed"
	// CodeMergeRejected fails a job whose merged file Radarr does not take: not in the
	// proposal of its folder, or rejected for another reason than replacing the film's file.
	CodeMergeRejected = "merge_rejected"
)

// Warnings of a merge's result (MergeResult.Warnings), for the UI (B6).
const (
	// WarnSearchCanReplace: the film is unmonitored, so RSS sync and the Wanted searches
	// leave the merged file alone, but the film's own Search in Radarr can still replace it
	// with a release without the dub.
	WarnSearchCanReplace = "search_can_replace"
	// WarnUpgradeCanReplace: the film is still monitored (merge.unmonitorAfterMerge off),
	// so any better release without the dub can replace the merged file.
	WarnUpgradeCanReplace = "upgrade_can_replace"
	// WarnLanguageUnknown: Radarr has no language for the added tracks' tag, so in Radarr the
	// merged file carries only the existing file's languages (the tracks themselves are
	// tagged; custom formats and searches by language do not see them).
	WarnLanguageUnknown = "language_unknown_to_radarr"
)

var mergeWarningText = map[string]string{
	WarnLanguageUnknown: "Radarr has no language for the added tracks, so it lists the merged file with the existing file's languages only. " +
		"The tracks themselves carry the language tag; the media server shows them.",
	WarnSearchCanReplace: "The film is unmonitored, so Radarr's RSS sync and its Wanted searches leave the merged file alone. " +
		"The film's own Search in Radarr can still replace it with a release that has no dub, and the added language is then lost.",
	WarnUpgradeCanReplace: "The film is still monitored (merge.unmonitorAfterMerge is off): a better release without the dub, " +
		"found by RSS sync or a search, can replace the merged file, and the added language is then lost.",
}

// MergeRequest is ConfirmRequest.Merge. Every field is optional (see the top of this file).
type MergeRequest struct {
	AudioTracks         []int   `json:"audioTracks"`
	SubtitleTracks      []int   `json:"subtitleTracks"`
	OffsetMs            *int64  `json:"offsetMs"`
	LanguageTag         *string `json:"languageTag"`
	TrackName           *string `json:"trackName"`
	KeepOriginalDefault *bool   `json:"keepOriginalDefault"`
}

// mergeChoice is what the confirmation stores in the target's options ("merge"), and the
// importing step's progress. LibraryFileID and LibrarySize are Radarr's tracked file at the
// match, checked again before the merge and before the import.
type mergeChoice struct {
	UploadFileID        int64  `json:"uploadFileId"`
	Upload              string `json:"upload"` // job-relative, under files/
	AudioTracks         []int  `json:"audioTracks"`
	SubtitleTracks      []int  `json:"subtitleTracks"`
	OffsetMs            int64  `json:"offsetMs"`
	LanguageTag         string `json:"languageTag"`
	TrackName           string `json:"trackName"`
	KeepOriginalDefault bool   `json:"keepOriginalDefault"`
	ArrLanguage         *Ref   `json:"arrLanguage"` // Radarr's language of the added tracks; nil: none
	Preview             string `json:"preview"`     // the preview request the sync was checked with
	LibraryFileID       int    `json:"libraryFileId"`
	LibrarySize         int64  `json:"librarySize"`

	// Attempt is the job attempt whose task makes the merge (runner.MergeID, into
	// runner.MergeDest); 0 until the first is submitted.
	Attempt int `json:"attempt,omitempty"`
	// Refusal is the merge task's refusal when it failed the job.
	Refusal string `json:"refusal,omitempty"`
	// Output is the verified merged file, once it is in.
	Output *mergedFile `json:"output,omitempty"`
}

type mergedFile struct {
	FileID int64  `json:"fileId"` // the job file registered for it
	Path   string `json:"path"`   // job-relative: merged/<attempt>/<name>
	Size   int64  `json:"size"`
}

// mergeOf returns the merge choice of a confirmed flow 2a job; ok is false for any other.
func mergeOf(t store.Target) (mergeChoice, bool) {
	var opts struct {
		Merge *mergeChoice `json:"merge"`
	}
	if len(t.Options) > 0 {
		_ = json.Unmarshal(t.Options, &opts)
	}
	if opts.Merge == nil || opts.Merge.Upload == "" {
		return mergeChoice{}, false
	}
	return *opts.Merge, true
}

// IsMerge reports whether the job of target t merges its upload into the film's file (it
// was confirmed so).
func IsMerge(t store.Target) bool {
	_, ok := mergeOf(t)
	return ok
}

// MergedFile returns, for a merge job whose merged file is in, the upload's file id and the
// merged file's: the subtitle step hands the upload's subtitle files over for the merged
// file, which is what was imported.
func MergedFile(t store.Target) (uploadFileID, mergedFileID int64, ok bool) {
	c, found := mergeOf(t)
	if !found || c.Output == nil {
		return 0, 0, false
	}
	return c.UploadFileID, c.Output.FileID, true
}

// ---- the confirmation ---------------------------------------------------------------------

// confirmMerge is the confirmation of a flow 2 job as a merge (see the top of this file).
// s is the job's session, rec its flow 2 record.
func (e *Engine) confirmMerge(ctx context.Context, user, email string, s *session, rec Flow2, req ConfirmRequest) (Confirmation, error) {
	cfg := e.p.cfg
	if len(req.Overrides) > 0 || req.SeasonDub != "" {
		return Confirmation{}, badRequest("A merge takes flow2, merge and subtitles only: no overrides or seasonDub.")
	}
	if s.t.App != AppRadarr {
		return Confirmation{}, refuse(CodeMergeNotOffered, "Merges are made for Radarr's films only.")
	}
	if rec.Library.Probe == nil {
		return Confirmation{}, refuse(CodeWrongState, "The film's library file is not compared yet.")
	}
	files, err := e.st.Files(ctx, s.job.ID)
	if err != nil {
		return Confirmation{}, err
	}
	f, up, ok := Flow2Upload(files)
	if !ok {
		return Confirmation{}, refuse(CodeMergeNotOffered, "The job has no video to merge.")
	}
	cmp := CompareProbes(*rec.Library.Probe, up, cfg.Match)
	if offers, notOffered := Offers(cmp.Finding, cfg); !slices.Contains(offers, OfferMerge) {
		why := ""
		for _, n := range notOffered {
			if n.Offer == OfferMerge {
				why = n.Reason
			}
		}
		return Confirmation{}, refuse(CodeMergeNotOffered, "The upload cannot be merged into the library file (%s).", why)
	}
	var mr MergeRequest
	if req.Merge != nil {
		mr = *req.Merge
	}
	pv, hasPreview := PreviewOf(s.t)
	offset := pv.OffsetMs
	if mr.OffsetMs != nil {
		offset = *mr.OffsetMs
	}
	if !hasPreview || pv.State != PreviewReady || pv.UploadFileID != f.ID || pv.OffsetMs != offset {
		return Confirmation{}, refuse(CodePreviewRequired, "Check the sync first: render the preview clips with an offset of %d ms, watch them, then confirm the merge.", offset)
	}
	audio := mr.AudioTracks
	if audio == nil {
		audio = []int{pv.AudioTrack}
	}
	if len(audio) == 0 {
		return Confirmation{}, badRequest("A merge adds at least one audio track (audioTracks).")
	}
	if !slices.Contains(audio, pv.AudioTrack) {
		return Confirmation{}, refuse(CodePreviewRequired, "The preview checked audio track %d: render the clips with the track to be merged, then confirm.", pv.AudioTrack)
	}
	subs := mr.SubtitleTracks
	if subs == nil {
		subs = []int{}
	}
	if err := checkTracks(up, audio, subs); err != nil {
		return Confirmation{}, err
	}
	tag := cfg.Merge.LanguageTag
	if mr.LanguageTag != nil {
		tag = *mr.LanguageTag
	}
	if c, ok := config.CanonicalLanguageTag(tag); !ok {
		if c != "" {
			return Confirmation{}, badRequest("languageTag %q is not in canonical BCP 47 form: use %q.", tag, c)
		}
		return Confirmation{}, badRequest("languageTag %q is not a BCP 47 language tag such as es-419.", tag)
	}
	name := cfg.Merge.TrackName
	if mr.TrackName != nil {
		name = *mr.TrackName
	}
	switch {
	case !config.ValidTrackName(name):
		return Confirmation{}, badRequest("trackName must be text of at most %d bytes, without control characters.", config.MaxTrackName)
	case name == "" && cfg.Merge.TrackName != "":
		return Confirmation{}, badRequest("trackName cannot be empty while merge.trackName is set: leave it out for %q.", cfg.Merge.TrackName)
	}
	keep := cfg.Merge.KeepOriginalDefault
	if mr.KeepOriginalDefault != nil {
		keep = *mr.KeepOriginalDefault
	}
	sidecars, err := s.subtitleChoices(req.Subtitles)
	if err != nil {
		return Confirmation{}, err
	}
	if err := e.checkStorage(); err != nil {
		e.log.Warn("import_storage_unavailable", "job", s.job.ID, "error", err)
		return Confirmation{}, refuse(CodeStorageUnavailable, "The storage is not available: a mount is missing, or the intake and library folders are on different filesystems.")
	}
	if e.p.radarr == nil {
		return Confirmation{}, appFailure(AppRadarr, errors.New("not configured"))
	}
	// Radarr's tracked file must still be the match's: else the comparison and the clips
	// were of another file, and the job goes back to probed, like a stale library probe.
	m, err := e.p.radarr.Movie(ctx, s.t.TitleID)
	if err != nil {
		return Confirmation{}, appFailure(AppRadarr, err)
	}
	if why := trackedChanged(rec.Library, m); why != nil {
		if err := e.backFromMerge(ctx, s.job, s.t, why); err != nil {
			return Confirmation{}, err
		}
		return Confirmation{}, refuse(CodeLibraryChanged, "The film's file in Radarr changed since the match, so the comparison and the clips no longer hold: the job goes back to the match. Match the film again.")
	}
	arrLangs, err := e.p.radarr.Languages(ctx)
	if err != nil {
		return Confirmation{}, appFailure(AppRadarr, err)
	}
	choice := mergeChoice{
		UploadFileID: f.ID, Upload: f.Path, AudioTracks: audio, SubtitleTracks: subs, OffsetMs: offset,
		LanguageTag: tag, TrackName: name, KeepOriginalDefault: keep, ArrLanguage: arrLanguageFor(tag, arrLangs),
		Preview: pv.Request, LibraryFileID: rec.Library.FileID, LibrarySize: rec.Library.Size,
	}
	arrLang := ""
	if choice.ArrLanguage != nil {
		arrLang = choice.ArrLanguage.Name
	}
	audit := []store.AuditEntry{{Actor: user, Email: email, Action: "merge.confirmed", Detail: map[string]string{
		"app": s.t.App, "titleId": strconv.Itoa(s.t.TitleID), "title": s.t.Title,
		"file": path.Base(f.Path), "libraryFileId": strconv.Itoa(rec.Library.FileID), "finding": cmp.Finding,
		"audioTracks": joinInts(audio), "subtitleTracks": joinInts(subs), "offsetMs": strconv.FormatInt(offset, 10),
		"languageTag": tag, "trackName": name, "keepOriginalDefault": strconv.FormatBool(keep),
		"arrLanguage": arrLang, "preview": pv.Request,
	}}}
	note := fmt.Sprintf("merge confirmed for %s: %d audio and %d subtitle track(s) of %s, offset %+d ms, %s",
		s.t.App, len(audio), len(subs), path.Base(f.Path), offset, tag)

	// The write, under the job's lock: the target as it is now, so that a preview requested
	// since the checks is neither written over nor taken for the one the user watched.
	defer e.locks.Lock(s.job.ID)()
	// The job must still wait for its confirmation: another confirmation of it (a second
	// tab, a double click) may have moved it on since the session was read, and its import
	// record must not be touched then. Every confirmation moves the job under this lock.
	if j, err := e.st.Job(ctx, s.job.ID); err != nil {
		return Confirmation{}, err
	} else if j.State != store.AwaitingConfirmation {
		return Confirmation{}, wrongState()
	}
	cur, err := e.st.Target(ctx, s.job.ID)
	if errors.Is(err, store.ErrNotFound) {
		return Confirmation{}, wrongState()
	}
	if err != nil {
		return Confirmation{}, err
	}
	if cur.App != s.t.App || cur.TitleID != s.t.TitleID || !cur.MatchedAt.Equal(s.t.MatchedAt) {
		return Confirmation{}, wrongState()
	}
	if now, ok := PreviewOf(cur); !ok || now.Request != pv.Request || now.State != PreviewReady {
		return Confirmation{}, refuse(CodePreviewRequired, "The preview changed meanwhile: watch the new clips, then confirm the merge.")
	}
	opts, err := withOption(cur.Options, "merge", choice)
	if err != nil {
		return Confirmation{}, err
	}
	var sc any // nil: the key goes
	if sidecars != nil {
		sc = sidecars
	}
	if opts, err = withOption(opts, "subtitles", sc); err != nil {
		return Confirmation{}, err
	}
	// An import record of an earlier confirmation of this job (one that went back to the
	// match since) is not this merge's: it starts as not submitted.
	if imp, err := e.st.Import(ctx, s.job.ID); err == nil && !imp.SubmittedAt.IsZero() {
		if err := e.st.PutImport(ctx, store.Import{JobID: s.job.ID}); err != nil {
			return Confirmation{}, err
		}
	} else if err != nil && !errors.Is(err, store.ErrNotFound) {
		return Confirmation{}, err
	}
	next, err := e.st.ConfirmImport(ctx, s.job.ID, cur, nil, opts, audit, store.Change{Actor: user, Note: note})
	if errors.Is(err, store.ErrConflict) || errors.Is(err, store.ErrInvalidTransition) || errors.Is(err, store.ErrNotFound) {
		return Confirmation{}, wrongState()
	}
	if err != nil {
		return Confirmation{}, err
	}
	e.log.Info("job_state", "job", s.job.ID, "from", store.AwaitingConfirmation, "to", next.State, "attempt", next.Attempt, "merge", path.Base(f.Path))
	// The sync check's clips stop: the merge reads the same library file. The importing step
	// cancels them again and takes in what they leave.
	if q := e.p.queue; q != nil {
		for slot := 1; slot <= runner.PreviewSlots; slot++ {
			if err := q.Cancel(ctx, runner.PreviewID(s.job.ID, slot)); err != nil {
				e.log.Warn("preview_cancel_failed", "job", s.job.ID, "slot", slot, "error", err)
				// The other slots are still cancelled; the importing step cancels them again.
			}
		}
	}
	t := cur
	t.Options = opts
	return Confirmation{Job: next, Target: t, Subtitles: req.Subtitles}, nil
}

// checkTracks checks the merge's tracks against the upload's probe: at least one audio
// track, every index an audio (or subtitle) track of the upload, each once.
func checkTracks(up media.Probe, audio, subs []int) error {
	if len(audio) == 0 {
		return badRequest("A merge adds at least one audio track (audioTracks).")
	}
	seen := map[int]bool{}
	for _, i := range audio {
		if seen[i] || !slices.ContainsFunc(up.Audio, func(a media.AudioTrack) bool { return a.Index == i }) {
			return badRequest("audioTracks: %d is not an audio track of the upload, or is listed twice (flow2.upload.audio lists them by index).", i)
		}
		seen[i] = true
	}
	for _, i := range subs {
		if seen[i] || !slices.ContainsFunc(up.Subtitles, func(s media.SubtitleTrack) bool { return s.Index == i }) {
			return badRequest("subtitleTracks: %d is not a subtitle track of the upload, or is listed twice (flow2.upload.subtitles lists them by index).", i)
		}
		seen[i] = true
	}
	return nil
}

func joinInts(v []int) string {
	parts := make([]string, 0, len(v))
	for _, n := range v {
		parts = append(parts, strconv.Itoa(n))
	}
	return strings.Join(parts, ",")
}

// Radarr's names of languages that its list names otherwise than English does: a regional
// variant it lists of its own, then a base language under another name.
var (
	arrRegional = map[string]string{"es-419": "Spanish (Latino)", "pt-BR": "Portuguese (Brazil)", "nl-BE": "Flemish"}
	arrBase     = map[string]string{"nb": "Norwegian", "nn": "Norwegian", "no": "Norwegian", "fil": "Tagalog", "tl": "Tagalog"}
	// latinAmerica is UN M.49 region 419; Spanish of any country in it is Spanish (Latino).
	latinAmerica = language.MustParseRegion("419")
)

// arrLanguageFor is the app's language (from its GET /language) for a BCP 47 tag: its own
// name for a regional variant it lists (es-419 and the Spanish of every country in region
// 419, es-MX or es-AR: Spanish (Latino); pt-BR: Portuguese (Brazil); nl-BE: Flemish), else
// the base language's English name (es, es-ES, es-US: Spanish; pt-PT: Portuguese). nil
// when the app lists none of them (the result then warns, WarnLanguageUnknown).
func arrLanguageFor(tag string, langs []arr.Language) *Ref {
	t, err := language.Parse(tag)
	if err != nil {
		return nil
	}
	base, conf := t.Base()
	if conf != language.Exact { // "und", or a tag that names no language
		return nil
	}
	var names []string
	if n, ok := arrRegional[t.String()]; ok {
		names = append(names, n)
	} else if r, rc := t.Region(); base.String() == "es" && rc == language.Exact && latinAmerica.Contains(r) {
		// es-MX, es-AR, es-CO…: Latin American Spanish is Radarr's Spanish (Latino), as es-419.
		names = append(names, "Spanish (Latino)")
	}
	if n, ok := arrBase[base.String()]; ok {
		names = append(names, n)
	}
	if n := display.English.Languages().Name(base); n != "" {
		names = append(names, n)
	}
	for _, n := range names {
		for _, l := range langs {
			if l.ID > 0 && strings.EqualFold(l.Name, n) {
				return &Ref{ID: l.ID, Name: l.Name}
			}
		}
	}
	return nil
}

// mergedLanguages is the merged file's languages for the app: the existing file's, then the
// added one when it is not among them. With an added language, "Unknown" (and the app's
// other pseudo-languages, ids ≤ 0) are dropped.
func mergedLanguages(existing []Ref, added *Ref) []arr.Language {
	out := []arr.Language{}
	for _, l := range existing {
		if added != nil && l.ID <= 0 {
			continue
		}
		out = append(out, arr.Language{ID: l.ID, Name: l.Name})
	}
	if added != nil && !slices.ContainsFunc(out, func(l arr.Language) bool { return l.ID == added.ID }) {
		out = append(out, arr.Language{ID: added.ID, Name: added.Name})
	}
	return out
}

// ---- the importing step -------------------------------------------------------------------

// mergeStep is the part of a merge job's importing step that comes before the import (see
// the top of this file). ready reports that the merged file's row is stored, so the import
// engine goes on with it.
func (e *Engine) mergeStep(ctx context.Context, job store.Job, t store.Target, c mergeChoice) (ready bool, err error) {
	rec, ok := Flow2Of(t)
	if !ok {
		return false, e.failMerge(ctx, job, t, CodeMergeFailed, "", "the job is no longer flow 2")
	}
	q := e.p.queue
	if q == nil {
		return false, errors.New("merge: the engine has no spool queue")
	}
	// The merges of earlier attempts stop; a result one leaves is acknowledged and its
	// directory removed (the task has ended, so nothing writes there after that). The
	// attempt whose merged file is in use is kept.
	keep := 0
	if c.Output != nil {
		keep = c.Attempt
	}
	for a := 1; a < job.Attempt; a++ {
		if a == keep {
			continue
		}
		saw, err := discardTask(ctx, q, runner.MergeID(job.ID, a))
		if err != nil {
			return false, err
		}
		if saw {
			if err := e.removeMerged(job.ID, runner.MergeDest(a)); err != nil {
				return false, err
			}
		}
	}
	if c.Output == nil {
		return false, e.runMerge(ctx, job, t, rec, c)
	}
	rows, err := e.st.Rows(ctx, job.ID)
	if err != nil {
		return false, err
	}
	if slices.ContainsFunc(rows, func(r store.Row) bool { return r.FileID == c.Output.FileID && len(r.Proposal) > 0 }) {
		return true, nil
	}
	// The task's result, when a crash came between registering the file and acknowledging it.
	if _, err := discardTask(ctx, q, runner.MergeID(job.ID, c.Attempt)); err != nil {
		return false, err
	}
	return e.proposeMerged(ctx, job, t, rec, c)
}

// runMerge is the merge task's part of the step: submit it, or take in its result.
func (e *Engine) runMerge(ctx context.Context, job store.Job, t store.Target, rec Flow2, c mergeChoice) error {
	q := e.p.queue
	// The sync check's clips stop, and what they leave goes.
	for slot := 1; slot <= runner.PreviewSlots; slot++ {
		if _, err := discardTask(ctx, q, runner.PreviewID(job.ID, slot)); err != nil {
			return err
		}
	}
	id := runner.MergeID(job.ID, job.Attempt)
	if c.Attempt != job.Attempt {
		return e.startMerge(ctx, job, t, rec, c, id)
	}
	res, ok, err := q.Result(ctx, id)
	if err != nil {
		return err
	}
	if ok {
		return e.useMerge(ctx, job, t, rec, c, res)
	}
	return e.submitMerge(ctx, job, t, rec, c, id)
}

// startMerge makes the checks before an attempt's merge, records the attempt and submits
// its task.
func (e *Engine) startMerge(ctx context.Context, job store.Job, t store.Target, rec Flow2, c mergeChoice, id string) error {
	cfg := e.p.cfg
	if _, _, in := media.LibraryPath(cfg.Paths.LibraryRoots, rec.Library.Path); !in {
		return e.failMerge(ctx, job, t, CodeLibraryUnreadable, "", fmt.Sprintf("the library file %q (%s: %q) is not under paths.libraryRoots %q",
			rec.Library.Path, t.App, rec.Library.AppPath, cfg.Paths.LibraryRoots))
	}
	files, err := e.st.Files(ctx, job.ID)
	if err != nil {
		return err
	}
	if i := slices.IndexFunc(files, func(f store.File) bool { return f.ID == c.UploadFileID }); i < 0 ||
		files[i].Path != c.Upload || files[i].Status != store.FileOK || !strings.HasPrefix(files[i].Path, jobdir.Files+"/") {
		return e.failMerge(ctx, job, t, CodeMergeFailed, "", "the upload to merge is no longer one of the job's files")
	}
	if err := e.checkStorage(); err != nil {
		return e.failMerge(ctx, job, t, CodeMergeFailed, "", "refused before the merge: "+err.Error())
	}
	if e.p.radarr == nil {
		return e.p.move(ctx, job, &t, store.Stalled, store.Change{ErrorCode: CodeArrUnavailable, ErrorText: "radarr is not configured"})
	}
	m, err := e.p.radarr.Movie(ctx, t.TitleID)
	if err != nil {
		return e.mergeAppError(ctx, job, t, "film", err)
	}
	if why := trackedChanged(rec.Library, m); why != nil {
		return e.backFromMerge(ctx, job, t, why)
	}
	// What the id holds now belongs to no merge of this choice (an earlier confirmation of
	// the attempt, which went back to the match): it goes. So does any merged file in the
	// job, with its row: before this attempt's merge none is in use.
	if _, err := discardTask(ctx, e.p.queue, id); err != nil {
		return err
	}
	if err := e.dropMerged(ctx, job.ID); err != nil {
		return err
	}
	c.Attempt, c.Refusal = job.Attempt, ""
	saved, err := e.saveMerge(ctx, job, t, c)
	if err != nil || !saved {
		return err
	}
	e.log.Info("merge_started", "job", job.ID, "task", id, "attempt", job.Attempt)
	return e.submitMerge(ctx, job, t, rec, c, id)
}

// submitMerge submits the attempt's merge task (nothing happens when it is waiting, running
// or has a result) and stalls the job while the worker is down.
func (e *Engine) submitMerge(ctx context.Context, job store.Job, t store.Target, rec Flow2, c mergeChoice, id string) error {
	keep := c.KeepOriginalDefault
	args, err := json.Marshal(merge.Args{
		Library: rec.Library.Path, Upload: c.Upload, Tracks: append(slices.Clone(c.AudioTracks), c.SubtitleTracks...),
		OffsetMs: c.OffsetMs, LanguageTag: c.LanguageTag, TrackName: c.TrackName, KeepOriginalDefault: &keep,
		Dest: runner.MergeDest(c.Attempt),
	})
	if err != nil {
		return err
	}
	if err := e.p.queue.Submit(ctx, spool.Task{ID: id, Kind: spool.KindMerge, Job: job.ID, Args: args}); err != nil {
		return err
	}
	if why, down := e.p.workerDown(); down {
		return e.p.move(ctx, job, &t, store.Stalled, store.Change{ErrorCode: runner.CodeWorkerUnavailable, ErrorText: why})
	}
	return nil
}

// useMerge takes in the result of the attempt's merge task (see the top of this file).
func (e *Engine) useMerge(ctx context.Context, job store.Job, t store.Target, rec Flow2, c mergeChoice, res spool.Result) error {
	q := e.p.queue
	var out merge.Output
	// A result that is not used leaves nothing behind: the task has ended, so what it wrote
	// into its destination (a full copy of the film, verified or not) can go. Removed
	// before the result is acknowledged, so that after a crash in between the next pass
	// (or the next attempt's, which takes in an earlier attempt's result) removes it again.
	drop := func() error {
		if err := e.removeMerged(job.ID, runner.MergeDest(c.Attempt)); err != nil {
			return err
		}
		if d := path.Dir(out.Path); out.Path != "" && d != runner.MergeDest(c.Attempt) && merge.ValidDest(d) {
			return e.removeMerged(job.ID, d)
		}
		return nil
	}
	fail := func(code, refusal, text string) error {
		if err := e.failMerge(ctx, job, t, code, refusal, text); err != nil {
			return err
		}
		if err := drop(); err != nil {
			return err
		}
		return q.Ack(ctx, res.ID)
	}
	again := func(why string, clean bool) error {
		e.log.Info("task_discarded", "job", job.ID, "kind", res.Kind, "task", res.ID, "note", why)
		if clean {
			if err := drop(); err != nil {
				return err
			}
		}
		return q.Ack(ctx, res.ID) // the next pass submits it again
	}
	back := func(why error) error {
		if err := e.backFromMerge(ctx, job, t, why); err != nil {
			return err
		}
		if err := drop(); err != nil {
			return err
		}
		return q.Ack(ctx, res.ID)
	}
	if res.Status != spool.StatusOK {
		code, text := runner.CodeTaskFailed, "no error given"
		if res.Error != nil {
			if res.Error.Code == spool.CodeCancelled {
				return again("a cancelled merge: submitted again", true)
			}
			if res.Error.Code == spool.CodeTimeout {
				code = runner.CodeTaskTimeout
			}
			text = res.Error.Code + ": " + res.Error.Message
		}
		return fail(code, "", res.Kind+" task "+res.ID+": "+text)
	}
	if err := json.Unmarshal(res.Output, &out); err != nil {
		return fail(runner.CodeTaskFailed, "", res.Kind+" task "+res.ID+": unreadable output: "+err.Error())
	}
	if !out.OK {
		switch out.Refusal {
		case merge.RefusalBusy:
			// The destination belongs to the merge that holds its lock: it is left alone.
			return again("another merge into the destination was running: submitted again", false)
		case merge.RefusalLibraryChanged:
			return back(fmt.Errorf("the library file %q changed while it was merged (%s): rescan the film in Radarr, then match it again", rec.Library.Path, out.Detail))
		}
		refusal := out.Refusal
		if refusal == "" {
			refusal = merge.RefusalMuxFailed
		}
		return fail(CodeMergeFailed, refusal, refusal+": "+out.Detail)
	}
	want := runner.MergeDest(c.Attempt) + "/" + merge.OutputName(rec.Library.Path)
	switch {
	case out.Path != want || out.Size <= 0:
		return fail(runner.CodeTaskFailed, "", fmt.Sprintf("%s task %s: the merged file is %q (%d bytes), not %q", res.Kind, res.ID, out.Path, out.Size, want))
	case out.LanguageTag != c.LanguageTag || out.TrackName != c.TrackName || out.KeepOriginalDefault != c.KeepOriginalDefault:
		return fail(runner.CodeTaskFailed, "", fmt.Sprintf("%s task %s: the added tracks got %q, %q, original default %t, not what was confirmed (%q, %q, %t): do the worker's merge settings differ?",
			res.Kind, res.ID, out.LanguageTag, out.TrackName, out.KeepOriginalDefault, c.LanguageTag, c.TrackName, c.KeepOriginalDefault))
	case out.Library == nil || out.Library.Path != rec.Library.Path:
		return fail(runner.CodeTaskFailed, "", fmt.Sprintf("%s task %s: the output names another library file", res.Kind, res.ID))
	case rec.Library.Size > 0 && out.Library.Size != rec.Library.Size:
		return back(fmt.Errorf("the library file %q changed since the match (tracked size %d, merged from %d bytes): rescan the film in Radarr, then match it again",
			rec.Library.Path, rec.Library.Size, out.Library.Size))
	}
	full := filepath.Join(jobdir.Dir(e.p.cfg.Paths.IntakeRoot, job.ID), filepath.FromSlash(out.Path))
	if info, err := os.Lstat(full); err != nil || !info.Mode().IsRegular() || info.Size() != out.Size {
		return fail(CodeMergeFailed, "", fmt.Sprintf("the merged file %s is not in the job directory at %d bytes", out.Path, out.Size))
	}
	stored, err := e.storeMerged(ctx, job, t, c, out)
	if err != nil || !stored {
		return err // not stored: the job changed meanwhile, and the next pass sees how
	}
	e.log.Info("merge_finished", "job", job.ID, "task", res.ID, "file", out.Path, "size", out.Size)
	return q.Ack(ctx, res.ID)
}

// storeMerged registers the verified merged file as a file of the job (a video under
// merged/; a file of that path from an earlier confirmation is updated) and in the choice,
// under the job's lock, while the job is still importing for the same title and attempt.
func (e *Engine) storeMerged(ctx context.Context, job store.Job, t store.Target, c mergeChoice, out merge.Output) (bool, error) {
	report, err := json.Marshal(out.Media)
	if err != nil {
		return false, err
	}
	defer e.locks.Lock(job.ID)()
	cur, ok, err := e.current(ctx, job, t)
	if err != nil || !ok {
		return false, err
	}
	if now, ok := mergeOf(cur); !ok || now.Attempt != c.Attempt || now.Output != nil {
		return false, nil
	}
	files, err := e.st.Files(ctx, job.ID)
	if err != nil {
		return false, err
	}
	f := store.File{JobID: job.ID, Path: out.Path, Kind: store.KindVideo, Size: out.Size, Status: store.FileOK, Report: report}
	if i := slices.IndexFunc(files, func(x store.File) bool { return x.Path == out.Path }); i >= 0 {
		f.ID = files[i].ID
		if err := e.st.UpdateFile(ctx, f); err != nil {
			return false, err
		}
	} else if f, err = e.st.AddFile(ctx, f); err != nil {
		return false, err
	}
	c.Output = &mergedFile{FileID: f.ID, Path: out.Path, Size: out.Size}
	if cur.Options, err = withOption(cur.Options, "merge", c); err != nil {
		return false, err
	}
	return true, e.st.SetTarget(ctx, job.ID, cur)
}

// proposeMerged asks Radarr for the merged file's row and stores it (see the top of this
// file). ready reports that it is stored.
func (e *Engine) proposeMerged(ctx context.Context, job store.Job, t store.Target, rec Flow2, c mergeChoice) (ready bool, err error) {
	full := filepath.Join(jobdir.Dir(e.p.cfg.Paths.IntakeRoot, job.ID), filepath.FromSlash(c.Output.Path))
	if info, err := os.Lstat(full); err != nil || !info.Mode().IsRegular() || info.Size() != c.Output.Size {
		return false, e.failMerge(ctx, job, t, CodeMergeFailed, "", fmt.Sprintf("the merged file %s is gone, or not at %d bytes", c.Output.Path, c.Output.Size))
	}
	if e.p.radarr == nil {
		return false, e.p.move(ctx, job, &t, store.Stalled, store.Change{ErrorCode: CodeArrUnavailable, ErrorText: "radarr is not configured"})
	}
	file := e.p.appPath(t.App, job.ID, c.Output.Path)
	items, err := e.p.radarr.Proposal(ctx, path.Dir(file))
	if err != nil {
		return false, e.mergeAppError(ctx, job, t, "proposal", err)
	}
	i := slices.IndexFunc(items, func(it arr.Row) bool { return path.Clean(it.Path) == path.Clean(file) })
	if i < 0 {
		return false, e.failMerge(ctx, job, t, CodeMergeRejected, "", "radarr did not offer the merged file in the proposal of its folder")
	}
	it := items[i]
	it.Path = file // what Fukku derives from the file, never what an app said
	it.MovieID, it.SeriesID, it.SeasonNumber, it.EpisodeIDs = t.TitleID, 0, 0, nil
	it.Quality = arr.Quality{ID: rec.Library.Quality.ID, Name: rec.Library.Quality.Name}
	it.Languages = mergedLanguages(rec.Library.Languages, c.ArrLanguage)
	fresh, err := e.p.radarr.Recheck(ctx, []arr.Row{it})
	if err != nil {
		return false, e.mergeAppError(ctx, job, t, "re-check", err)
	}
	got, err := mergeAnswers([]arr.Row{it}, fresh)
	if err != nil {
		return false, e.failMerge(ctx, job, t, CodeMergeRejected, "", "radarr (re-check): "+err.Error())
	}
	row := got[0]
	// What the import sends: the existing file's quality and both languages.
	row.Quality, row.Languages = it.Quality, it.Languages
	if err := titleOK(t, row); err != nil {
		return false, e.failMerge(ctx, job, t, CodeMergeRejected, "", "radarr (re-check): "+err.Error())
	}
	var refused []string
	for _, r := range row.Rejections {
		if !replaceCovers(r.Reason) {
			refused = append(refused, r.Reason)
		}
	}
	if len(refused) > 0 {
		return false, e.failMerge(ctx, job, t, CodeMergeRejected, "", "radarr refuses the merged file: "+strings.Join(refused, "; "))
	}
	data, err := json.Marshal(row)
	if err != nil {
		return false, err
	}
	defer e.locks.Lock(job.ID)()
	cur, ok, err := e.current(ctx, job, t)
	if err != nil || !ok {
		return false, err
	}
	if now, ok := mergeOf(cur); !ok || now.Output == nil || now.Output.FileID != c.Output.FileID {
		return false, nil
	}
	r := store.Row{ID: RowID(c.Output.FileID), FileID: c.Output.FileID, Edit: json.RawMessage(`{}`), Proposal: data, Override: len(row.Rejections) > 0}
	if err := e.st.PutRows(ctx, job.ID, []store.Row{r}); err != nil {
		return false, err
	}
	e.log.Info("merge_proposed", "job", job.ID, "row", r.ID, "quality", row.Quality.Name, "languages", len(row.Languages), "rejections", len(row.Rejections))
	return true, nil
}

// current reads the job's target under its lock (the caller holds it); ok is false when the
// job is no longer in job's state or has another target than t.
func (e *Engine) current(ctx context.Context, job store.Job, t store.Target) (store.Target, bool, error) {
	j, err := e.st.Job(ctx, job.ID)
	if err != nil {
		return store.Target{}, false, err
	}
	cur, err := e.st.Target(ctx, job.ID)
	if errors.Is(err, store.ErrNotFound) {
		return store.Target{}, false, nil
	}
	if err != nil {
		return store.Target{}, false, err
	}
	same := j.State == job.State && cur.App == t.App && cur.TitleID == t.TitleID && cur.MatchedAt.Equal(t.MatchedAt)
	return cur, same, nil
}

// saveMerge stores the choice c into the target's options, under the job's lock, while the
// job is still in its state for the same title.
func (e *Engine) saveMerge(ctx context.Context, job store.Job, t store.Target, c mergeChoice) (bool, error) {
	defer e.locks.Lock(job.ID)()
	cur, ok, err := e.current(ctx, job, t)
	if err != nil || !ok {
		return false, err
	}
	if _, ok := mergeOf(cur); !ok {
		return false, nil
	}
	if cur.Options, err = withOption(cur.Options, "merge", c); err != nil {
		return false, err
	}
	return true, e.st.SetTarget(ctx, job.ID, cur)
}

// failMerge fails the job before anything was imported; refusal (the merge task's) is
// recorded in the choice first, for the merge view.
func (e *Engine) failMerge(ctx context.Context, job store.Job, t store.Target, code, refusal, text string) error {
	if refusal != "" {
		if c, ok := mergeOf(t); ok {
			c.Refusal = refusal
			if _, err := e.saveMerge(ctx, job, t, c); err != nil {
				return err
			}
		}
	}
	e.forget(job.ID)
	e.log.Warn("merge_failed", "job", job.ID, "code", code, "refusal", refusal, "detail", text)
	return e.p.move(ctx, job, &t, store.Failed, store.Change{ErrorCode: code, ErrorText: t.App + ": " + text})
}

// mergeAppError ends a merge pass whose call to the app failed, as the proposal's do
// (appFailed): stalled while the app does not answer (failed after server.arrStallLimit
// stalls in a row), failed with merge_failed when it refused.
func (e *Engine) mergeAppError(ctx context.Context, job store.Job, t store.Target, call string, err error) error {
	if ctx.Err() != nil {
		return ctx.Err()
	}
	text := fmt.Sprintf("%s (%s): %v", t.App, call, err)
	if !Unavailable(err) {
		return e.failMerge(ctx, job, t, CodeMergeFailed, "", fmt.Sprintf("(%s): %v", call, err))
	}
	n, herr := e.p.stalls(ctx, job.ID, job.State, t.MatchedAt)
	if herr != nil {
		return herr
	}
	if n >= e.p.cfg.Server.ArrStallLimit {
		e.forget(job.ID)
		return e.p.move(ctx, job, &t, store.Failed, store.Change{ErrorCode: CodeArrUnavailable, ErrorText: fmt.Sprintf("%s; stalled %d times in a row", text, n)})
	}
	return e.p.move(ctx, job, &t, store.Stalled, store.Change{ErrorCode: CodeArrUnavailable, ErrorText: text})
}

// discardTask cancels a task and acknowledges the result it left; saw reports that there
// was one (the task has ended).
func discardTask(ctx context.Context, q spool.Queue, id string) (saw bool, err error) {
	if err := q.Cancel(ctx, id); err != nil {
		return false, err
	}
	_, ok, err := q.Result(ctx, id)
	if err != nil || !ok {
		return false, err
	}
	return true, q.Ack(ctx, id)
}

// removeMerged removes rel (job-relative: merged/<attempt>, or all of merged/) from the job's
// directory, through a root of it, so nothing outside the job is reached. Each merged file
// is a full copy of the film. A job without a directory has nothing to remove.
func (e *Engine) removeMerged(jobID, rel string) error {
	root, err := os.OpenRoot(jobdir.Dir(e.p.cfg.Paths.IntakeRoot, jobID))
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	defer func() { _ = root.Close() }()
	if err := root.RemoveAll(rel); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return fmt.Errorf("merge: remove %s: %w", rel, err)
	}
	return nil
}

// dropMerged removes every merged file of the job, on disk (merged/) and as job files: when
// no merge of the job is in use (before an attempt's merge, after going back to the match).
func (e *Engine) dropMerged(ctx context.Context, jobID string) error {
	if err := e.removeMerged(jobID, jobdir.Merged); err != nil {
		return err
	}
	files, err := e.st.Files(ctx, jobID)
	if err != nil {
		return err
	}
	for _, f := range files {
		if strings.HasPrefix(f.Path, jobdir.Merged+"/") {
			if err := e.st.DeleteFile(ctx, f.ID); err != nil && !errors.Is(err, store.ErrNotFound) {
				return err
			}
		}
	}
	return nil
}

// backFromMerge sends a merge job whose film's file changed back to probed (rescan) and,
// once it has gone, removes its merged files: nothing of them is imported any more. A crash
// in between leaves them to the next attempt's merge (startMerge), or to the removal of the
// job's directory.
func (e *Engine) backFromMerge(ctx context.Context, job store.Job, t store.Target, why error) error {
	moved, err := e.p.backToProbed(ctx, job, t, why)
	if err != nil || !moved {
		return err
	}
	return e.dropMerged(ctx, job.ID)
}

// mergeStillTracked checks, before a merge job's import is submitted, that Radarr's tracked
// file is still the match's. stop reports that the pass ends here: the job went back to
// probed (or the app failed, and the job moved as appError decides).
func (e *Engine) mergeStillTracked(ctx context.Context, job store.Job, w work) (stop bool, err error) {
	rec, ok := Flow2Of(w.t)
	if !ok {
		return true, e.fail(ctx, job, w.t, CodeImportFailed, "refused before the import: the job is no longer flow 2")
	}
	m, err := e.p.radarr.Movie(ctx, w.t.TitleID)
	if err != nil {
		return true, e.appError(ctx, job, w, "film", err, false)
	}
	if why := trackedChanged(rec.Library, m); why != nil {
		e.forget(job.ID)
		return true, e.backFromMerge(ctx, job, w.t, why)
	}
	return false, nil
}

// ---- after the import ---------------------------------------------------------------------

// mergeImported is the imported step of a merge job: with merge.unmonitorAfterMerge the
// film is unmonitored (B6), else its monitoring is left as it is; what came of it is
// recorded for the job's result.
func (e *Engine) mergeImported(ctx context.Context, job store.Job, t store.Target) error {
	if e.p.radarr == nil {
		return e.p.move(ctx, job, &t, store.Stalled, store.Change{ErrorCode: CodeArrUnavailable, ErrorText: "radarr is not configured"})
	}
	var mon monitoring
	note := ""
	if e.p.cfg.Merge.UnmonitorAfterMerge {
		if err := e.p.radarr.SetMonitored(ctx, t.TitleID, false); err != nil {
			return e.monitorFailed(ctx, job, t, err)
		}
		mon = monitoring{Monitored: false, Reason: UnmonitorMerged}
		note = "unmonitored after the merge (merge.unmonitorAfterMerge), so that an upgrade without the dub does not replace it; the film's own search still can"
	} else {
		m, err := e.p.radarr.Movie(ctx, t.TitleID)
		if err != nil {
			return e.monitorFailed(ctx, job, t, err)
		}
		mon.Monitored = m.Monitored
		note = fmt.Sprintf("monitoring unchanged after the merge (merge.unmonitorAfterMerge is off): monitored %t", m.Monitored)
	}
	if err := e.recordMonitoring(ctx, job, t, mon); err != nil {
		return err
	}
	return e.p.move(ctx, job, &t, store.PostProcessing, store.Change{Note: note})
}

// mergeDone removes a done merge job's merged/ (with importMode copy Radarr leaves the merged
// file there, a full copy of the film; with move only what an earlier attempt could have
// left). The upload stays until the job is removed. A failure is logged: the job is done.
func (e *Engine) mergeDone(ctx context.Context, job store.Job) {
	if j, err := e.st.Job(ctx, job.ID); err != nil || j.State != store.Done {
		return // it did not move (another pass, a cancel): nothing is removed
	}
	if err := e.removeMerged(job.ID, jobdir.Merged); err != nil {
		e.log.Warn("merge_cleanup_failed", "job", job.ID, "error", err)
	}
}

// ---- the views ----------------------------------------------------------------------------

// Merge states (MergeView.State).
const (
	MergePending = "pending" // confirmed; the merge task is not submitted yet
	MergeRunning = "running" // the worker merges
	MergeDone    = "merged"  // the merged file is verified and in the job
	MergeFailed  = "failed"  // the job failed before the merge was made (Refusal: the task's refusal, if it refused)
)

// MergeView is ImportView.Merge: what the merge adds and how far it is. Name and Size are
// the merged file's, once it is in.
type MergeView struct {
	State               string `json:"state"`
	Refusal             string `json:"refusal"`
	AudioTracks         []int  `json:"audioTracks"`
	SubtitleTracks      []int  `json:"subtitleTracks"`
	OffsetMs            int64  `json:"offsetMs"`
	LanguageTag         string `json:"languageTag"`
	TrackName           string `json:"trackName"`
	KeepOriginalDefault bool   `json:"keepOriginalDefault"`
	ArrLanguage         *Ref   `json:"arrLanguage"`
	Name                string `json:"name"`
	Size                int64  `json:"size"`
}

// MergeResult is ResultView.Merge, once the merged file is imported: its name, and what can
// still replace it in Radarr (B6), as codes with English text.
type MergeResult struct {
	Name     string         `json:"name"`
	Warnings []MergeWarning `json:"warnings"`
}

// MergeWarning is a warning of a merge's result: a code (WarnSearchCanReplace,
// WarnUpgradeCanReplace) for the UI's own text, and the English text.
type MergeWarning struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

// viewMerge is ImportView.Merge for a merge job; nil for another.
func viewMerge(job store.Job, t store.Target) *MergeView {
	c, ok := mergeOf(t)
	if !ok {
		return nil
	}
	v := &MergeView{
		State: MergePending, Refusal: c.Refusal, AudioTracks: c.AudioTracks, SubtitleTracks: c.SubtitleTracks,
		OffsetMs: c.OffsetMs, LanguageTag: c.LanguageTag, TrackName: c.TrackName,
		KeepOriginalDefault: c.KeepOriginalDefault, ArrLanguage: c.ArrLanguage,
	}
	if v.AudioTracks == nil {
		v.AudioTracks = []int{}
	}
	if v.SubtitleTracks == nil {
		v.SubtitleTracks = []int{}
	}
	switch {
	case c.Output != nil:
		v.State, v.Name, v.Size = MergeDone, path.Base(c.Output.Path), c.Output.Size
	case job.State == store.Failed:
		v.State = MergeFailed
	case c.Attempt == job.Attempt:
		v.State = MergeRunning
	}
	return v
}

// mergeResult is ResultView.Merge for a merge job whose file is imported; nil otherwise.
func mergeResult(t store.Target) *MergeResult {
	c, ok := mergeOf(t)
	if !ok || c.Output == nil {
		return nil
	}
	r := &MergeResult{Name: path.Base(c.Output.Path), Warnings: []MergeWarning{}}
	if mon, ok := monitoringOf(t); ok {
		code := WarnSearchCanReplace
		if mon.Monitored {
			code = WarnUpgradeCanReplace
		}
		r.Warnings = append(r.Warnings, MergeWarning{Code: code, Message: mergeWarningText[code]})
	}
	if c.ArrLanguage == nil {
		r.Warnings = append(r.Warnings, MergeWarning{Code: WarnLanguageUnknown, Message: mergeWarningText[WarnLanguageUnknown]})
	}
	return r
}
