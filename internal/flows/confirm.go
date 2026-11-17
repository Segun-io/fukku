package flows

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strconv"
	"strings"

	"github.com/segun-io/fukku/internal/store"
)

// ConfirmRequest is the body of POST /api/jobs/{id}/confirm: the rows whose rejections or
// warnings the user accepts (probes B8-radarr and B8-sonarr: with explicit ids, the apps
// import despite rejections), and the language and flags of the job's subtitle files, for
// the hand-over to the subtitle manager (#42). A subtitle file not listed keeps what its
// name says.
type ConfirmRequest struct {
	Overrides []string         `json:"overrides"`
	Subtitles []SubtitleChoice `json:"subtitles"`
	// SeasonDub is what to do with rows whose episodes already have a file (#57): one of
	// the proposal's seasonDub.offers when seasonDub.ask is set; empty otherwise.
	SeasonDub string `json:"seasonDub,omitempty"`
	// Flow2 is what to do with a flow 2 job (#52): "merge" adds the upload's tracks to the
	// film's file (#55, merge.go), with Merge; "version" keeps the upload as a second
	// version of the film (#56), with Version. Empty: an import, which a flow 2 job refuses.
	Flow2   string          `json:"flow2,omitempty"`
	Merge   *MergeRequest   `json:"merge,omitempty"`
	Version *VersionRequest `json:"version,omitempty"`
}

// Confirmation is what a confirmation did, for the audit log.
type Confirmation struct {
	Job        store.Job
	Target     store.Target
	Rows       []string // the rows to import, in the job's order
	Overridden []Overridden
	Subtitles  []SubtitleChoice
	// SeasonDub is the policy applied to rows that have a file ("": none), with the seasons
	// and the rows it covers.
	SeasonDub string
	Seasons   []int
	DubRows   []string
}

// Overridden is a row imported despite its rejections or warnings.
type Overridden struct {
	RowID      string
	File       string // the file's name
	Rejections []string
	Warnings   []string
	// Language is the language warning (custom formats, score and the profile's minimum)
	// when it was one of the warnings overridden.
	Language *Notice
}

// Confirm moves a job from awaiting-confirmation to importing, as user (email for the
// audit log). Every row that is not
// skipped must be importable, or have only rejections and warnings and be listed in
// req.Overrides. The rows are judged as the proposal shows them now (View), so the warnings
// are those the user saw; for a show the app is asked for its episodes. The job's lock is
// held for the write, never across the call to the app: the rows must be unchanged since
// they were judged (an edit meanwhile makes it judge again), and the transition checks
// that the job still has the same title.
//
// A flow 2 job is confirmed with req.Flow2: "merge" (confirmMerge, merge.go).
//
// Refusals: wrong_state, flow2 (the film already has a file, at the match or since: flow 2, #52), bad_request (an
// override names no row of the job, or flow2 for a job that is not flow 2),
// storage_unavailable (the filesystem checks fail), incomplete_rows (rows without a title
// or episodes: edit or skip them), needs_override (Rows lists them), nothing_to_import;
// arr_unavailable when the app does not answer.
func (e *Engine) Confirm(ctx context.Context, user, email string, job store.Job, req ConfirmRequest) (Confirmation, error) {
	for range editAttempts {
		c, err := e.confirm(ctx, user, email, job.ID, req)
		if !errors.Is(err, errRetry) {
			return c, err
		}
	}
	return Confirmation{}, rowsChanged()
}

