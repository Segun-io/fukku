package flows

import (
	"cmp"
	"encoding/json"
	"fmt"
	"path"
	"regexp"
	"slices"
	"strconv"
	"strings"

	"github.com/segun-io/fukku/internal/arr"
	"github.com/segun-io/fukku/internal/media"
	"github.com/segun-io/fukku/internal/store"
)

// ---- episodes -------------------------------------------------------------------------

type epRef struct{ season, number int }

func episodeIndex(eps []arr.Episode) map[int]arr.Episode {
	idx := make(map[int]arr.Episode, len(eps))
	for _, e := range eps {
		idx[e.ID] = e
	}
	return idx
}

// lookupEpisodes finds the series' episodes for refs, all in one season, ordered by number.
func lookupEpisodes(refs []epRef, eps []arr.Episode) (ids []int, ok bool) {
	if len(refs) == 0 {
		return nil, false
	}
	for _, r := range refs {
		if r.season != refs[0].season {
			return nil, false
		}
		i := slices.IndexFunc(eps, func(e arr.Episode) bool { return e.SeasonNumber == r.season && e.Number == r.number })
		if i < 0 {
			return nil, false
		}
		if !slices.Contains(ids, eps[i].ID) {
			ids = append(ids, eps[i].ID)
		}
	}
	return ids, true
}

// sortEpisodes orders episode ids by season and number; ids it does not know go last.
func sortEpisodes(ids []int, idx map[int]arr.Episode) {
	slices.SortStableFunc(ids, func(a, b int) int {
		ea, oka := idx[a]
		eb, okb := idx[b]
		switch {
		case oka && okb:
			return cmp.Or(cmp.Compare(ea.SeasonNumber, eb.SeasonNumber), cmp.Compare(ea.Number, eb.Number))
		case oka:
			return -1
		case okb:
			return 1
		}
		return 0
	})
}

// assignEpisodes gives a proposal item of a show the target series and its episodes:
//   - the app matched the file to this series already (by its name): its episodes stay;
//   - else the season and numbers the app parsed (an item matched to another series still
//     carries them), else those in the file name (SxxEyy, NxNN), on this series;
//   - else none: the user picks.
func assignEpisodes(it arr.Row, seriesID int, eps []arr.Episode, name string) arr.Row {
	idx := episodeIndex(eps)
	if it.SeriesID == seriesID && len(it.EpisodeIDs) > 0 {
		if season, ok := oneSeason(it.EpisodeIDs, idx); ok {
			it.SeasonNumber = season
			sortEpisodes(it.EpisodeIDs, idx)
			return it
		}
	}
	it.SeriesID = seriesID
	refs := rawEpisodes(it.Raw)
	if len(refs) == 0 {
		refs = parseEpisodes(name)
	}
	if ids, ok := lookupEpisodes(refs, eps); ok {
		it.EpisodeIDs, it.SeasonNumber = ids, refs[0].season
		return it
	}
	it.EpisodeIDs, it.SeasonNumber = nil, 0
	if len(refs) > 0 {
		it.SeasonNumber = refs[0].season
	}
	it.Raw = withoutKeys(it.Raw, "episodes", "episodeIds")
	return it
}

// oneSeason reports the season of episodes ids, when they are all known and in one.
func oneSeason(ids []int, idx map[int]arr.Episode) (int, bool) {
	season := -1
	for _, id := range ids {
		e, ok := idx[id]
		if !ok || (season >= 0 && e.SeasonNumber != season) {
			return 0, false
		}
		season = e.SeasonNumber
	}
	return season, season >= 0
}

// rawEpisodes reads the season and episode numbers of the episodes the app put on an item.
func rawEpisodes(raw json.RawMessage) []epRef {
	var item struct {
		Episodes []struct {
			SeasonNumber  int `json:"seasonNumber"`
			EpisodeNumber int `json:"episodeNumber"`
		} `json:"episodes"`
	}
	if len(raw) == 0 || json.Unmarshal(raw, &item) != nil {
		return nil
	}
	var refs []epRef
	for _, e := range item.Episodes {
		if e.EpisodeNumber > 0 {
			refs = append(refs, epRef{e.SeasonNumber, e.EpisodeNumber})
		}
	}
	return refs
}

var (
	seasonEpisodeRE = regexp.MustCompile(`(?i)(?:^|[^a-z0-9])s(\d{1,3})((?:[ ._-]*e\d{1,4})+)`)
	episodeNumRE    = regexp.MustCompile(`(?i)e(\d{1,4})`)
	crossRE         = regexp.MustCompile(`(?i)(?:^|[^a-z0-9])(\d{1,2})x(\d{2,3})(?:[^a-z0-9]|$)`)
)

// parseEpisodes reads the season and episodes from a file name: "S01E02", "s01e02e03",
// "S01E02-E03", "1x02". Sonarr gives no season or episodes for a file it could not match to
// a series, so this is the fallback for those files.
func parseEpisodes(name string) []epRef {
	name = strings.TrimSuffix(name, path.Ext(name))
	if m := seasonEpisodeRE.FindStringSubmatch(name); m != nil {
		season, _ := strconv.Atoi(m[1])
		var refs []epRef
		for _, e := range episodeNumRE.FindAllStringSubmatch(m[2], -1) {
			n, _ := strconv.Atoi(e[1])
			refs = append(refs, epRef{season, n})
		}
		return refs
	}
	if m := crossRE.FindStringSubmatch(name); m != nil {
		season, _ := strconv.Atoi(m[1])
		n, _ := strconv.Atoi(m[2])
		return []epRef{{season, n}}
	}
	return nil
}

