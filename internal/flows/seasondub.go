package flows

import (
	"encoding/json"
	"fmt"
	"slices"
	"strconv"
	"strings"

	"github.com/segun-io/fukku/internal/store"
)

// A dub of a season already in the library (plan §8, #57). When rows of a show's job have
// episodes that already have a file, the proposal says which of the policies apply, and the
// confirmation carries the choice (ConfirmRequest.SeasonDub), or seasonDub.policy decides
// when it is fixed. This file has replace and subtitlesOnly; version (#58) is in
// episode_version.go; merge (per episode) is not offered yet, so a fixed policy that is not
// offered leaves those rows skipped, as in M2.
//
//   - replace: the rows with an existing file are imported over it, as an override of the
//     app's "not an upgrade" refusal (probe B8-sonarr), and each season replaced is
//     unmonitored afterwards (merge.unmonitorAfterDubOnlyImport), through the season flag.
//   - subtitlesOnly: nothing is imported; the job's subtitle files go to the subtitle manager,
//     for the episodes that already have a file, and the result says so.
//   - version: the rows with an existing file are kept as second versions of their episodes,
//     next to the episode's file (episode_version.go); offered when versions.enabled is on and
//     the media server is Jellyfin 12.4 or later.
//
// What the confirmation decided is stored in the target's Options:
//
//	"seasonDub": {"policy": "replace", "seasons": [1], "rows": ["r3", "r4"]}

// Season-dub policies, as seasonDub.policy spells them.
const (
	PolicyMerge         = "merge"
	PolicyVersion       = "version"
	PolicyReplace       = "replace"
	PolicySubtitlesOnly = "subtitlesOnly"
	PolicyAsk           = "ask"
)

// CodeSeasonDubChoice is the refusal of a confirmation that does not say what to do with
// the rows whose episodes already have a file (seasonDub.policy is ask). Rows lists them.
const CodeSeasonDubChoice = "season_dub_choice"

// CodeSharedFile is the refusal of a replace that would remove the library file of an
// episode outside the job: the file holds several episodes (a double) and the job has only
// some of them. Rows lists the rows; listing one in overrides accepts it.
const CodeSharedFile = "shared_file"

// SeasonDub is the proposal's question about a dub of a season already in the library. It
// is absent when no row that is not skipped has an episode with a file.
type SeasonDub struct {
	Policy  string `json:"policy"`            // seasonDub.policy
	Ask     bool   `json:"ask"`               // the confirmation must choose one of Offers
	Decided string `json:"decided,omitempty"` // the fixed policy, when it is offered: the question is skipped
	// Offers are the policies that apply, in seasonDub.order. With none, the rows that have a
	// file are skipped.
	Offers  []string          `json:"offers"`
	Seasons []SeasonDubSeason `json:"seasons"`
	// NotOffered says why a policy of seasonDub.order is not among the offers, when Fukku
	// can tell (#58: version, with versions_disabled, versions_unsupported,
	// media_server_too_old or media_server_unknown; #116: replace or version with
	// admin_required, which an admin would be offered, auth.libraryChangesRequireAdmin).
	NotOffered []NotOffered `json:"notOffered"`
	// Version is what keeping the rows as second versions would be (#58), while "version"
	// is among the offers; absent otherwise.
	Version *EpisodeVersionsView `json:"version,omitempty"`
}

// SeasonDubSeason counts, for one season, the episodes of the rows not skipped and those
// of them that already have a file.
type SeasonDubSeason struct {
	Season   int `json:"season"`
	Episodes int `json:"episodes"`
	Existing int `json:"existing"`
	// New are the episodes without a file: whatever is chosen, they import as usual.
	New int `json:"new"`
}

// seasonDubChoice is what the confirmation stored.
type seasonDubChoice struct {
	Policy  string   `json:"policy"`
	Seasons []int    `json:"seasons,omitempty"` // the seasons with an episode replaced or given subtitles
	Rows    []string `json:"rows,omitempty"`    // the rows that have an episode with a file
	// Files is, under replace, the library file of each episode of Rows at the confirmation
	// (episode id → Sonarr's episode file id, 0: none): the import replaces those files
	// only, and refuses when one changed since (#116). Absent for a choice stored before it
	// was kept: those rows are not compared.
	Files map[string]int `json:"files,omitempty"`
}

func seasonDubOf(t store.Target) (seasonDubChoice, bool) {
	var opts struct {
		SeasonDub *seasonDubChoice `json:"seasonDub"`
	}
	if len(t.Options) > 0 {
		_ = json.Unmarshal(t.Options, &opts)
	}
	if opts.SeasonDub == nil {
		return seasonDubChoice{}, false
	}
	return *opts.SeasonDub, true
}