func (e *Engine) confirm(ctx context.Context, user, email, jobID string, req ConfirmRequest) (Confirmation, error) {
	s, err := e.p.edit(ctx, jobID)
	if err != nil {
		return Confirmation{}, err
	}
	if rec, ok := Flow2Of(s.t); ok {
		switch req.Flow2 {
		case OfferMerge:
			if req.Version != nil {
				return Confirmation{}, badRequest("A merge takes merge, not version.")
			}
			return e.confirmMerge(ctx, user, email, s, rec, req) // #55
		case OfferVersion:
			if req.Merge != nil {
				return Confirmation{}, badRequest("A second version takes version, not merge.")
			}
			return e.confirmVersion(ctx, user, email, s, rec, req) // #56
		}
		// Importing the upload would replace the film's file: flow 2 merges or adds a version.
		return Confirmation{}, refuse(CodeFlow2, "The film already has a file in the library: this job can merge the new language into it or keep it as a second version, not import it.")
	}
	if req.Flow2 != "" || req.Merge != nil || req.Version != nil {
		return Confirmation{}, badRequest("flow2, merge and version apply only to a job whose film already has a file (flow 2).")
	}
	if s.t.App == AppRadarr {
		// A flow 1 job was matched to a film without a file; one may have appeared since
		// (Radarr's RSS sync of a monitored film, say), and importing would replace it.
		m, err := e.p.radarr.Movie(ctx, s.t.TitleID)
		if err != nil {
			return Confirmation{}, appFailure(s.t.App, err)
		}
		if m.File != nil {
			return Confirmation{}, refuse(CodeFlow2, "The film has a file in the library now: this job can merge the new language into it or keep it as a second version, not import it. Match the film again.")
		}
	}
	for _, id := range req.Overrides {
		if s.find(id) < 0 {
			return Confirmation{}, badRequest("Row %s is not a row of this job.", id)
		}
	}
	subs, err := s.subtitleChoices(req.Subtitles)
	if err != nil {
		return Confirmation{}, err
	}
	if err := e.checkStorage(); err != nil {
		e.log.Warn("import_storage_unavailable", "job", jobID, "error", err)
		return Confirmation{}, refuse(CodeStorageUnavailable, "The storage is not available: a mount is missing, or the intake and library folders are on different filesystems.")
	}
	dub, ex, err := s.chooseDub(req.SeasonDub)
	if err != nil {
		return Confirmation{}, err
	}
	s.dub = dub
	views, _, err := s.views(ctx)
	if err != nil {
		return Confirmation{}, err
	}
	var (
		c          = Confirmation{Target: s.t, SeasonDub: dub, Seasons: ex.seasons, DubRows: ex.rows}
		incomplete []string
		needs      []string
		sharing    []string
	)
	override := map[string]bool{}
	for i, v := range views {
		if v.Skip {
			continue
		}
		ar, offered, err := decodeRow(s.rows[i])
		if err != nil {
			return Confirmation{}, err
		}
		if !offered || titleOK(s.t, ar) != nil {
			incomplete = append(incomplete, v.ID)
			continue
		}
		// With replace, the choice answers what the app says of replacing a file it has.
		replaced := dub == PolicyReplace && slices.Contains(ex.rows, v.ID)
		if replaced && len(ex.shared[v.ID]) > 0 && !slices.Contains(req.Overrides, v.ID) {
			sharing = append(sharing, v.ID)
			continue
		}
		if needsOverride(v, replaced) {
			if !slices.Contains(req.Overrides, v.ID) {
				needs = append(needs, v.ID)
				continue
			}
		}
		if len(v.Rejections) > 0 || len(v.Warnings) > 0 {
			o := Overridden{RowID: v.ID, File: v.Name}
			for _, r := range v.Rejections {
				o.Rejections = append(o.Rejections, r.Reason)
			}
			for _, w := range v.Warnings {
				o.Warnings = append(o.Warnings, w.Code)
				if w.Code == WarnLanguage {
					lw := w
					o.Language = &lw
				}
			}
			c.Overridden = append(c.Overridden, o)
			override[v.ID] = true
		}
		c.Rows = append(c.Rows, v.ID)
	}
	switch {
	case len(incomplete) > 0:
		err := refuse(CodeIncompleteRows, "Some rows have no title or no episode: choose one or skip them.")
		err.Rows = incomplete
		return Confirmation{}, err
	case len(sharing) > 0:
		err := refuse(CodeSharedFile, "Replacing these files would remove the library file of other episodes too (a file that holds several): add those episodes to the job, skip the rows, or confirm them as overrides.")
		err.Rows = sharing
		return Confirmation{}, err
	case len(needs) > 0:
		err := refuse(CodeNeedsOverride, "Some rows have rejections or warnings: confirm them as overrides, or skip them.")
		err.Rows = needs
		return Confirmation{}, err
	case len(c.Rows) == 0 && dub != PolicySubtitlesOnly:
		return Confirmation{}, refuse(CodeNothingToImport, "Every row is skipped: there is nothing to import.")
	}

	// The write, under the job's lock: the rows as judged, then the transition, which checks
	// the state and the title in its transaction.
	defer e.locks.Lock(jobID)()
	cur, err := e.st.Rows(ctx, jobID)
	if err != nil {
		return Confirmation{}, err
	}
	if len(cur) != len(s.rows) {
		return Confirmation{}, errRetry
	}
	for i := range cur {
		if !sameRow(cur[i], s.rows[i]) {
			return Confirmation{}, errRetry
		}
	}
	var flags []store.Row
	for i, r := range cur {
		want := r
		want.Skip = views[i].Skip
		want.Override = override[r.ID]
		want.Result = store.ResultNone
		if want.Skip {
			want.Result = store.ResultSkipped
		}
		if !sameRow(want, r) {
			flags = append(flags, want)
		}
	}
	// The subtitle choices go where the subtitles step reads them: the target's options.
	var choices any // nil: the key goes
	if subs != nil {
		choices = subs
	}
	opts, err := withOption(s.t.Options, "subtitles", choices)
	if err != nil {
		return Confirmation{}, err
	}
	var dubChoice any // nil: the key goes
	if dub != "" {
		dubChoice = seasonDubChoice{Policy: dub, Seasons: ex.seasons, Rows: ex.rows}
	}
	if opts, err = withOption(opts, "seasonDub", dubChoice); err != nil {
		return Confirmation{}, err
	}
	if string(opts) == string(s.t.Options) {
		opts = nil // unchanged
	} else {
		c.Target.Options = opts
	}
	c.Subtitles = req.Subtitles
	note := fmt.Sprintf("%d file(s) confirmed for %s", len(c.Rows), s.t.App)
	switch dub {
	case PolicyReplace:
		note += fmt.Sprintf(", replacing the existing files of %s", seasonsText(ex.seasons))
	case PolicySubtitlesOnly:
		note = fmt.Sprintf("nothing to import for %s: only the subtitles of %s are handed over", s.t.App, seasonsText(ex.seasons))
	}
	if len(c.Overridden) > 0 {
		var ids []string
		for _, o := range c.Overridden {
			ids = append(ids, o.RowID)
		}
		note += fmt.Sprintf(", %s despite rejections or warnings", strings.Join(ids, ", "))
	}
	// One transaction: the title is checked before anything is written, then the rows'
	// flags, the options, the transition and the audit entries.
	next, err := e.st.ConfirmImport(ctx, jobID, s.t, flags, opts, c.audit(user, email), store.Change{Actor: user, Note: note})
	if errors.Is(err, store.ErrConflict) || errors.Is(err, store.ErrInvalidTransition) || errors.Is(err, store.ErrNotFound) {
		return Confirmation{}, wrongState()
	}
	if err != nil {
		return Confirmation{}, err
	}
	e.log.Info("job_state", "job", jobID, "from", store.AwaitingConfirmation, "to", next.State, "attempt", next.Attempt,
		"rows", len(c.Rows), "overridden", len(c.Overridden))
	c.Job = next
	return c, nil
}

