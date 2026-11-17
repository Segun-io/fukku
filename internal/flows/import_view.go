package flows

import (
	"context"
	"errors"
	"path"

	"github.com/segun-io/fukku/internal/store"
)

// The import and its result as JobDetail (GET /api/jobs/{id}) shows them (#40's contract,
// used by #44):
//
//	"import": {"status": "running", "rows": [{"id": "r1", "name": "Film.mkv", "result": "imported"}, {"id": "r2", "name": "x.mkv", "result": ""}]}
//	"result": {"titleLink": null, "monitored": false, "unmonitorReason": "dub_only",
//	           "postProcessing": [{"step": "subtitles", "status": "pending", "detail": ""}]}
//
// Both are null before the job is confirmed; result is null until the files are imported.
// result.monitored is null until the monitoring is set (the imported step); for a show it
// is true when every imported episode is monitored. unmonitorReason says why Fukku left it
// unmonitored: dub_only (only a dub was imported, merge.unmonitorAfterDubOnlyImport; the
// title's own search can still replace it), chosen (the user chose so at the match), merged
// (a merge replaced the film's file, merge.unmonitorAfterMerge, #55), or "" (as it was in
// the app, or monitored).
//
// A merge (#55) adds "import.merge" (MergeView) and "result.merge" (MergeResult):
//
//	"merge": {"state": "merged", "refusal": "", "audioTracks": [2], "subtitleTracks": [], "offsetMs": -800,
//	          "languageTag": "es-419", "trackName": "Español (Latino)", "keepOriginalDefault": true,
//	          "arrLanguage": {"id": 37, "name": "Spanish (Latino)"}, "name": "Film (2001) WEBDL-1080p.mkv", "size": 123456789}
//	"merge": {"name": "Film (2001) WEBDL-1080p.mkv",
//	          "warnings": [{"code": "search_can_replace", "message": "The film is unmonitored, …"}]}
//
// import.merge.state is pending, running (the worker merges), merged or failed (the job
// failed before the merge was made; refusal is the merge task's, e.g. verify_failed, or ""
// when it did not refuse). arrLanguage is null when Radarr has no language for the tag.
// result.merge.warnings are search_can_replace (the film is unmonitored; its own Search can
// still replace the file), upgrade_can_replace (merge.unmonitorAfterMerge is off), and
// language_unknown_to_radarr (Radarr lists the file with the existing languages only).

// Import statuses.
const (
	ImportPending = "pending" // confirmed, not submitted yet
	ImportRunning = "running" // submitted; the app is importing
	ImportDone    = "done"    // every row is imported
	ImportFailed  = "failed"  // the job failed while importing (import_failed, title_busy…)
)

type ImportView struct {
	Status string          `json:"status"`
	Rows   []ImportRowView `json:"rows"`
	// Merge is the merge of a flow 2a job (#55, merge.go): what it adds and how far it is.
	// Its row (the merged file) is in rows once Radarr has re-checked it. Null for every
	// other job.
	Merge *MergeView `json:"merge"`
}

// ImportRowView is a row's result: "" (not yet), imported, failed or skipped. Name is the
// file's name (not its path).
type ImportRowView struct {
	ID     string `json:"id"`
	Name   string `json:"name"`
	Result string `json:"result"`
}

type ResultView struct {
	TitleLink       *string `json:"titleLink"`
	Monitored       *bool   `json:"monitored"`
	UnmonitorReason string  `json:"unmonitorReason"`
	// SeasonDub is the policy the user (or seasonDub.policy) applied to episodes that already
	// had a file: replace (the season is unmonitored when only a dub was imported, so the UI
	// warns that the title's own Search can still replace it), or subtitlesOnly (nothing was
	// imported; point the user at the subtitle manager). Null otherwise.
	SeasonDub *SeasonDubResult `json:"seasonDub"`
	// Version is the second version the job placed (#56) and its grouping in the media
	// server; null for every other job.
	Version *VersionResult `json:"version"`
	// Merge is the merged file a flow 2a job imported (#55), with the warnings for the user
	// (what can still replace it in Radarr, B6). Null for every other job.
	Merge          *MergeResult `json:"merge"`
	PostProcessing []StepView   `json:"postProcessing"`
}

