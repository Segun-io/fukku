package flows

import (
	"context"
	"encoding/json"
	"errors"
	"slices"

	"github.com/segun-io/fukku/internal/arr"
	"github.com/segun-io/fukku/internal/store"
)

// BulkEdit changes several rows of a show at once (POST …/proposal/bulk): shift their
// episode numbers by Shift, or move them to season SeasonNumber. Exactly one is set.
type BulkEdit struct {
	RowIDs       []string `json:"rowIds"`
	Shift        *int     `json:"shift,omitempty"`
	SeasonNumber *int     `json:"seasonNumber,omitempty"`
}

// edit opens a session for an edit: the job is awaiting confirmation and has a target.
func (p *Proposals) edit(ctx context.Context, jobID string) (*session, error) {
	job, err := p.st.Job(ctx, jobID)
	if err != nil {
		return nil, err
	}
	if job.State != store.AwaitingConfirmation {
		return nil, wrongState()
	}
	t, err := p.st.Target(ctx, jobID)
	if errors.Is(err, store.ErrNotFound) {
		return nil, wrongState()
	}
	if err != nil {
		return nil, err
	}
	return p.open(ctx, job, t, false)
}

func (s *session) find(rowID string) int {
	return slices.IndexFunc(s.rows, func(r store.Row) bool { return r.ID == rowID })
}

// recheck sends rows to the app and returns its verdicts, merged with what was sent.
func (s *session) recheck(ctx context.Context, rows []arr.Row) ([]arr.Row, error) {
	for i := range rows {
		rows[i].MovieID, rows[i].SeriesID = 0, 0
		if s.show() {
			rows[i].SeriesID = s.t.TitleID
		} else {
			rows[i].MovieID = s.t.TitleID
		}
	}
	fresh, err := s.imp.Recheck(ctx, rows)
	if err != nil {
		return nil, appFailure(s.t.App, err)
	}
	merged, err := mergeAnswers(rows, fresh)
	if err != nil {
		return nil, appFailure(s.t.App, err)
	}
	return merged, nil
}

// store writes the app's verdict and the merged edit onto row i (not yet persisted).
func (s *session) store(i int, ar arr.Row, e Edit) error {
	r := &s.rows[i]
	var err error
	if r.Proposal, err = json.Marshal(ar); err != nil {
		return err
	}
	if r.Edit, err = e.merge(r.Edit); err != nil {
		return err
	}
	r.Skip = s.skip(rowEval{ar: ar, offered: true, edit: decodeEdit(r.Edit)})
	return nil
}

// appPath is row i's file as the app sees it, derived from the job's file, never from
// what an app said earlier.
func (s *session) appPath(i int) (string, error) {
	f, ok := s.files[s.rows[i].FileID]
	if !ok {
		return "", wrongState() // the file, and so the row, is gone
	}
	return s.p.appPath(s.t.App, s.job.ID, f.Path), nil
}

// editAttempts is how often an edit starts again when another edit of the same rows was
// stored while the app was re-checking this one.
const editAttempts = 5

// errRetry: another writer changed the rows this edit read; read them again.
var errRetry = errors.New("flows: retry")

func sameRow(a, b store.Row) bool {
	return a.ID == b.ID && a.FileID == b.FileID && a.Skip == b.Skip && a.Override == b.Override &&
		a.Result == b.Result && string(a.Edit) == string(b.Edit) && string(a.Proposal) == string(b.Proposal)
}

// commit stores the rows at idx of the session, which were read as base, under the job's
// lock (shared with the match flow): each of those rows must be unchanged since it was read
// (else errRetry) and name the job's title. The other rows are kept as they are now. The
// write is PutProposal, which checks in its transaction that the job is still awaiting
// confirmation for the same title. The session's rows become the job's rows as stored.
func (s *session) commit(ctx context.Context, base []store.Row, idx []int) error {
	defer s.p.Lock(s.job.ID)()
	cur, err := s.p.st.Rows(ctx, s.job.ID)
	if err != nil {
		return err
	}
	at := make([]int, len(idx))
	changed := make([]store.Row, 0, len(idx))
	for k, i := range idx {
		j := slices.IndexFunc(cur, func(r store.Row) bool { return r.ID == s.rows[i].ID })
		if j < 0 {
			return wrongState() // the rows were replaced: a new proposal
		}
		if !sameRow(cur[j], base[i]) {
			return errRetry
		}
		at[k] = j
		changed = append(changed, s.rows[i])
	}
	if err := forTarget(changed, s.t); err != nil {
		return appFailure(s.t.App, err)
	}
	for k, i := range idx {
		cur[at[k]] = s.rows[i]
	}
	_, err = s.p.st.PutProposal(ctx, s.job.ID, store.AwaitingConfirmation, store.AwaitingConfirmation, s.t, cur, store.Change{Actor: actor})
	if errors.Is(err, store.ErrConflict) || errors.Is(err, store.ErrNotFound) {
		return wrongState()
	}
	if err != nil {
		return err
	}
	s.rows = cur
	return nil
}