// needsOverride reports whether a row still has rejections or warnings the user must
// override. For a row replaced by the season-dub policy, the choice answers the existing_file
// warning and the app's refusals to replace a file (replaceCovers).
func needsOverride(v RowView, replaced bool) bool {
	for _, r := range v.Rejections {
		if !replaced || !replaceCovers(r.Reason) {
			return true
		}
	}
	for _, w := range v.Warnings {
		if !replaced || w.Code != WarnExistingFile {
			return true
		}
	}
	return false
}

// audit is the confirmation's audit entries: import.confirmed, rejection.overridden for
// each row imported despite its rejections or warnings, and language.overridden, with the
// custom formats and scores, for each whose language warning (#39) was among them.
// A season-dub policy adds season.replaced for each season whose files are replaced, and
// season.subtitles_only for each season whose episodes get the upload's subtitles only.
func (c Confirmation) audit(user, email string) []store.AuditEntry {
	overrides := make([]string, 0, len(c.Overridden))
	for _, o := range c.Overridden {
		overrides = append(overrides, o.RowID)
	}
	subs := make([]string, 0, len(c.Subtitles))
	for _, sc := range c.Subtitles {
		subs = append(subs, fmt.Sprintf("%d:%s forced=%t hi=%t", sc.FileID, sc.Language, sc.Forced, sc.HearingImpaired))
	}
	entry := func(action string, d map[string]string) store.AuditEntry {
		return store.AuditEntry{Actor: user, Email: email, Action: action, Detail: d}
	}
	out := []store.AuditEntry{entry("import.confirmed", map[string]string{
		"app": c.Target.App, "titleId": strconv.Itoa(c.Target.TitleID), "title": c.Target.Title,
		"rows": strings.Join(c.Rows, ","), "overrides": strings.Join(overrides, ","),
		"subtitles": strings.Join(subs, "; "),
	})}
	for _, season := range c.Seasons {
		switch c.SeasonDub {
		case PolicyReplace:
			out = append(out, entry("season.replaced", map[string]string{
				"app": c.Target.App, "titleId": strconv.Itoa(c.Target.TitleID), "title": c.Target.Title,
				"season": strconv.Itoa(season), "rows": strings.Join(c.DubRows, ","), "policy": PolicyReplace,
			}))
		case PolicySubtitlesOnly:
			out = append(out, entry("season.subtitles_only", map[string]string{
				"app": c.Target.App, "titleId": strconv.Itoa(c.Target.TitleID), "title": c.Target.Title,
				"season": strconv.Itoa(season), "rows": strings.Join(c.DubRows, ","), "policy": PolicySubtitlesOnly,
			}))
		}
	}
	for _, o := range c.Overridden {
		out = append(out, entry("rejection.overridden", map[string]string{
			"row": o.RowID, "file": o.File,
			"rejections": strings.Join(o.Rejections, "; "), "warnings": strings.Join(o.Warnings, ","),
		}))
		if l := o.Language; l != nil {
			d := map[string]string{"row": o.RowID, "file": o.File, "customFormats": strings.Join(l.CustomFormats, ", ")}
			if l.Score != nil {
				d["score"] = strconv.Itoa(*l.Score)
			}
			if l.MinFormatScore != nil {
				d["minFormatScore"] = strconv.Itoa(*l.MinFormatScore)
			}
			out = append(out, entry("language.overridden", d))
		}
	}
	return out
}

// subtitleChoices validates the subtitle choices of a confirmation (each a subtitle file of
// the job that passed the checks, once, with a known language) and returns them as stored:
// by file id, or nil when there are none.
func (s *session) subtitleChoices(in []SubtitleChoice) (map[string]storedChoice, error) {
	if len(in) == 0 {
		return nil, nil
	}
	out := map[string]storedChoice{}
	for i, c := range in {
		f, ok := s.files[c.FileID]
		if !ok || f.Kind != store.KindSubtitle || f.Status != store.FileOK {
			return nil, badRequest("File %d is not a subtitle file of this job.", c.FileID)
		}
		key := strconv.FormatInt(c.FileID, 10)
		if _, dup := out[key]; dup {
			return nil, badRequest("File %d is listed twice.", c.FileID)
		}
		lang := strings.ToLower(strings.TrimSpace(c.Language))
		if !KnownLanguage(lang) {
			return nil, badRequest("%q is not a known language code (ISO 639-1, e.g. es).", c.Language)
		}
		in[i].Language = lang
		out[key] = storedChoice{Language: lang, Forced: c.Forced, HearingImpaired: c.HearingImpaired}
	}
	return out, nil
}
