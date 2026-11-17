package flows

import (
	"context"
	"encoding/json"
	"errors"
	"math"
	"path"
	"slices"

	"github.com/segun-io/fukku/internal/arr"
	"github.com/segun-io/fukku/internal/store"
)

// The proposal as the API returns it (GET /api/jobs/{id}/proposal).

// Proposal is a job's proposal. For a film, rows have no seasonNumber or episodes, and
// Episodes is absent. Episodes, Qualities and Languages are what the pickers offer.
type Proposal struct {
	State  string      `json:"state"` // StatePending or StateReady
	App    string      `json:"app"`
	Target *TargetView `json:"target"`
	Rows   []RowView   `json:"rows"`
	Hints  []Notice    `json:"hints"`
	// SeasonDub asks what to do with rows whose episodes already have a file (#57); absent
	// when there are none.
	SeasonDub *SeasonDub `json:"seasonDub,omitempty"`
	// NotOffered lists what this user may not confirm, with the reason (#116): the offer
	// override (importing a row despite a rejection by Radarr or Sonarr, or an
	// existing_file warning; Fukku's other warnings stay open) with admin_required, while
	// auth.libraryChangesRequireAdmin is on and the user is not an admin. Empty otherwise.
	NotOffered []NotOffered   `json:"notOffered"`
	Episodes   *[]EpisodeView `json:"episodes,omitempty"`
	Qualities  []Ref          `json:"qualities"`
	Languages  []Ref          `json:"languages"`
}

type TargetView struct {
	TitleID int    `json:"titleId"`
	Title   string `json:"title"`
	Year    int    `json:"year"`
}

// Who skipped a row (RowView.SkippedBy).
const (
	SkippedByUser   = "user"
	SkippedByServer = "server"
)

// RowView is one row: a job file, what the app makes of it, and what Fukku adds.
type RowView struct {
	ID         string `json:"id"`
	FileID     int64  `json:"fileId"`
	Name       string `json:"name"`
	DurationMs int64  `json:"durationMs"`
	Skip       bool   `json:"skip"`
	// SkippedBy says who left the row out while Skip is set: "user" (the user said so, or the
	// film's extra files start that way) or "server" (the app did not offer the file, or an
	// episode of it has a file and the season-dub choice does not replace it). The user can
	// record a skip on a row the server skips: it then keeps its own file under any policy
	// and gets no version. Absent while the row is not skipped.
	SkippedBy string `json:"skippedBy,omitempty"`
	// Shows only: the season (absent while unknown) and the episodes.
	SeasonNumber      *int           `json:"seasonNumber,omitempty"`
	Episodes          *[]EpisodeView `json:"episodes,omitempty"`
	Quality           *Ref           `json:"quality"` // null: the app did not offer the file
	Languages         []Ref          `json:"languages"`
	CustomFormats     []string       `json:"customFormats"`
	CustomFormatScore int            `json:"customFormatScore"`
	Rejections        []Rejection    `json:"rejections"`
	Warnings          []Notice       `json:"warnings"`
	// Importable: not skipped, has the title (and an episode, for a show), and no
	// rejection or warning. A row with rejections or warnings is imported only when the user
	// overrides them at confirmation (#40).
	Importable bool `json:"importable"`
}

// EpisodeView is an episode of the series. SeasonNumber is set in the proposal's list,
// not on a row's episodes (the row has its own).
type EpisodeView struct {
	ID           int    `json:"id"`
	SeasonNumber *int   `json:"seasonNumber,omitempty"`
	Number       int    `json:"number"`
	Title        string `json:"title"`
	RuntimeMs    int64  `json:"runtimeMs"`
	HasFile      bool   `json:"hasFile"`
}

type Ref struct {
	ID   int    `json:"id"`
	Name string `json:"name"`
}

type Rejection struct {
	Reason    string `json:"reason"`
	Permanent bool   `json:"permanent"`
}

// Notice is a warning or a hint.
type Notice struct {
	Code string `json:"code"`
	// The facts of the warning `language` (WarnLanguage); absent on the others.
	CustomFormats  []string `json:"customFormats,omitempty"`
	Score          *int     `json:"score,omitempty"`
	MinFormatScore *int     `json:"minFormatScore,omitempty"`
}