// hasExistingFile reports whether an episode of the row already has a file.
func hasExistingFile(r arr.Row, idx map[int]arr.Episode) bool {
	for _, id := range r.EpisodeIDs {
		if idx[id].FileID != 0 {
			return true
		}
	}
	return false
}

// ---- quality --------------------------------------------------------------------------

func unknownQuality(q arr.Quality) bool { return q.ID == 0 || strings.EqualFold(q.Name, "unknown") }

func probeOf(f store.File) media.Probe {
	var pr media.Probe
	if len(f.Report) > 0 {
		_ = json.Unmarshal(f.Report, &pr)
	}
	return pr
}

// resolution is the probe's video resolution class (2160, 1080, 720, 480), or 0. Width
// counts too: a 1920×800 scope film is 1080p.
func resolution(pr media.Probe) int {
	if len(pr.Video) == 0 {
		return 0
	}
	w, h := pr.Video[0].Width, pr.Video[0].Height
	switch {
	case h >= 1600 || w >= 3200:
		return 2160
	case h >= 900 || w >= 1700:
		return 1080
	case h >= 600 || w >= 1100:
		return 720
	case h > 0:
		return 480
	}
	return 0
}

// defaultQuality is the quality for a film the app found none for (flow 1 step 3): the
// probe's resolution and match.defaultSource, named as both apps name their qualities.
func defaultQuality(pr media.Probe, source string, quals []arr.Quality) (arr.Quality, bool) {
	res := resolution(pr)
	if res == 0 {
		return arr.Quality{}, false
	}
	var name string
	switch strings.ToUpper(source) {
	case "WEBDL":
		name = fmt.Sprintf("WEBDL-%dp", res)
	case "WEBRIP":
		name = fmt.Sprintf("WEBRip-%dp", res)
	case "BLURAY":
		name = fmt.Sprintf("Bluray-%dp", res)
	case "HDTV":
		name = fmt.Sprintf("HDTV-%dp", res)
		if res == 480 {
			name = "SDTV"
		}
	case "DVD":
		name = "DVD"
	}
	for _, q := range quals {
		if strings.EqualFold(q.Name, name) {
			return q, true
		}
	}
	return arr.Quality{}, false
}

// ---- re-check answers -----------------------------------------------------------------

// mergeAnswers pairs each row sent to Recheck with the app's answer for it and merges the
// two. The paths sent are Fukku's own (derived from the job's files); the answer is
// trusted for its verdict only. Answers that carry paths are paired by path, and every one
// must be a path that was sent, once, with an answer for every row; only an answer that
// carries no path at all is paired by position, and then it must have one item per row.
func mergeAnswers(sent, fresh []arr.Row) ([]arr.Row, error) {
	if len(fresh) != len(sent) {
		return nil, fmt.Errorf("the answer has %d item(s) for %d row(s)", len(fresh), len(sent))
	}
	withPaths := slices.ContainsFunc(fresh, func(f arr.Row) bool { return f.Path != "" })
	out := make([]arr.Row, len(sent))
	if !withPaths {
		for i := range sent {
			out[i] = mergeRow(sent[i], fresh[i])
		}
		return out, nil
	}
	index := map[string]int{}
	for i, s := range sent {
		index[path.Clean(s.Path)] = i
	}
	done := make([]bool, len(sent))
	for _, f := range fresh {
		i, ok := index[path.Clean(f.Path)]
		if f.Path == "" || !ok {
			return nil, fmt.Errorf("the answer has an item for %q, which was not sent", f.Path)
		}
		if done[i] {
			return nil, fmt.Errorf("the answer has two items for %q", f.Path)
		}
		done[i] = true
		out[i] = mergeRow(sent[i], f)
	}
	return out, nil
}

// mergeRow is the app's fresh verdict on a row, keeping what the answer leaves out (a
// Sonarr re-check answers without size or name, and without the release type of the
// proposal, #35's probe) and always the path that was sent: the item's path too.
func mergeRow(prev, fresh arr.Row) arr.Row {
	out := fresh
	out.Path = prev.Path
	if out.Size == 0 {
		out.Size = prev.Size
	}
	out.Raw = overlay(prev.Raw, fresh.Raw)
	if p, err := json.Marshal(prev.Path); err == nil {
		out.Raw = overlay(out.Raw, json.RawMessage(`{"path":`+string(p)+`}`))
	}
	return out
}

// overlay writes top's keys over base's. Either one not an object: top (else base).
func overlay(base, top json.RawMessage) json.RawMessage {
	if len(top) == 0 {
		return base
	}
	var b, t map[string]json.RawMessage
	if json.Unmarshal(base, &b) != nil || b == nil || json.Unmarshal(top, &t) != nil || t == nil {
		return top
	}
	for k, v := range t {
		b[k] = v
	}
	data, err := json.Marshal(b)
	if err != nil {
		return top
	}
	return data
}

// withoutKeys drops keys from a JSON object, so that a stale value cannot reach the app.
func withoutKeys(raw json.RawMessage, keys ...string) json.RawMessage {
	var obj map[string]json.RawMessage
	if len(raw) == 0 || json.Unmarshal(raw, &obj) != nil || obj == nil {
		return raw
	}
	for _, k := range keys {
		delete(obj, k)
	}
	data, err := json.Marshal(obj)
	if err != nil {
		return raw
	}
	return data
}

// decodeRow returns the app's verdict stored on a row; false: the app did not offer the file.
func decodeRow(r store.Row) (arr.Row, bool, error) {
	if len(r.Proposal) == 0 || string(r.Proposal) == "null" {
		return arr.Row{}, false, nil
	}
	var ar arr.Row
	if err := json.Unmarshal(r.Proposal, &ar); err != nil {
		return arr.Row{}, false, fmt.Errorf("row %s: stored proposal: %w", r.ID, err)
	}
	return ar, true, nil
}