// SubtitlesOnlyRows returns the rows whose episode already has a file, when the job was
// confirmed with the policy subtitlesOnly: the subtitles step hands their subtitles over
// although nothing was imported. ok is false for any other job.
func SubtitlesOnlyRows(t store.Target) (rows []string, ok bool) {
	c, found := seasonDubOf(t)
	if !found || c.Policy != PolicySubtitlesOnly {
		return nil, false
	}
	return c.Rows, true
}

// LeftOutByUser reports whether the user left the row out (a skip edit), as against a skip
// the server decides.
func LeftOutByUser(r store.Row) bool {
	sk := decodeEdit(r.Edit).Skip
	return sk != nil && *sk
}

// SeasonDubResult is what the job's result says of the policy chosen, for the UI's text:
// replace (with the seasons it covered), or subtitlesOnly (nothing was imported; the user
// is pointed at the subtitle manager).
type SeasonDubResult struct {
	Policy  string `json:"policy"`
	Seasons []int  `json:"seasons"`
}

// offers are the policies that apply to the job, in seasonDub.order, without repeats, and
// those of them the user may not choose (replace and version, while only admins may change
// the library, #116), which are not among the offers.
func (s *session) offers() (out, admin []string) {
	out = []string{}
	for _, p := range s.p.cfg.SeasonDub.Order {
		if slices.Contains(out, p) || slices.Contains(admin, p) {
			continue
		}
		applies := false
		switch p {
		case PolicyReplace:
			applies = true
		case PolicySubtitlesOnly:
			applies = s.hasSubtitles()
		case PolicyVersion: // #58
			applies = s.episodeVersionsOffered()
		}
		switch {
		case !applies:
		case adminOnly(p) && !s.changes:
			admin = append(admin, p)
		default:
			out = append(out, p)
		}
	}
	return out, admin
}

// hasSubtitles: the job has an accepted subtitle file and the hand-over is configured.
func (s *session) hasSubtitles() bool {
	c := s.p.cfg.Subtitles
	if c.Provider != "bazarr" || !c.HandOverUploads {
		return false
	}
	for _, f := range s.files {
		if f.Kind == store.KindSubtitle && f.Status == store.FileOK {
			return true
		}
	}
	return false
}

// existing is what the rows that the user did not skip (and the app offered) have: the ids
// of the rows with an episode that already has a file, the seasons of those episodes, and
// per season the episodes (true: has a file).
type existing struct {
	rows     []string
	seasons  []int
	episodes map[int]map[int]bool
	// shared maps the id of a row to the episodes outside the job's rows whose library file is
	// one of the row's: replacing it would remove their file too (a double episode).
	shared map[string][]int
}

func (s *session) existing() existing {
	x := existing{episodes: map[int]map[int]bool{}, shared: map[string][]int{}}
	if !s.show() {
		return x
	}
	claimed := map[int]bool{}
	fileRows := map[int][]string{} // library file id -> rows that would replace it
	for _, r := range s.rows {
		ar, offered, err := decodeRow(r)
		if err != nil || !offered {
			continue
		}
		if sk := decodeEdit(r.Edit).Skip; sk != nil && *sk {
			continue
		}
		has := hasExistingFile(ar, s.idx)
		for _, id := range ar.EpisodeIDs {
			claimed[id] = true
			ep, ok := s.idx[id]
			if !ok {
				continue
			}
			if ep.FileID != 0 && !slices.Contains(fileRows[ep.FileID], r.ID) {
				fileRows[ep.FileID] = append(fileRows[ep.FileID], r.ID)
			}
			if x.episodes[ep.SeasonNumber] == nil {
				x.episodes[ep.SeasonNumber] = map[int]bool{}
			}
			x.episodes[ep.SeasonNumber][id] = ep.FileID != 0
			if ep.FileID != 0 && !slices.Contains(x.seasons, ep.SeasonNumber) {
				x.seasons = append(x.seasons, ep.SeasonNumber)
			}
		}
		if has {
			x.rows = append(x.rows, r.ID)
		}
	}
	slices.Sort(x.seasons)
	for _, ep := range s.eps {
		if ep.FileID == 0 || claimed[ep.ID] {
			continue
		}
		for _, row := range fileRows[ep.FileID] {
			x.shared[row] = append(x.shared[row], ep.ID)
		}
	}
	return x
}

