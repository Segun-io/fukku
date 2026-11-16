package flows

import (
	"context"
	"slices"

	"github.com/segun-io/fukku/internal/arr"
)

// The language warning (#39, decision #21, plan section 7 B7). Neither app rejects a dubbed
// file: Sonarr imports a dub scored -10000 against a minimum of 0 (probes B7-sonarr, C8).
// Fukku reads the title's quality profile itself and warns on a row whose custom-format
// score is below the profile's minFormatScore, with the matching custom formats and the
// score. The warning makes the row need the same explicit override as a rejection (it is in
// Warnings, so it is not Importable). The same rule runs for films. match.languageWarning
// turns it off.

// languageRule is the rule for one pass over a job's rows. The profile is read once, on the
// first row that needs it.
type languageRule struct {
	s      *session
	off    bool
	loaded bool
	min    int
	known  bool // the title's profile was found
}

func (s *session) languageRule() *languageRule {
	return &languageRule{s: s, off: !s.p.cfg.Match.LanguageWarning}
}

// check returns the warning for row, or nil. An app that does not answer is an arr_unavailable
// failure, like the other reads of the view.
func (r *languageRule) check(ctx context.Context, row arr.Row) (*Notice, error) {
	if r.off {
		return nil, nil
	}
	if !r.loaded {
		if err := r.load(ctx); err != nil {
			return nil, err
		}
	}
	if !r.known || row.CustomFormatScore >= r.min {
		return nil, nil
	}
	score, minScore := row.CustomFormatScore, r.min
	return &Notice{
		Code:           WarnLanguage,
		CustomFormats:  append([]string{}, row.CustomFormats...),
		Score:          &score,
		MinFormatScore: &minScore,
	}, nil
}

func (r *languageRule) load(ctx context.Context) error {
	t := r.s.t
	var profileID int
	if r.s.show() {
		sr, err := r.s.p.sonarr.Series(ctx, t.TitleID)
		if err != nil {
			return appFailure(t.App, err)
		}
		profileID = sr.QualityProfileID
	} else {
		m, err := r.s.p.radarr.Movie(ctx, t.TitleID)
		if err != nil {
			return appFailure(t.App, err)
		}
		profileID = m.QualityProfileID
	}
	profiles, err := r.s.imp.Profiles(ctx)
	if err != nil {
		return appFailure(t.App, err)
	}
	r.loaded = true
	if i := slices.IndexFunc(profiles, func(p arr.Profile) bool { return p.ID == profileID }); i >= 0 {
		r.min, r.known = profiles[i].MinFormatScore, true
	} else {
		r.s.p.log.Warn("language warning skipped: the title's quality profile is unknown", "app", t.App, "profileId", profileID)
	}
	return nil
}
