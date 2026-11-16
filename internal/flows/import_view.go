package flows

import (
	"context"
	"errors"

	"github.com/segun-io/fukku/internal/store"
)

// The import and its result as JobDetail (GET /api/jobs/{id}) shows them (#40's contract,
// used by #44):
//
//	"import": {"status": "running", "rows": [{"id": "r1", "result": "imported"}, {"id": "r2", "result": ""}]}
//	"result": {"titleLink": null, "postProcessing": [{"step": "subtitles", "status": "pending", "detail": ""}]}
//
// Both are null before the job is confirmed; result is null until the files are imported.

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
}

// ImportRowView is a row's result: "" (not yet), imported, failed or skipped.
type ImportRowView struct {
	ID     string `json:"id"`
	Result string `json:"result"`
}

type ResultView struct {
	TitleLink      *string    `json:"titleLink"`
	PostProcessing []StepView `json:"postProcessing"`
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
	iv = &ImportView{Rows: make([]ImportRowView, 0, len(rows))}
	for _, r := range rows {
		iv.Rows = append(iv.Rows, ImportRowView{ID: r.ID, Result: r.Result})
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
	rv := &ResultView{PostProcessing: []StepView{}}
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
	listed := map[string]bool{}
	for _, s := range e.steps {
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