// EditRow applies an edit to one row, re-checks the row through the app, stores it and
// returns it. Errors: refusals with the codes wrong_state, not_found
// and bad_request, and failures of the app (arr_unavailable). The app is asked without the job's lock; an edit of the same row stored
// meanwhile makes this one start again from the fresh row.
func (p *Proposals) EditRow(ctx context.Context, job store.Job, rowID string, e Edit) (RowView, error) {
	for range editAttempts {
		v, err := p.editRow(ctx, job.ID, rowID, e)
		if !errors.Is(err, errRetry) {
			return v, err
		}
	}
	return RowView{}, rowsChanged()
}

func (p *Proposals) editRow(ctx context.Context, jobID, rowID string, e Edit) (RowView, error) {
	s, err := p.edit(ctx, jobID)
	if err != nil {
		return RowView{}, err
	}
	i := s.find(rowID)
	if i < 0 {
		return RowView{}, rowNotFound()
	}
	base := slices.Clone(s.rows)
	ar, offered, err := decodeRow(s.rows[i])
	if err != nil {
		return RowView{}, err
	}
	if !offered {
		// Nothing to re-check: the app does not see the file. Skipping it is all there is.
		if e.SeasonNumber != nil || e.EpisodeIDs != nil || e.QualityID != nil || e.LanguageIDs != nil {
			return RowView{}, badRequest("The app did not offer this file, so it cannot be changed or imported.")
		}
		if s.rows[i].Edit, err = e.merge(s.rows[i].Edit); err != nil {
			return RowView{}, err
		}
		s.rows[i].Skip = true
	} else {
		if ar.Path, err = s.appPath(i); err != nil {
			return RowView{}, err
		}
		if err := s.loadPickers(ctx, e.QualityID != nil, e.LanguageIDs != nil); err != nil {
			return RowView{}, err
		}
		if err := s.apply(&ar, &e); err != nil {
			return RowView{}, err
		}
		fresh, err := s.recheck(ctx, []arr.Row{ar})
		if err != nil {
			return RowView{}, err
		}
		if err := s.store(i, fresh[0], e); err != nil {
			return RowView{}, err
		}
	}
	id := s.rows[i].ID
	if err := s.commit(ctx, base, []int{i}); err != nil {
		return RowView{}, err
	}
	views, _, err := s.views(ctx)
	if err != nil {
		return RowView{}, err
	}
	return views[s.find(id)], nil
}

// apply validates an edit and writes it onto the row. For a show it normalises e to the
// season and episodes it ends up with.
func (s *session) apply(ar *arr.Row, e *Edit) error {
	if e.SeasonNumber != nil && *e.SeasonNumber < 0 {
		return badRequest("seasonNumber must be 0 or more.")
	}
	if !s.show() && (e.SeasonNumber != nil || e.EpisodeIDs != nil) {
		return badRequest("A film has no seasons or episodes.")
	}
	switch {
	case e.EpisodeIDs != nil:
		ids := slices.Clone(*e.EpisodeIDs)
		if len(ids) == 0 {
			setEpisodes(ar, nil, ar.SeasonNumber)
			if e.SeasonNumber != nil {
				ar.SeasonNumber = *e.SeasonNumber
			}
			break
		}
		seen := map[int]bool{}
		for _, id := range ids {
			if seen[id] {
				return badRequest("Episode %d is listed twice.", id)
			}
			seen[id] = true
			if _, ok := s.idx[id]; !ok {
				return badRequest("Episode %d is not in this series.", id)
			}
		}
		season, ok := oneSeason(ids, s.idx)
		if !ok {
			return badRequest("The episodes of one file must be in one season.")
		}
		if e.SeasonNumber != nil && *e.SeasonNumber != season {
			return badRequest("Episode %d is not in season %d.", ids[0], *e.SeasonNumber)
		}
		sortEpisodes(ids, s.idx)
		setEpisodes(ar, ids, season)
	case e.SeasonNumber != nil:
		ids, err := s.renumber(*ar, *e.SeasonNumber, 0)
		if err != nil {
			return err
		}
		setEpisodes(ar, ids, *e.SeasonNumber)
	}
	if s.show() && (e.EpisodeIDs != nil || e.SeasonNumber != nil) {
		season, ids := ar.SeasonNumber, slices.Clone(ar.EpisodeIDs)
		if ids == nil {
			ids = []int{}
		}
		e.SeasonNumber, e.EpisodeIDs = &season, &ids
	}
	if e.QualityID != nil {
		i := slices.IndexFunc(s.quals, func(q arr.Quality) bool { return q.ID == *e.QualityID })
		if i < 0 {
			return badRequest("Quality %d is not one the app knows.", *e.QualityID)
		}
		ar.Quality = s.quals[i]
	}
	if e.LanguageIDs != nil {
		if len(*e.LanguageIDs) == 0 {
			return badRequest("Choose at least one language.")
		}
		var ls []arr.Language
		for _, id := range *e.LanguageIDs {
			i := slices.IndexFunc(s.langs, func(l arr.Language) bool { return l.ID == id })
			if i < 0 {
				return badRequest("Language %d is not one the app knows.", id)
			}
			if slices.ContainsFunc(ls, func(l arr.Language) bool { return l.ID == id }) {
				return badRequest("Language %d is listed twice.", id)
			}
			ls = append(ls, s.langs[i])
		}
		ar.Languages = ls
	}
	return nil
}

