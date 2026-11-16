//go:build probes

package probes

import (
	"fmt"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/segun-io/fukku/test/testenv"
)

type M = testenv.M

const cmdTimeout = 3 * time.Minute

// Probe titles: Blender open films (TMDB) and a Creative Commons web series (TVDB).
var (
	bigBuckBunny     = film{tmdb: 10378, title: "Big Buck Bunny", year: 2008, minutes: 9}
	sintel           = film{tmdb: 45745, title: "Sintel", year: 2010, minutes: 14}
	tearsOfSteel     = film{tmdb: 133701, title: "Tears of Steel", year: 2012, minutes: 12}
	elephantsDream   = film{tmdb: 9761, title: "Elephants Dream", year: 2006, minutes: 11}
	cosmosLaundromat = film{tmdb: 358332, title: "Cosmos Laundromat", year: 2015, minutes: 12}
	pioneerOne       = show{tvdb: 170551, title: "Pioneer One", minutes: 30}
)

type film struct {
	tmdb    int
	title   string
	year    int
	minutes int
}

// dotted is a scene-style release name prefix, e.g. Big.Buck.Bunny.2008
func (f film) dotted() string {
	return strings.ReplaceAll(f.title, " ", ".") + fmt.Sprintf(".%d", f.year)
}

type show struct {
	tvdb    int
	title   string
	minutes int
}

func profileID(p *probe, a *testenv.Arr) int {
	prof, err := a.FindByName(p.ctx, "/qualityprofile", "Fukku Probe")
	p.must(err, "quality profile")
	p.check(prof != nil, "quality profile Fukku Probe exists (make env-up)")
	return testenv.ID(prof)
}

// addFilm adds the film fresh (deleting any earlier copy and its files) with search off.
func addFilm(p *probe, f film) M {
	r := env.Radarr
	p.needMeta("movie lookup")
	var existing []M
	p.must(r.Get(p.ctx, fmt.Sprintf("/movie?tmdbId=%d", f.tmdb), &existing), "find movie")
	for _, m := range existing {
		p.must(r.Delete(p.ctx, fmt.Sprintf("/movie/%d?deleteFiles=true&addImportExclusion=false", testenv.ID(m))), "delete movie")
	}
	var lookup M
	// The lookup goes through Radarr to its public metadata service, which can be slow.
	p.meta(testenv.WaitFor(p.ctx, 4*time.Minute, 5*time.Second, "movie lookup", func() (bool, error) {
		err := r.Get(p.ctx, fmt.Sprintf("/movie/lookup/tmdb?tmdbId=%d", f.tmdb), &lookup)
		return err == nil, err
	}), "lookup")
	lookup["qualityProfileId"] = profileID(p, r)
	lookup["rootFolderPath"] = "/data/media/movies"
	lookup["monitored"] = true
	lookup["minimumAvailability"] = "released"
	lookup["addOptions"] = M{"searchForMovie": false, "monitor": "movieOnly"}
	var movie M
	p.meta(r.Post(p.ctx, "/movie", lookup, &movie), "add movie")
	p.fresh(movie["path"].(string))
	return movie
}

// proposal is GET /manualimport for a folder. With movieId (Radarr) the folder is ignored and
// the film's own folder is scanned instead (probe C6), so pass a scope only for that case.
func proposal(p *probe, a *testenv.Arr, folder string, scope ...string) []M {
	q := append([]string{"folder", folder, "filterExistingFiles", "false"}, scope...)
	var items []M
	p.must(a.Get(p.ctx, "/manualimport"+testenv.Q(q...), &items), "manual import proposal")
	return items
}

func itemFor(items []M, path string) M {
	for _, it := range items {
		if it["path"] == path {
			return it
		}
	}
	return nil
}

func rejections(item M) []string {
	var out []string
	if item == nil {
		return []string{"(file not in the proposal)"}
	}
	raw, _ := item["rejections"].([]any)
	for _, r := range raw {
		if rm, ok := r.(M); ok {
			out = append(out, fmt.Sprint(rm["reason"]))
		}
	}
	return out
}

func qualityName(item M) string {
	if q, ok := item["quality"].(M); ok {
		if qq, ok := q["quality"].(M); ok {
			return fmt.Sprint(qq["name"])
		}
	}
	return "?"
}

func languageNames(item M) []string {
	var out []string
	raw, _ := item["languages"].([]any)
	for _, l := range raw {
		if lm, ok := l.(M); ok {
			out = append(out, fmt.Sprint(lm["name"]))
		}
	}
	return out
}

func formatNames(item M) []string {
	var out []string
	raw, _ := item["customFormats"].([]any)
	for _, f := range raw {
		if fm, ok := f.(M); ok {
			out = append(out, fmt.Sprint(fm["name"]))
		}
	}
	return out
}