// View returns the job's proposal: pending while the matched step has not finished
// (the job is matched, or failed or stalled on its way out of matched), ready while the job
// is awaiting confirmation; a wrong_state refusal otherwise. A ready proposal asks the app for the
// pickers (the series' episodes, the qualities and languages), so its warnings describe
// what the app knows now; an arr_unavailable failure when it does not answer. admin is
// whether the user is an admin: what only an admin may confirm (#116) is not offered to
// others.
func (p *Proposals) View(ctx context.Context, job store.Job, admin bool) (Proposal, error) {
	// No lock: the rows are read in one call, and writers replace them in one.
	job, err := p.st.Job(ctx, job.ID)
	if err != nil {
		return Proposal{}, err
	}
	pending := job.State == store.Matched ||
		((job.State == store.Failed || job.State == store.Stalled) && job.Resume == store.Matched)
	if !pending && job.State != store.AwaitingConfirmation {
		return Proposal{}, wrongState()
	}
	t, err := p.st.Target(ctx, job.ID)
	if err != nil && (!errors.Is(err, store.ErrNotFound) || !pending) {
		return Proposal{}, err
	}
	out := Proposal{App: t.App, Rows: []RowView{}, Hints: []Notice{}, NotOffered: []NotOffered{}, Qualities: []Ref{}, Languages: []Ref{}}
	changes := LibraryChangesAllowed(p.cfg, admin)
	if !changes {
		out.NotOffered = append(out.NotOffered, NotOffered{Offer: OfferOverride, Reason: ReasonAdminRequired})
	}
	if t.App != "" {
		out.Target = &TargetView{TitleID: t.TitleID, Title: t.Title, Year: t.Year}
	}
	if pending {
		out.State = StatePending
		return out, nil
	}
	out.State = StateReady
	s, err := p.open(ctx, job, t, true)
	if err != nil {
		return Proposal{}, err
	}
	s.changes = changes
	if out.Rows, out.Hints, err = s.views(ctx); err != nil {
		return Proposal{}, err
	}
	if err := s.checkEpisodeVersions(ctx); err != nil { // #58: whether the policy version is offered
		return Proposal{}, err
	}
	out.SeasonDub = s.seasonDub()
	if sd := out.SeasonDub; sd != nil && slices.Contains(sd.Offers, PolicyVersion) {
		if sd.Version, err = s.episodeVersionsView(ctx); err != nil {
			return Proposal{}, err
		}
	}
	for _, q := range s.quals {
		out.Qualities = append(out.Qualities, Ref(q))
	}
	for _, l := range s.langs {
		out.Languages = append(out.Languages, Ref(l))
	}
	if t.App == AppSonarr {
		eps := make([]EpisodeView, 0, len(s.eps))
		for _, e := range s.eps {
			v := episodeView(e)
			v.SeasonNumber = &e.SeasonNumber
			eps = append(eps, v)
		}
		slices.SortStableFunc(eps, func(a, b EpisodeView) int {
			if *a.SeasonNumber != *b.SeasonNumber {
				return *a.SeasonNumber - *b.SeasonNumber
			}
			return a.Number - b.Number
		})
		out.Episodes = &eps
	}
	return out, nil
}

func episodeView(e arr.Episode) EpisodeView {
	return EpisodeView{ID: e.ID, Number: e.Number, Title: e.Title, RuntimeMs: e.RuntimeMs, HasFile: e.FileID != 0}
}

// session is what the view and the edits of one job work on, loaded under the job's lock.
type session struct {
	p     *Proposals
	job   store.Job
	t     store.Target
	imp   arr.Importer
	rows  []store.Row
	files map[int64]store.File
	eps   []arr.Episode
	idx   map[int]arr.Episode
	quals []arr.Quality
	langs []arr.Language
	// changes: the user may confirm what changes a file the library has (#116,
	// LibraryChangesAllowed). The view and the confirmation set it; false otherwise.
	changes bool
	// dub is what the confirmation chose for the rows that have a file (PolicyReplace,
	// PolicySubtitlesOnly or PolicyVersion); "" while the proposal is only viewed or edited.
	dub string
	// epvChecked: whether episodes' versions are offered was settled (checkEpisodeVersions,
	// #58), and epvNot says why not (nil: they are).
	epvChecked bool
	epvNot     *NotOffered
	epvLib     *epvLibrary // Sonarr's series folder and episode files, once read
}