// setEpisodes gives a row its episodes. No episodes also drops the app's earlier ones from
// the item, so that they cannot reach the app again.
func setEpisodes(ar *arr.Row, ids []int, season int) {
	ar.EpisodeIDs, ar.SeasonNumber = ids, season
	if len(ids) == 0 {
		ar.EpisodeIDs = nil
		ar.Raw = withoutKeys(ar.Raw, "episodes", "episodeIds")
	}
}

// renumber returns the series' episodes in season with the row's episode numbers plus
// shift. A row without episodes has none to move.
func (s *session) renumber(ar arr.Row, season, shift int) ([]int, error) {
	var refs []epRef
	for _, id := range ar.EpisodeIDs {
		ep, ok := s.idx[id]
		if !ok {
			return nil, badRequest("Episode %d is not in this series.", id)
		}
		refs = append(refs, epRef{season, ep.Number + shift})
	}
	if len(refs) == 0 {
		return nil, nil
	}
	ids, ok := lookupEpisodes(refs, s.eps)
	if !ok {
		for _, r := range refs {
			if _, found := lookupEpisodes([]epRef{r}, s.eps); !found {
				return nil, badRequest("Season %d has no episode %d.", r.season, r.number)
			}
		}
		return nil, badRequest("Season %d does not have these episodes.", season)
	}
	return ids, nil
}

// Bulk applies a bulk edit to rows of a show, re-checks the changed rows through the app
// in one call, stores them in one transaction and returns the requested rows in the order
// asked. Rows the app did not offer, and (for a shift) rows without episodes, are returned
// unchanged. Errors as for EditRow.
func (p *Proposals) Bulk(ctx context.Context, job store.Job, b BulkEdit) ([]RowView, error) {
	if len(b.RowIDs) == 0 {
		return nil, badRequest("rowIds must list at least one row.")
	}
	if (b.Shift == nil) == (b.SeasonNumber == nil) {
		return nil, badRequest("Give either shift or seasonNumber.")
	}
	if b.Shift != nil && *b.Shift == 0 {
		return nil, badRequest("shift must not be 0.")
	}
	if b.SeasonNumber != nil && *b.SeasonNumber < 0 {
		return nil, badRequest("seasonNumber must be 0 or more.")
	}
	for range editAttempts {
		v, err := p.bulk(ctx, job.ID, b)
		if !errors.Is(err, errRetry) {
			return v, err
		}
	}
	return nil, rowsChanged()
}

func (p *Proposals) bulk(ctx context.Context, jobID string, b BulkEdit) ([]RowView, error) {
	s, err := p.edit(ctx, jobID)
	if err != nil {
		return nil, err
	}
	if !s.show() {
		return nil, badRequest("Bulk edits are for the episodes of a show.")
	}
	base := slices.Clone(s.rows)
	var (
		ids  []string // the requested rows
		send []arr.Row
		sent []int // row index of each row sent
	)
	for _, id := range b.RowIDs {
		i := s.find(id)
		if i < 0 {
			return nil, rowNotFound()
		}
		if slices.Contains(ids, id) {
			return nil, badRequest("Row %s is listed twice.", id)
		}
		ids = append(ids, id)
		ar, offered, err := decodeRow(s.rows[i])
		if err != nil {
			return nil, err
		}
		if !offered {
			continue
		}
		season, shift := ar.SeasonNumber, 0
		if b.Shift != nil {
			if len(ar.EpisodeIDs) == 0 {
				continue
			}
			if sn, ok := oneSeason(ar.EpisodeIDs, s.idx); ok {
				season = sn
			}
			shift = *b.Shift
		} else {
			season = *b.SeasonNumber
		}
		eps, err := s.renumber(ar, season, shift)
		var bad *Error
		if errors.As(err, &bad) {
			return nil, badRequest("Row %s: %s", id, bad.Message)
		}
		if err != nil {
			return nil, err
		}
		if ar.Path, err = s.appPath(i); err != nil {
			return nil, err
		}
		setEpisodes(&ar, eps, season)
		send = append(send, ar)
		sent = append(sent, i)
	}
	if len(send) > 0 {
		fresh, err := s.recheck(ctx, send)
		if err != nil {
			return nil, err
		}
		for k, i := range sent {
			season, eps := send[k].SeasonNumber, slices.Clone(send[k].EpisodeIDs)
			if eps == nil {
				eps = []int{}
			}
			if err := s.store(i, fresh[k], Edit{SeasonNumber: &season, EpisodeIDs: &eps}); err != nil {
				return nil, err
			}
		}
		if err := s.commit(ctx, base, sent); err != nil {
			return nil, err
		}
	}
	views, _, err := s.views(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]RowView, 0, len(ids))
	for _, id := range ids {
		out = append(out, views[s.find(id)])
	}
	return out, nil
}