// summary is the part of a proposal item worth recording.
func summary(item M) M {
	if item == nil {
		return M{"missing": true}
	}
	return M{
		"file": filepath.Base(fmt.Sprint(item["path"])), "quality": qualityName(item), "languages": languageNames(item),
		"customFormats": formatNames(item), "customFormatScore": item["customFormatScore"], "rejections": rejections(item),
	}
}

// reprocess re-checks proposal items with fields set (POST /manualimport), e.g. movieId.
func reprocess(p *probe, a *testenv.Arr, items []M, set M) []M {
	var edited []M
	for _, it := range items {
		e := M{}
		for k, v := range it {
			e[k] = v
		}
		for k, v := range set {
			e[k] = v
		}
		edited = append(edited, e)
	}
	var out []M
	p.must(a.Post(p.ctx, "/manualimport", edited, &out), "re-check proposal")
	return out
}

// proposalForMovie is the flow Fukku uses: the job folder's proposal, then each row re-checked
// with the chosen film.
func proposalForMovie(p *probe, folder string, movieID int) []M {
	items := proposal(p, env.Radarr, folder)
	if len(items) == 0 {
		return nil
	}
	return reprocess(p, env.Radarr, items, M{"movieId": movieID})
}

// manualImport runs the ManualImport command for proposal items, adding title ids.
func manualImport(p *probe, a *testenv.Arr, items []M, extra M, mode string) testenv.Command {
	var files []M
	for _, it := range items {
		f := M{"path": it["path"], "quality": it["quality"], "languages": it["languages"],
			"releaseGroup": it["releaseGroup"], "indexerFlags": it["indexerFlags"]}
		for k, v := range extra {
			f[k] = v
		}
		files = append(files, f)
	}
	cmd, err := a.RunCommand(p.ctx, M{"name": "ManualImport", "importMode": mode, "files": files}, cmdTimeout)
	p.must(err, "ManualImport command")
	return cmd
}

// importFilm stages a generated file and imports it for the film, returning the movie file.
func importFilm(p *probe, movie M, f film, release string, langs ...string) M {
	dir := p.fresh(intake(fmt.Sprintf("%s-%d", p.r.ID, f.tmdb)))
	src := filepath.Join(dir, release+".mkv")
	p.video(src, long(f.minutes, langs...))
	id := testenv.ID(movie)
	item := itemFor(proposalForMovie(p, dir, id), src)
	p.check(item != nil, "staged file is in the proposal")
	cmd := manualImport(p, env.Radarr, []M{item}, M{"movieId": id}, "move")
	p.check(cmd.Status == "completed", "setup import completed: "+cmd.Status+" "+cmd.Message)
	mf := movieFile(p, id)
	p.check(mf != nil, "setup import produced a movie file")
	return mf
}

// movieFile returns the film's tracked file (GET /movie/<id>.movieFile). /moviefile?movieId=
// can list more than one record, so its first entry is not necessarily the tracked file.
func movieFile(p *probe, movieID int) M {
	var m M
	p.must(env.Radarr.Get(p.ctx, fmt.Sprintf("/movie/%d", movieID), &m), "movie")
	mf, _ := m["movieFile"].(M)
	return mf
}

// movieFileRecords lists every /moviefile record of the film.
func movieFileRecords(p *probe, movieID int) []string {
	var files []M
	p.must(env.Radarr.Get(p.ctx, fmt.Sprintf("/moviefile?movieId=%d", movieID), &files), "movie files")
	var out []string
	for _, f := range files {
		out = append(out, fmt.Sprintf("%v (%s)", f["relativePath"], qualityName(f)))
	}
	return out
}

func rescanMovie(p *probe, movieID int) {
	cmd, err := env.Radarr.RunCommand(p.ctx, M{"name": "RescanMovie", "movieId": movieID}, cmdTimeout)
	p.must(err, "RescanMovie")
	p.check(cmd.Status == "completed", "RescanMovie completed")
}

func hasRejection(rs []string, words ...string) bool {
	return slices.ContainsFunc(rs, func(r string) bool {
		l := strings.ToLower(r)
		for _, w := range words {
			if strings.Contains(l, w) {
				return true
			}
		}
		return false
	})
}

// setMovieNaming sets Radarr's film file format and returns a function that restores it.
func setMovieNaming(p *probe, format string) func() {
	var naming M
	p.must(env.Radarr.Get(p.ctx, "/config/naming", &naming), "naming")
	old := naming["standardMovieFormat"]
	naming["standardMovieFormat"] = format
	p.must(env.Radarr.Put(p.ctx, "/config/naming", naming, nil), "set naming")
	return func() {
		naming["standardMovieFormat"] = old
		_ = env.Radarr.Put(p.ctx, "/config/naming", naming, nil)
	}
}