// open loads the job's rows and files, and from the app the series' episodes (for a show)
// and, with pickers, the qualities and languages.
func (p *Proposals) open(ctx context.Context, job store.Job, t store.Target, pickers bool) (*session, error) {
	s := &session{p: p, job: job, t: t, imp: p.importer(t.App), files: map[int64]store.File{}}
	if s.imp == nil {
		return nil, appFailure(t.App, errors.New("not configured"))
	}
	var err error
	if s.rows, err = p.st.Rows(ctx, job.ID); err != nil {
		return nil, err
	}
	files, err := p.st.Files(ctx, job.ID)
	if err != nil {
		return nil, err
	}
	for _, f := range files {
		s.files[f.ID] = f
	}
	if t.App == AppSonarr {
		if s.eps, err = p.sonarr.Episodes(ctx, t.TitleID); err != nil {
			return nil, appFailure(t.App, err)
		}
	}
	s.idx = episodeIndex(s.eps)
	if pickers {
		if err := s.loadPickers(ctx, true, true); err != nil {
			return nil, err
		}
	}
	return s, nil
}

func (s *session) loadPickers(ctx context.Context, quals, langs bool) error {
	var err error
	if quals && s.quals == nil {
		if s.quals, err = s.imp.Qualities(ctx); err != nil {
			return appFailure(s.t.App, err)
		}
	}
	if langs && s.langs == nil {
		if s.langs, err = s.imp.Languages(ctx); err != nil {
			return appFailure(s.t.App, err)
		}
	}
	return nil
}

func (s *session) show() bool { return s.t.App == AppSonarr }

// rowEval is one row with what the warnings need.
type rowEval struct {
	row      store.Row
	ar       arr.Row
	offered  bool
	edit     Edit
	duration int64
	warnings []Notice
}

// views computes every row's view and the job's hints. Warnings need all the rows: two
// rows may claim one episode.
func (s *session) views(ctx context.Context) ([]RowView, []Notice, error) {
	m := s.p.cfg.Match
	evals := make([]rowEval, len(s.rows))
	comparable, mismatches := 0, 0
	claims := map[int]int{} // episode id → rows that are not skipped
	films := 0              // offered rows of a film that are not skipped
	for i, r := range s.rows {
		ar, offered, err := decodeRow(r)
		if err != nil {
			return nil, nil, err
		}
		e := rowEval{row: r, ar: ar, offered: offered, edit: decodeEdit(r.Edit), duration: probeOf(s.files[r.FileID]).DurationMs}
		switch {
		case !offered:
			e.warnings = append(e.warnings, Notice{Code: WarnNotOffered})
		case s.show():
			if hasExistingFile(ar, s.idx) {
				e.warnings = append(e.warnings, Notice{Code: WarnExistingFile})
			}
			var runtime int64
			known := len(ar.EpisodeIDs) > 0
			for _, id := range ar.EpisodeIDs {
				ep, ok := s.idx[id]
				if !ok || ep.RuntimeMs <= 0 {
					known = false
				}
				runtime += ep.RuntimeMs
			}
			if known && e.duration > 0 {
				comparable++
				ratio := float64(e.duration) / float64(runtime)
				switch {
				case len(ar.EpisodeIDs) == 1 && math.Abs(ratio-m.DoubleEpisodeRatio) <= m.DoubleEpisodeTolerance*m.DoubleEpisodeRatio:
					e.warnings = append(e.warnings, Notice{Code: WarnDoubleEpisode})
				case math.Abs(ratio-1) > m.RuntimeMismatchTolerance:
					e.warnings = append(e.warnings, Notice{Code: WarnRuntimeMismatch})
					mismatches++
				}
			}
		}
		if s.skip(e) {
			e.row.Skip = true
		} else {
			e.row.Skip = false
			if !s.show() {
				films++
			}
			for _, id := range ar.EpisodeIDs {
				claims[id]++
			}
		}
		evals[i] = e
	}
	views := make([]RowView, len(evals))
	lang := s.languageRule()
	for i := range evals {
		e := &evals[i]
		if e.offered && !e.row.Skip && slices.ContainsFunc(e.ar.EpisodeIDs, func(id int) bool { return claims[id] > 1 }) {
			e.warnings = append(e.warnings, Notice{Code: WarnDuplicateEpisode})
		}
		if e.offered && !e.row.Skip && films > 1 {
			e.warnings = append(e.warnings, Notice{Code: WarnDuplicateFilm})
		}
		if e.offered {
			n, err := lang.check(ctx, e.ar)
			if err != nil {
				return nil, nil, err
			}
			if n != nil {
				e.warnings = append(e.warnings, *n)
			}
		}
		views[i] = s.view(*e)
	}
	hints := []Notice{}
	if comparable >= 2 && float64(mismatches) > m.DVDOrderShare*float64(comparable) {
		hints = append(hints, Notice{Code: HintDVDOrder})
	}
	return views, hints, nil
}