// StepView is a post-processing step: pending, ok, failed or skipped. The detail of a step
// that failed (or is waiting to try again) is the error, which may name a service's
// address, so only an admin sees it.
type StepView struct {
	Step   string `json:"step"`
	Status string `json:"status"`
	Detail string `json:"detail"`
}

// Detail returns the job's import and result views (nil while they do not apply).
func (e *Engine) Detail(ctx context.Context, job store.Job, admin bool) (*ImportView, *ResultView, error) {
	at := job.State
	if job.State == store.Failed || job.State == store.Stalled {
		at = job.Resume
	}
	var iv *ImportView
	switch at {
	case store.Importing, store.Imported, store.PostProcessing, store.Done:
	default:
		return nil, nil, nil
	}
	rows, err := e.st.Rows(ctx, job.ID)
	if err != nil {
		return nil, nil, err
	}
	files, err := e.st.Files(ctx, job.ID)
	if err != nil {
		return nil, nil, err
	}
	names := map[int64]string{}
	for _, f := range files {
		names[f.ID] = path.Base(f.Path)
	}
	t, err := e.st.Target(ctx, job.ID)
	if err != nil && !errors.Is(err, store.ErrNotFound) {
		return nil, nil, err
	}
	iv = &ImportView{Rows: make([]ImportRowView, 0, len(rows)), Merge: viewMerge(job, t)}
	for _, r := range rows {
		iv.Rows = append(iv.Rows, ImportRowView{ID: r.ID, Name: names[r.FileID], Result: r.Result})
	}
	switch {
	case at != store.Importing:
		iv.Status = ImportDone
	case job.State == store.Failed:
		iv.Status = ImportFailed
	default:
		iv.Status = ImportPending
		imp, err := e.st.Import(ctx, job.ID)
		if err != nil && !errors.Is(err, store.ErrNotFound) {
			return nil, nil, err
		}
		if !imp.SubmittedAt.IsZero() {
			iv.Status = ImportRunning
		}
	}
	if at == store.Importing {
		return iv, nil, nil
	}

	recorded, err := e.st.PostSteps(ctx, job.ID)
	if err != nil {
		return nil, nil, err
	}
	byName := map[string]store.PostStep{}
	for _, ps := range recorded {
		byName[ps.Name] = ps
	}
	rv := &ResultView{PostProcessing: []StepView{}, Merge: mergeResult(t)}
	if mon, ok := monitoringOf(t); ok {
		rv.Monitored, rv.UnmonitorReason = &mon.Monitored, mon.Reason
	}
	if c, ok := seasonDubOf(t); ok {
		rv.SeasonDub = &SeasonDubResult{Policy: c.Policy, Seasons: append([]int{}, c.Seasons...)}
	}
	view := func(ps store.PostStep) StepView {
		v := StepView{Step: ps.Name, Status: ps.Status}
		if v.Status == "" {
			v.Status = store.StepPending
		}
		if admin || v.Status == store.StepOK || v.Status == store.StepSkipped {
			v.Detail = ps.Detail
		}
		return v
	}
	if rv.Version, err = e.versionResult(ctx, job, t, admin); err != nil {
		return nil, nil, err
	}
	listed := map[string]bool{}
	for _, s := range e.stepsFor(t) {
		ps, ok := byName[s.Name()]
		if !ok {
			ps = store.PostStep{Name: s.Name(), Status: store.StepPending}
		}
		listed[s.Name()] = true
		rv.PostProcessing = append(rv.PostProcessing, view(ps))
		if l, ok := s.(Linker); ok && rv.TitleLink == nil && ps.Status == store.StepOK {
			if url, ok := l.Link(ps.Detail); ok {
				rv.TitleLink = &url
			}
		}
	}
	for _, ps := range recorded { // a step recorded by an earlier version that is no longer registered
		if !listed[ps.Name] {
			rv.PostProcessing = append(rv.PostProcessing, view(ps))
		}
	}
	return iv, rv, nil
}
