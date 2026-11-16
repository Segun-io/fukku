//go:build probes

package probes

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/segun-io/fukku/internal/arr"
	"github.com/segun-io/fukku/internal/config"
	"github.com/segun-io/fukku/test/testenv"
)

// radarrClient is internal/arr's real client, pointed at the test Radarr (testenv has already
// refused any other address). The probes' own helpers are used only for the setup and the
// cleanup around it.
func radarrClient() arr.MovieManager {
	return arr.NewRadarr(config.Arr{
		URL:            env.Radarr.Base,
		APIKey:         config.Secret(os.Getenv("FUKKU_TEST_RADARR_KEY")),
		RequestTimeout: config.Duration(60 * time.Second),
	})
}

// TestArrClientOffline runs the client against the test Radarr for what needs no metadata
// service: status, profiles, root folders, the proposal of a staged folder, the commands.
func TestArrClientOffline(t *testing.T) {
	p := newProbe(t, "M2-client", "internal/arr client against Radarr (offline part)",
		"The arr client's Status, Profiles, RootFolders, Proposal (no title id) and Commands work against the pinned Radarr", false, "radarr")
	rc := radarrClient()

	st, err := rc.Status(p.ctx)
	p.must(err, "Status")
	p.check(st.AppName == "Radarr" && st.Version != "", fmt.Sprintf("status looks like Radarr: %+v", st))
	profiles, err := rc.Profiles(p.ctx)
	p.must(err, "Profiles")
	var fukku *arr.Profile
	for i := range profiles {
		if profiles[i].Name == "Fukku Probe" {
			fukku = &profiles[i]
		}
	}
	p.check(fukku != nil, "quality profile Fukku Probe exists (make env-up)")
	roots, err := rc.RootFolders(p.ctx)
	p.must(err, "RootFolders")
	p.check(len(roots) > 0, "Radarr has a root folder")

	dir := p.fresh(intake("M2-client"))
	src := filepath.Join(dir, bigBuckBunny.dotted()+".1080p.WEB-DL.mkv")
	p.video(src, long(bigBuckBunny.minutes, "eng"))
	rows, err := rc.Proposal(p.ctx, dir)
	p.must(err, "Proposal")
	var row *arr.Row
	for i := range rows {
		if rows[i].Path == src {
			row = &rows[i]
		}
	}
	p.check(row != nil, "the staged file is in the proposal of its folder")
	p.check(row.Size > 0 && row.Quality.Name != "" && len(row.Raw) > 0, fmt.Sprintf("the row is filled in: %+v", row.Quality))
	cmds, err := rc.Commands(p.ctx)
	p.must(err, "Commands")

	p.evidence("status", st)
	p.evidence("proposal", M{"quality": row.Quality.Name, "languages": row.Languages, "rejections": row.Rejections})
	p.observe("Radarr %s; proposal for the folder: %s, %d rejection(s); %d recent command(s)", st.Version, row.Quality.Name, len(row.Rejections), len(cmds))
	p.confirm()
}