// skip is whether a row is skipped: the user said so, or it cannot be imported (the app did
// not offer the file, or an episode already has a file and the season-dub choice does not
// replace it). Rows without a file import as usual under any policy.
func (s *session) skip(e rowEval) bool {
	user := e.edit.Skip != nil && *e.edit.Skip
	switch {
	case user || !e.offered:
		return true
	case s.show() && hasExistingFile(e.ar, s.idx):
		return s.dub != PolicyReplace // subtitlesOnly imports nothing over a file
	}
	return false
}

func (s *session) view(e rowEval) RowView {
	f := s.files[e.row.FileID]
	v := RowView{
		ID: e.row.ID, FileID: e.row.FileID, Name: path.Base(f.Path), DurationMs: e.duration, Skip: e.row.Skip,
		Languages: []Ref{}, CustomFormats: []string{}, Rejections: []Rejection{}, Warnings: []Notice{},
	}
	v.Warnings = append(v.Warnings, e.warnings...)
	if e.row.Skip {
		v.SkippedBy = SkippedByServer
		if e.edit.Skip != nil && *e.edit.Skip {
			v.SkippedBy = SkippedByUser
		}
	}
	if s.show() {
		eps := []EpisodeView{}
		v.Episodes = &eps
	}
	if !e.offered {
		return v
	}
	ar := e.ar
	v.Quality = &Ref{ID: ar.Quality.ID, Name: ar.Quality.Name}
	for _, l := range ar.Languages {
		v.Languages = append(v.Languages, Ref(l))
	}
	v.CustomFormats = append(v.CustomFormats, ar.CustomFormats...)
	v.CustomFormatScore = ar.CustomFormatScore
	for _, r := range ar.Rejections {
		v.Rejections = append(v.Rejections, Rejection(r))
	}
	hasTitle := ar.MovieID == s.t.TitleID
	if s.show() {
		hasTitle = ar.SeriesID == s.t.TitleID
		eps := make([]EpisodeView, 0, len(ar.EpisodeIDs))
		for _, id := range ar.EpisodeIDs {
			ep, ok := s.idx[id]
			if !ok {
				ep = arr.Episode{ID: id}
			}
			eps = append(eps, episodeView(ep))
		}
		v.Episodes = &eps
		if season, ok := oneSeason(ar.EpisodeIDs, s.idx); ok {
			v.SeasonNumber = &season
		} else if len(ar.EpisodeIDs) > 0 || ar.SeasonNumber > 0 || e.edit.SeasonNumber != nil {
			season := ar.SeasonNumber
			v.SeasonNumber = &season
		}
	}
	v.Importable = !v.Skip && hasTitle && (!s.show() || len(ar.EpisodeIDs) > 0) &&
		len(v.Rejections) == 0 && len(v.Warnings) == 0
	return v
}

// Edit is a change to one row (PATCH …/proposal/rows/{rowId}); absent fields stay as they
// are. A row's stored Edit is every change the user made to it, merged.
type Edit struct {
	Skip         *bool  `json:"skip,omitempty"`
	SeasonNumber *int   `json:"seasonNumber,omitempty"`
	EpisodeIDs   *[]int `json:"episodeIds,omitempty"`
	QualityID    *int   `json:"qualityId,omitempty"`
	LanguageIDs  *[]int `json:"languageIds,omitempty"`
}

func decodeEdit(raw json.RawMessage) Edit {
	var e Edit
	if len(raw) > 0 {
		_ = json.Unmarshal(raw, &e)
	}
	return e
}

// merge writes e's fields over the stored edit.
func (e Edit) merge(stored json.RawMessage) (json.RawMessage, error) {
	m := decodeEdit(stored)
	if e.Skip != nil {
		m.Skip = e.Skip
	}
	if e.SeasonNumber != nil {
		m.SeasonNumber = e.SeasonNumber
	}
	if e.EpisodeIDs != nil {
		m.EpisodeIDs = e.EpisodeIDs
	}
	if e.QualityID != nil {
		m.QualityID = e.QualityID
	}
	if e.LanguageIDs != nil {
		m.LanguageIDs = e.LanguageIDs
	}
	return json.Marshal(m)
}