// seasonDub builds the proposal's question; nil when there is none.
func (s *session) seasonDub() *SeasonDub {
	x := s.existing()
	if len(x.rows) == 0 {
		return nil
	}
	policy := s.p.cfg.SeasonDub.Policy
	if policy == "" {
		policy = PolicyAsk
	}
	offers, admin := s.offers()
	v := &SeasonDub{Policy: policy, Offers: offers, Seasons: []SeasonDubSeason{}, NotOffered: []NotOffered{}}
	for _, p := range admin {
		v.NotOffered = append(v.NotOffered, NotOffered{Offer: p, Reason: ReasonAdminRequired})
	}
	if s.epvNot != nil && slices.Contains(s.p.cfg.SeasonDub.Order, PolicyVersion) {
		v.NotOffered = append(v.NotOffered, *s.epvNot)
	}
	switch {
	case policy == PolicyAsk:
		v.Ask = len(v.Offers) > 0
	case slices.Contains(v.Offers, policy):
		v.Decided = policy
	}
	seasons := make([]int, 0, len(x.episodes))
	for n := range x.episodes {
		seasons = append(seasons, n)
	}
	slices.Sort(seasons)
	for _, n := range seasons {
		c := SeasonDubSeason{Season: n, Episodes: len(x.episodes[n])}
		for _, has := range x.episodes[n] {
			if has {
				c.Existing++
			}
		}
		c.New = c.Episodes - c.Existing
		v.Seasons = append(v.Seasons, c)
	}
	return v
}

// chooseDub settles what the confirmation does with the rows that have a file: the fixed
// policy, or what req says when the policy is ask. An empty policy means nothing is decided
// (the rows stay skipped); the existing rows and seasons come with a decision.
func (s *session) chooseDub(req string) (string, existing, error) {
	req = strings.TrimSpace(req)
	v := s.seasonDub()
	if v == nil {
		if req != "" {
			return "", existing{}, badRequest("No row has an episode that already has a file: there is nothing to choose.")
		}
		return "", existing{}, nil
	}
	x := s.existing()
	if adminOnly(req) && !s.changes { // #116
		return "", existing{}, adminRequired("Only an admin can choose %s for episodes that already have a file: ask an admin to confirm this upload, or choose another option.", req)
	}
	switch {
	case v.Decided != "":
		if req != "" && req != v.Decided {
			return "", existing{}, badRequest("seasonDub.policy is %s: the choice %q is not accepted.", v.Decided, req)
		}
		return v.Decided, x, nil
	case v.Ask:
		if req == "" {
			err := refuse(CodeSeasonDubChoice, "Some episodes already have a file: choose one of %s, or skip those rows.", strings.Join(v.Offers, ", "))
			err.Rows = x.rows
			return "", existing{}, err
		}
		if !slices.Contains(v.Offers, req) {
			return "", existing{}, badRequest("%q does not apply here; choose one of %s.", req, strings.Join(v.Offers, ", "))
		}
		return req, x, nil
	case req != "":
		return "", existing{}, badRequest("Nothing is offered for the rows that already have a file.")
	}
	return "", existing{}, nil
}

// replacedFiles returns the library file of each episode of rows, as Sonarr said when the
// proposal was judged (episode id → episode file id; 0: none).
func (s *session) replacedFiles(rows []string) map[string]int {
	out := map[string]int{}
	for _, r := range s.rows {
		if !slices.Contains(rows, r.ID) {
			continue
		}
		ar, _, err := decodeRow(r)
		if err != nil {
			continue
		}
		for _, id := range ar.EpisodeIDs {
			out[strconv.Itoa(id)] = s.idx[id].FileID
		}
	}
	return out
}

// replaceCovers reports whether a rejection of a row replaced by the policy is one the
// choice answers: the app's refusal to replace a file it has ("not an upgrade", or that the
// existing file is as good), as against any other reason, which still needs an override.
func replaceCovers(reason string) bool {
	r := strings.ToLower(strings.TrimSpace(reason))
	for _, prefix := range upgradeRefusals {
		if strings.HasPrefix(r, prefix) {
			return true
		}
	}
	return false
}

// upgradeRefusals are the app's refusals to replace a file it has (probes B8-sonarr and
// B8-radarr), lower case. "Has the same filesize as existing file" and "...does not allow
// upgrades" are other reasons and are not answered by the choice.
var upgradeRefusals = []string{
	"not an upgrade for existing",
	"not a custom format upgrade for existing",
	"not a quality revision upgrade",
}

// seasonsText is the audit text of seasons: "S01, S02".
func seasonsText(seasons []int) string {
	parts := make([]string, 0, len(seasons))
	for _, n := range seasons {
		parts = append(parts, fmt.Sprintf("S%02d", n))
	}
	return strings.Join(parts, ", ")
}