// TestArrClientOnline adds a film through the client (the lookup needs the internet) and runs
// the plan's import flow with it: proposal for the job folder, re-check with the film,
// import by rename, the tracked file.
func TestArrClientOnline(t *testing.T) {
	p := newProbe(t, "M2-client-online", "internal/arr client against Radarr (lookup, add, re-check, import)",
		"The arr client finds a film, adds it (search off, tagged), re-checks a proposal with it, imports by rename and reports the tracked file", true, "radarr")
	p.needMeta("movie lookup")
	rc := radarrClient()
	f := bigBuckBunny

	// Start from a library without the film.
	var existing []M
	p.must(env.Radarr.Get(p.ctx, fmt.Sprintf("/movie?tmdbId=%d", f.tmdb), &existing), "find movie")
	for _, m := range existing {
		p.must(env.Radarr.Delete(p.ctx, fmt.Sprintf("/movie/%d?deleteFiles=true&addImportExclusion=false", testenv.ID(m))), "delete movie")
	}

	var found []arr.Movie
	p.meta(testenv.WaitFor(p.ctx, 4*time.Minute, 5*time.Second, "movie lookup", func() (bool, error) {
		var err error
		found, err = rc.Lookup(p.ctx, fmt.Sprintf("tmdb:%d", f.tmdb))
		return err == nil && len(found) > 0, err
	}), "lookup")
	p.check(found[0].TMDBID == f.tmdb && found[0].ID == 0, fmt.Sprintf("lookup finds the film, not in the library: %+v", found[0]))

	profiles, err := rc.Profiles(p.ctx)
	p.must(err, "Profiles")
	profile := 0
	for _, pr := range profiles {
		if pr.Name == "Fukku Probe" {
			profile = pr.ID
		}
	}
	p.check(profile != 0, "quality profile Fukku Probe exists (make env-up)")
	movie, err := rc.Add(p.ctx, arr.AddMovie{TMDBID: f.tmdb, QualityProfileID: profile, RootFolder: "/data/media/movies", Monitored: true, Tag: "Fukku-Probe"})
	p.meta(err, "Add")
	p.fresh(movie.Path)
	p.check(movie.ID > 0 && movie.File == nil, fmt.Sprintf("added without a file: %+v", movie))
	var raw M
	p.must(env.Radarr.Get(p.ctx, fmt.Sprintf("/movie/%d", movie.ID), &raw), "movie")
	p.check(len(fmt.Sprint(raw["tags"])) > 2, "the film carries the tag")

	dir := p.fresh(intake("M2-client-online"))
	src := filepath.Join(dir, f.dotted()+".1080p.WEB-DL.mkv")
	p.video(src, long(f.minutes, "eng"))
	srcInode := inode(src)
	rows, err := rc.Proposal(p.ctx, dir)
	p.must(err, "Proposal")
	p.check(len(rows) == 1 && rows[0].Path == src, "the folder's proposal has the staged file")
	rows[0].MovieID = movie.ID
	checked, err := rc.Recheck(p.ctx, rows)
	p.must(err, "Recheck")
	p.check(len(checked) == 1 && checked[0].MovieID == movie.ID, fmt.Sprintf("re-check keeps the film: %+v", checked))

	id, err := rc.Import(p.ctx, checked, "move")
	p.must(err, "Import")
	var cmd arr.Command
	p.must(testenv.WaitFor(p.ctx, cmdTimeout, 500*time.Millisecond, "import command", func() (bool, error) {
		var err error
		cmd, err = rc.Command(p.ctx, id)
		return cmd.Status != "queued" && cmd.Status != "started", err
	}), "Command")
	p.check(cmd.Status == "completed", "import completed: "+cmd.Status+" "+cmd.Message)
	all, err := rc.Commands(p.ctx)
	p.must(err, "Commands")
	listed := false
	for _, c := range all {
		listed = listed || c.ID == id
	}
	p.check(listed, "the import is in the command list")

	got, err := rc.Movie(p.ctx, movie.ID)
	p.must(err, "Movie")
	p.check(got.File != nil, "the film has a tracked file")
	sameInode := inode(got.File.Path) == srcInode
	p.evidence("movieFile", M{"quality": got.File.Quality.Name, "runtimeMs": got.File.RuntimeMs, "sameInode": sameInode, "sourceGone": !exists(src)})
	p.observe("imported %s (%s, %d ms); same inode: %v; source gone: %v", filepath.Base(got.File.Path), got.File.Quality.Name, got.File.RuntimeMs, sameInode, !exists(src))
	p.check(sameInode && !exists(src), "the import was a rename")
	p.check(got.File.RuntimeMs > 8*60*1000 && got.File.RuntimeMs < 10*60*1000, fmt.Sprintf("mediaInfo.runTime parses (~9 min): %d ms", got.File.RuntimeMs))

	busy, err := rc.Busy(p.ctx, movie.ID)
	p.must(err, "Busy")
	p.check(!busy, "nothing is downloading")
	p.must(rc.SetMonitored(p.ctx, movie.ID, false), "SetMonitored")
	got, err = rc.Movie(p.ctx, movie.ID)
	p.must(err, "Movie")
	p.check(!got.Monitored, "the film is no longer monitored")
	p.confirm()
}
