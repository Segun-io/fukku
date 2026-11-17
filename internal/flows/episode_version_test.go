package flows

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/segun-io/fukku/internal/arr"
	"github.com/segun-io/fukku/internal/config"
	"github.com/segun-io/fukku/internal/media"
	"github.com/segun-io/fukku/internal/store"
)

// Second versions of episodes (#58): the season-dub policy version keeps a dub of episodes
// that already have a file next to that file, once Sonarr rejects it, under its version
// name, as not an upgrade.

// epvEnv is dubEnv with versions on: Jellyfin of version jellyfin as the media server, a
// library root on the intake's filesystem with Sonarr's season folder in it (Sonarr sees it as
// /tv), and S01E01 and S01E02 with a file there.
type epvEnv struct {
	*ienv
	lib, season string
	jellyfin    string
	jfErr       error
}

const appTV = "/tv"

func newEpvEnv(t *testing.T, mutate func(*config.Config)) *epvEnv {
	t.Helper()
	lib := t.TempDir()
	x := &epvEnv{jellyfin: "12.4.0", lib: lib}
	x.ienv = dubEnv(t, func(c *config.Config) {
		c.Versions.Enabled, c.Versions.Root = true, filepath.Join(t.TempDir(), "versions")
		c.MediaServer.Provider = "jellyfin"
		c.Paths.LibraryRoots = []string{lib}
		c.Paths.Mappings.Sonarr = append(c.Paths.Mappings.Sonarr, config.PathMapping{From: lib, To: appTV})
		if mutate != nil {
			mutate(c)
		}
	}, 101, 102)
	x.p.SetMediaServerVersion(func(context.Context) (string, error) { return x.jellyfin, x.jfErr })
	x.season = filepath.Join(lib, "Some Show", "Season 01")
	if err := os.MkdirAll(x.season, 0o755); err != nil {
		t.Fatal(err)
	}
	x.series.Library[0].Path = appTV + "/Some Show"
	x.series.Files = map[int][]arr.EpisodeFile{showID: {}}
	for _, ep := range x.series.Eps[showID] {
		if ep.FileID == 0 {
			continue
		}
		name := "Some Show - S01E0" + string(rune('0'+ep.Number)) + " - " + ep.Title + " WEBDL-1080p.mkv"
		if err := os.WriteFile(filepath.Join(x.season, name), []byte("the library's own"), 0o644); err != nil {
			t.Fatal(err)
		}
		x.series.Files[showID] = append(x.series.Files[showID], arr.EpisodeFile{ID: ep.FileID, Path: appTV + "/Some Show/Season 01/" + name, Size: 17})
	}
	return x
}

// spanishAudio gives the job's videos a Spanish audio track, so the label is proposed.
func (x *epvEnv) spanishAudio(j store.Job) {
	x.t.Helper()
	files, err := x.st.Files(x.ctx, j.ID)
	if err != nil {
		x.t.Fatal(err)
	}
	for _, f := range files {
		var pr media.Probe
		_ = json.Unmarshal(f.Report, &pr)
		pr.Audio = []media.AudioTrack{{Index: 1, Codec: "ac3", Language: "spa", Default: true}}
		f.Report, _ = json.Marshal(pr)
		if err := x.st.UpdateFile(x.ctx, f); err != nil {
			x.t.Fatal(err)
		}
	}
}

// epvJob is dubJob (three files, S01E01 and S01E02 with a file) with Spanish audio, and
// Sonarr's answer for each staged version: rejected as not an upgrade unless winning lists
// its episode number.
func (x *epvEnv) epvJob(winning ...int) store.Job {
	x.t.Helper()
	j := x.dubJob()
	x.spanishAudio(j)
	for _, r := range x.rows(j) {
		ar, _, _ := decodeRow(r)
		if len(ar.EpisodeIDs) != 1 || ar.EpisodeIDs[0] > 102 {
			continue
		}
		n := ar.EpisodeIDs[0] - 100
		x.sonarrSays(j, r.ID, "Some Show - S01E0"+string(rune('0'+n))+" - Español.mkv", ar.EpisodeIDs[0], !slices.Contains(winning, n))
	}
	return x.jobNow(j)
}

// sonarrSays scripts Sonarr's proposal and re-check for the staged version of row: matched
// to the episode, with a "not an upgrade" rejection when loses.
func (x *epvEnv) sonarrSays(j store.Job, row, name string, episodeID int, loses bool) {
	folder := appIntake + "/jobs/" + j.ID + "/versions/" + row
	p := folder + "/" + name
	x.series.Proposed[folder] = []arr.Row{{Path: p, Size: 1000, SeriesID: showID, EpisodeIDs: []int{episodeID}, SeasonNumber: 1,
		Quality: arr.Quality{ID: 9, Name: "HDTV-1080p"}}}
	if x.series.Verdicts == nil {
		x.series.Verdicts = map[string][]arr.Rejection{}
	}
	x.series.Verdicts[p] = nil
	if loses {
		x.series.Verdicts[p] = []arr.Rejection{{Reason: "Not an upgrade for existing episode file(s). Existing quality: WEBDL-1080p. New Quality HDTV-1080p.", Permanent: true}}
	}
}

func (x *epvEnv) confirmEpv(j store.Job, label string) (Confirmation, error) {
	req := ConfirmRequest{SeasonDub: PolicyVersion}
	if label != "" {
		req.Version = &VersionRequest{Label: label}
	}
	return x.eng.Confirm(x.ctx, "user1", "user1@example.com", true, j, req)
}

func (x *epvEnv) libraryNames() []string {
	x.t.Helper()
	entries, err := os.ReadDir(x.season)
	if err != nil {
		x.t.Fatal(err)
	}
	var out []string
	for _, e := range entries {
		out = append(out, e.Name())
	}
	return out
}

func (x *epvEnv) actions(j store.Job) []string {
	x.t.Helper()
	log, err := x.st.AuditLog(x.ctx, j.ID)
	if err != nil {
		x.t.Fatal(err)
	}
	var out []string
	for _, e := range log {
		out = append(out, e.Action)
	}
	return out
}

func sameInode(t *testing.T, a, b string) bool {
	t.Helper()
	ia, err := os.Stat(a)
	if err != nil {
		t.Fatal(err)
	}
	ib, err := os.Stat(b)
	if err != nil {
		return false
	}
	return os.SameFile(ia, ib)
}

func TestEpisodeVersionsAreOfferedWithJellyfin124(t *testing.T) {
	for _, c := range []struct {
		name     string
		mutate   func(*config.Config)
		jellyfin string
		jfErr    error
		hook     bool
		offered  bool
		reason   string
	}{
		{"jellyfin 12.4", nil, "12.4.0", nil, true, true, ""},
		{"a later jellyfin", nil, "13.0.1", nil, true, true, ""},
		{"jellyfin before 12.4", nil, "12.3.9", nil, true, false, ReasonMediaServerTooOld},
		{"an older line", nil, "10.11.2", nil, true, false, ReasonMediaServerTooOld},
		{"a version that does not parse", nil, "nightly", nil, true, false, ReasonMediaServerUnknown},
		{"the server does not answer", nil, "", errors.New("connection refused"), true, false, ReasonMediaServerUnknown},
		{"versions off", func(c *config.Config) { c.Versions.Enabled = false }, "12.4.0", nil, true, false, ReasonVersionsDisabled},
		{"no media server", func(c *config.Config) { c.MediaServer.Provider = "none" }, "12.4.0", nil, true, false, ReasonVersionsUnsupported},
		{"no version call", nil, "12.4.0", nil, false, false, ReasonVersionsUnsupported},
	} {
		t.Run(c.name, func(t *testing.T) {
			x := newEpvEnv(t, c.mutate)
			x.jellyfin, x.jfErr = c.jellyfin, c.jfErr
			if !c.hook {
				x.p.msVersion = nil
			}
			sd := x.view(x.epvJob()).SeasonDub
			if sd == nil {
				t.Fatal("no question")
			}
			if got := slices.Contains(sd.Offers, PolicyVersion); got != c.offered {
				t.Fatalf("offers %v, version offered %v, want %v", sd.Offers, got, c.offered)
			}
			if c.offered {
				if sd.Offers[0] != PolicyVersion || len(sd.NotOffered) != 0 || sd.Version == nil {
					t.Fatalf("question %+v", sd)
				}
				return
			}
			if sd.Version != nil || !slices.Equal(sd.NotOffered, []NotOffered{{Offer: PolicyVersion, Reason: c.reason}}) {
				t.Fatalf("not offered %+v, version %+v, want %s", sd.NotOffered, sd.Version, c.reason)
			}
			_, err := x.confirmEpv(x.jobNow(x.epvJob()), "")
			_ = wantRefusal(t, err, CodeBadRequest)
		})
	}
}

// The answer of the media server is reused for a while, so a view does not ask every time.
func TestEpisodeVersionsAskTheMediaServerOnce(t *testing.T) {
	x := newEpvEnv(t, nil)
	calls := 0
	x.p.SetMediaServerVersion(func(context.Context) (string, error) { calls++; return "12.4.0", nil })
	j := x.epvJob()
	for range 3 {
		_ = x.view(j)
	}
	if calls != 1 {
		t.Errorf("the version was asked %d times", calls)
	}
	x.clock.advance(serverVersionTTL)
	_ = x.view(j)
	if calls != 2 {
		t.Errorf("after the cache's lifetime: %d calls, want 2", calls)
	}
}

func TestEpisodeVersionsView(t *testing.T) {
	x := newEpvEnv(t, nil)
	j := x.epvJob()
	rows := x.rows(j)
	v := x.view(j).SeasonDub.Version
	if v == nil || v.Label != "Español" || v.Language != "es" || v.Problem != nil {
		t.Fatalf("view %+v", v)
	}
	want := []EpisodeVersionRow{
		{Row: rows[0].ID, EpisodeID: 101, SeasonNumber: 1, EpisodeNumber: 1, Name: "Some Show - S01E01 - Español.mkv"},
		{Row: rows[1].ID, EpisodeID: 102, SeasonNumber: 1, EpisodeNumber: 2, Name: "Some Show - S01E02 - Español.mkv"},
	}
	if !slices.Equal(v.Rows, want) {
		t.Fatalf("rows %+v, want %+v", v.Rows, want)
	}
	// Every job of the helpers has a subtitle file, which is not handed over for a version.
	if !slices.Equal(v.Notices, []string{NoticeUntrackedBySonarr, NoticeAdoptedWithoutFile, NoticeNoSubtitles}) {
		t.Errorf("notices %v", v.Notices)
	}
	// The view writes nothing, not even the staging links.
	if _, err := os.Stat(filepath.Join(x.root, "jobs", j.ID, "versions")); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("the view staged versions: %v", err)
	}
}

// The policy version, end to end: the rows with a file are placed next to the episodes'
// files (a hard link of the upload; nothing replaced), the other row is imported, and
// Sonarr's monitoring of the versions' episodes is left alone.
func TestEpisodeVersionsArePlacedNextToTheEpisodes(t *testing.T) {
	x := newEpvEnv(t, nil)
	j := x.epvJob()
	rows := x.rows(j)
	before := x.libraryNames()
	c, err := x.confirmEpv(j, "")
	if err != nil {
		t.Fatal(err)
	}
	if c.Job.State != store.Importing || c.SeasonDub != PolicyVersion || !slices.Equal(c.Rows, []string{rows[2].ID}) {
		t.Fatalf("confirmation %+v", c)
	}
	if got := x.libraryNames(); !slices.Equal(got, before) {
		t.Fatalf("the confirmation wrote into the library: %v", got)
	}
	acts := x.actions(j)
	if !slices.Contains(acts, "season.versions") || strings.Count(strings.Join(acts, ","), "version.confirmed") != 2 {
		t.Fatalf("audit %v", acts)
	}
	// Sonarr was asked about each version under its library name, in the job's folder.
	staged := filepath.Join(x.root, "jobs", j.ID, "versions", rows[0].ID, "Some Show - S01E01 - Español.mkv")
	if !sameInode(t, x.src(j, "files/Show.S01E01.mkv"), staged) {
		t.Fatal("the staged version is not the upload")
	}

	j = x.drive(x.eng, j)
	if j.State != store.Done {
		t.Fatalf("job %s (%s: %s)", j.State, j.ErrorCode, j.ErrorText)
	}
	for i, name := range []string{"Some Show - S01E01 - Español.mkv", "Some Show - S01E02 - Español.mkv"} {
		dest := filepath.Join(x.season, name)
		if !sameInode(t, x.src(j, "files/Show.S01E0"+string(rune('1'+i))+".mkv"), dest) {
			t.Fatalf("%s is not the upload", name)
		}
		rec, err := x.st.VersionByPath(x.ctx, dest)
		if err != nil || rec.App != AppSonarr || rec.TitleID != showID || rec.EpisodeID != 101+i || rec.SeasonNumber != 1 ||
			rec.EpisodeNumber != 1+i || rec.Status != store.VersionLinked || rec.Label != "Español" {
			t.Fatalf("record %+v, %v", rec, err)
		}
	}
	for _, name := range before {
		if b, _ := os.ReadFile(filepath.Join(x.season, name)); string(b) != "the library's own" {
			t.Fatalf("%s changed: %q", name, b)
		}
	}
	if len(x.series.Imports) != 1 || len(x.series.Imports[0].Rows) != 1 || x.series.Imports[0].Rows[0].EpisodeIDs[0] != 103 {
		t.Fatalf("imports %+v", x.series.Imports)
	}
	for _, ep := range x.series.Eps[showID][:2] {
		if !ep.Monitored {
			t.Errorf("episode %d was unmonitored", ep.ID)
		}
	}
	if acts := x.actions(j); strings.Count(strings.Join(acts, ","), "version.linked") != 2 {
		t.Fatalf("audit %v", acts)
	}
	iv, rv, err := x.eng.Detail(x.ctx, j, false)
	if err != nil {
		t.Fatal(err)
	}
	results := map[string]string{}
	for _, r := range iv.Rows {
		results[r.ID] = r.Result
	}
	if results[rows[0].ID] != ResultVersion || results[rows[1].ID] != ResultVersion || results[rows[2].ID] != store.ResultImported {
		t.Fatalf("row results %v", results)
	}
	if rv.EpisodeVersions == nil || len(rv.EpisodeVersions.Versions) != 2 || rv.EpisodeVersions.Label != "Español" ||
		rv.EpisodeVersions.Versions[0].Status != store.VersionLinked || rv.SeasonDub == nil || rv.SeasonDub.Policy != PolicyVersion {
		t.Fatalf("result %+v", rv)
	}
}

// The records keep the series' TVDB id (#96), and the job's result still shows each version
// after the keeper's re-check followed one to a new path (Sonarr moved the series) and
// re-attached another to a new episode id.
func TestEpisodeVersionsResultAfterTheRecheck(t *testing.T) {
	x := newEpvEnv(t, nil)
	x.series.Library[0].TVDBID = 5000
	j := x.epvJob()
	rows := x.rows(j)
	if _, err := x.confirmEpv(j, ""); err != nil {
		t.Fatal(err)
	}
	if j = x.drive(x.eng, j); j.State != store.Done {
		t.Fatalf("job %s (%s: %s)", j.State, j.ErrorCode, j.ErrorText)
	}
	recs, err := x.st.VersionsOf(x.ctx, j.ID)
	if err != nil || len(recs) != 2 {
		t.Fatalf("records %+v, %v", recs, err)
	}
	for _, r := range recs {
		if r.TVDBID != 5000 {
			t.Fatalf("record without the series' TVDB id: %+v", r)
		}
	}
	followed, reattached := recs[0], recs[1]
	followed.Path, followed.Status = filepath.Join(x.root, "elsewhere", filepath.Base(followed.Path)), store.VersionGrouped
	reattached.EpisodeID, reattached.Status = 999, store.VersionAdopted
	for _, r := range []store.Version{followed, reattached} {
		if _, err := x.st.PutVersion(x.ctx, r); err != nil {
			t.Fatal(err)
		}
	}
	iv, rv, err := x.eng.Detail(x.ctx, j, true)
	if err != nil {
		t.Fatal(err)
	}
	results := map[string]string{}
	for _, r := range iv.Rows {
		results[r.ID] = r.Result
	}
	if results[rows[0].ID] != ResultVersion || results[rows[1].ID] != ResultVersion {
		t.Fatalf("row results %v", results)
	}
	if v := rv.EpisodeVersions.Versions; len(v) != 2 || v[0].Status != store.VersionGrouped || v[1].Status != store.VersionAdopted {
		t.Fatalf("result %+v", rv.EpisodeVersions)
	}
}

// Only versions: nothing is imported, and the job still ends.
func TestEpisodeVersionsAloneImportNothing(t *testing.T) {
	x := newEpvEnv(t, nil)
	j := x.epvJob()
	rows := x.rows(j)
	x.edit(j, rows[2].ID, Edit{Skip: ptr(true)})
	x.sonarrSays(j, rows[0].ID, "Some Show - S01E01 - Latino.mkv", 101, true)
	x.sonarrSays(j, rows[1].ID, "Some Show - S01E02 - Latino.mkv", 102, true)
	if _, err := x.confirmEpv(x.jobNow(j), "Latino"); err != nil {
		t.Fatal(err)
	}
	j = x.drive(x.eng, j)
	if j.State != store.Done || len(x.series.Imports) != 0 {
		t.Fatalf("job %s (%s), imports %+v", j.State, j.ErrorText, x.series.Imports)
	}
	if !slices.Contains(x.libraryNames(), "Some Show - S01E02 - Latino.mkv") {
		t.Fatalf("library %v", x.libraryNames())
	}
}

// A file whose name would win on Sonarr's rescan is refused: at the confirmation, and when
// Sonarr's verdict changes before the placement.
func TestEpisodeVersionThatSonarrWouldAdoptIsRefused(t *testing.T) {
	x := newEpvEnv(t, nil)
	j := x.epvJob(2) // S01E02's version is an upgrade for Sonarr
	rows := x.rows(j)
	before := x.libraryNames()
	_, err := x.confirmEpv(j, "")
	e := wantRefusal(t, err, CodeVersionUpgrade)
	if !slices.Equal(e.Rows, []string{rows[1].ID}) {
		t.Fatalf("rows %v", e.Rows)
	}
	if j = x.jobNow(j); j.State != store.AwaitingConfirmation || !slices.Equal(x.libraryNames(), before) {
		t.Fatalf("job %s, library %v", j.State, x.libraryNames())
	}

	// Sonarr rejected both at the confirmation, then no longer the first: nothing is placed
	// for it, its row fails with the reason, and the rest goes on (the other version, the new
	// episode).
	y := newEpvEnv(t, nil)
	jy := y.epvJob()
	rows = y.rows(jy)
	if _, err := y.confirmEpv(jy, ""); err != nil {
		t.Fatal(err)
	}
	y.sonarrSays(jy, rows[0].ID, "Some Show - S01E01 - Español.mkv", 101, false)
	jy = y.drive(y.eng, jy)
	if jy.State != store.Done {
		t.Fatalf("job %s (%s: %s)", jy.State, jy.ErrorCode, jy.ErrorText)
	}
	if names := y.libraryNames(); slices.Contains(names, "Some Show - S01E01 - Español.mkv") || !slices.Contains(names, "Some Show - S01E02 - Español.mkv") {
		t.Fatalf("library %v", names)
	}
	if _, err := y.st.VersionByPath(y.ctx, filepath.Join(y.season, "Some Show - S01E01 - Español.mkv")); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("a record for a version that was not placed: %v", err)
	}
	if len(y.series.Imports) != 1 {
		t.Fatalf("the new episode was not imported: %+v", y.series.Imports)
	}
	y.wantRowResults(jy, map[string]string{rows[0].ID: store.ResultFailed, rows[1].ID: ResultVersion, rows[2].ID: store.ResultImported})
	_, rv, err := y.eng.Detail(y.ctx, jy, true)
	if err != nil {
		t.Fatal(err)
	}
	if v := rv.EpisodeVersions.Versions[0]; v.Status != VersionRefused || v.Refusal != CodeVersionUpgrade || !strings.Contains(v.Detail, "would take it") {
		t.Fatalf("result %+v", v)
	}
	if _, rv, _ := y.eng.Detail(y.ctx, jy, false); rv.EpisodeVersions.Versions[0].Detail != "" {
		t.Error("the reason is shown to a user who is not an admin")
	}
	if acts := y.actions(jy); !slices.Contains(acts, "version.refused") {
		t.Fatalf("audit %v", acts)
	}

	// Every version refused and nothing else to import: the job fails with the reasons, and a
	// retry tries the versions again.
	z := newEpvEnv(t, nil)
	jz := z.epvJob()
	rows = z.rows(jz)
	z.edit(jz, rows[2].ID, Edit{Skip: ptr(true)})
	if _, err := z.confirmEpv(z.jobNow(jz), ""); err != nil {
		t.Fatal(err)
	}
	z.sonarrSays(jz, rows[0].ID, "Some Show - S01E01 - Español.mkv", 101, false)
	z.sonarrSays(jz, rows[1].ID, "Some Show - S01E02 - Español.mkv", 102, false)
	jz = z.drive(z.eng, jz)
	if jz.State != store.Failed || jz.ErrorCode != CodeVersionUpgrade || !strings.Contains(jz.ErrorText, "S01E01") || !strings.Contains(jz.ErrorText, "S01E02") {
		t.Fatalf("job %s (%s: %s)", jz.State, jz.ErrorCode, jz.ErrorText)
	}
	tg, _ := z.st.Target(z.ctx, jz.ID)
	if f := episodeVersionFailures(tg); len(f) != 0 {
		t.Fatalf("failures kept for the retry: %v", f)
	}
	if len(z.libraryNames()) != 2 {
		t.Fatalf("library %v", z.libraryNames())
	}
}

// wantRowResults checks the import view's result of each row.
func (x *epvEnv) wantRowResults(j store.Job, want map[string]string) {
	x.t.Helper()
	iv, _, err := x.eng.Detail(x.ctx, j, false)
	if err != nil {
		x.t.Fatal(err)
	}
	got := map[string]string{}
	for _, r := range iv.Rows {
		got[r.ID] = r.Result
	}
	for id, w := range want {
		if got[id] != w {
			x.t.Errorf("row %s: %q, want %q (all %v)", id, got[id], w, got)
		}
	}
}

// A profile that allows no upgrade is a verdict too: Sonarr never takes another file for
// the episode's.
func TestEpisodeVersionWhenTheProfileAllowsNoUpgrade(t *testing.T) {
	x := newEpvEnv(t, nil)
	j := x.epvJob(1, 2)
	for _, r := range x.rows(j)[:2] {
		ar, _, _ := decodeRow(r)
		p := appIntake + "/jobs/" + j.ID + "/versions/" + r.ID + "/Some Show - S01E0" + string(rune('0'+ar.EpisodeIDs[0]-100)) + " - Español.mkv"
		x.series.Verdicts[p] = []arr.Rejection{{Reason: "Existing file and the Quality profile does not allow upgrades", Permanent: true}}
	}
	if _, err := x.confirmEpv(j, ""); err != nil {
		t.Fatal(err)
	}
}

// Sonarr's verdict must be about the episode: a staged name Sonarr does not offer, or reads
// as another episode, has none.
func TestEpisodeVersionWithoutAVerdictIsRefused(t *testing.T) {
	x := newEpvEnv(t, nil)
	j := x.epvJob()
	rows := x.rows(j)
	delete(x.series.Proposed, appIntake+"/jobs/"+j.ID+"/versions/"+rows[0].ID)
	x.sonarrSays(j, rows[1].ID, "Some Show - S01E02 - Español.mkv", 103, true) // read as S01E03
	_, err := x.confirmEpv(j, "")
	e := wantRefusal(t, err, CodeVersionUnchecked)
	if !slices.Equal(e.Rows, []string{rows[0].ID, rows[1].ID}) || !strings.Contains(e.Message, "does not offer") || !strings.Contains(e.Message, "another episode") {
		t.Fatalf("refusal %+v", e)
	}
	// An upgrade among them is said first.
	x.sonarrSays(j, rows[0].ID, "Some Show - S01E01 - Español.mkv", 101, false)
	_, err = x.confirmEpv(j, "")
	if e := wantRefusal(t, err, CodeVersionUpgrade); !slices.Equal(e.Rows, []string{rows[0].ID}) {
		t.Fatalf("rows %v", e.Rows)
	}
}

// What would keep a version from being named or placed shows in the proposal, never as a
// failed view, and the other policies stay on offer.
func TestEpisodeVersionsProblemsShowInTheProposal(t *testing.T) {
	t.Run("a series folder that cannot be a file name", func(t *testing.T) {
		x := newEpvEnv(t, nil)
		x.series.Library[0].Path = appTV + "/What If...?"
		sd := x.view(x.epvJob()).SeasonDub
		if slices.Contains(sd.Offers, PolicyVersion) || !slices.Contains(sd.Offers, PolicyReplace) || sd.Version != nil ||
			!slices.Equal(sd.NotOffered, []NotOffered{{Offer: PolicyVersion, Reason: ReasonSeriesFolderUnusable}}) {
			t.Fatalf("question %+v", sd)
		}
	})
	t.Run("a season folder that takes no link", func(t *testing.T) {
		x := newEpvEnv(t, nil)
		calls := 0
		x.p.linkTrials.check = func(string) error { calls++; return errors.New("EXDEV") }
		j := x.epvJob()
		sd := x.view(j).SeasonDub
		if slices.Contains(sd.Offers, PolicyVersion) || !slices.Equal(sd.NotOffered, []NotOffered{{Offer: PolicyVersion, Reason: ReasonSeasonFolderUnavailable}}) {
			t.Fatalf("question %+v", sd)
		}
		_ = x.view(j)
		if calls != 1 {
			t.Errorf("%d trial links for two views, want one (cached)", calls)
		}
		_, err := x.confirmEpv(j, "")
		_ = wantRefusal(t, err, CodeBadRequest)
	})
	t.Run("a folder that cannot be read", func(t *testing.T) {
		x := newEpvEnv(t, nil)
		x.p.linkTrials.check = func(string) error { return nil }
		j := x.epvJob()
		if err := os.Chmod(x.season, 0o600); err != nil { // no search permission: Lstat fails with EACCES
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = os.Chmod(x.season, 0o755) })
		if _, err := os.Lstat(filepath.Join(x.season, "x")); errors.Is(err, os.ErrNotExist) {
			t.Skip("the permissions are not enforced here (root?)")
		}
		v := x.view(j).SeasonDub.Version
		if v == nil || v.Rows[0].Problem != EpisodeFolderUnreadable || v.Rows[0].Name != "" {
			t.Fatalf("view %+v", v)
		}
		_, err := x.confirmEpv(j, "")
		if e := wantRefusal(t, err, CodeVersionUnavailable); len(e.Rows) != 2 {
			t.Fatalf("refusal %+v", e)
		}
	})
}

// With versions.requireNotUpgradeVerdict off, Sonarr is not asked.
func TestEpisodeVersionsWithoutTheVerdict(t *testing.T) {
	x := newEpvEnv(t, func(c *config.Config) { c.Versions.RequireNotUpgradeVerdict = false })
	j := x.epvJob(1, 2)
	n := len(x.series.ProposalCalls())
	if _, err := x.confirmEpv(j, ""); err != nil {
		t.Fatal(err)
	}
	if j = x.drive(x.eng, j); j.State != store.Done {
		t.Fatalf("job %s (%s)", j.State, j.ErrorText)
	}
	if got := len(x.series.ProposalCalls()); got != n {
		t.Errorf("Sonarr was asked %d time(s)", got-n)
	}
}

func TestEpisodeVersionLabelsAndRows(t *testing.T) {
	t.Run("labels", func(t *testing.T) {
		x := newEpvEnv(t, nil)
		j := x.epvJob()
		for _, label := range []string{"S02E05", "Doblaje 2x05", "1080p", "Español-trailer", "a/b", "E03", "e3", "-E03", "E03 Doblaje"} {
			_, err := x.confirmEpv(j, label)
			_ = wantRefusal(t, err, CodeVersionLabel)
		}
	})
	t.Run("a double episode", func(t *testing.T) {
		x := newEpvEnv(t, nil)
		j := x.epvJob()
		rows := x.rows(j)
		x.edit(j, rows[0].ID, Edit{EpisodeIDs: &[]int{101, 102}})
		x.edit(j, rows[1].ID, Edit{Skip: ptr(true)})
		_, err := x.confirmEpv(x.jobNow(j), "")
		e := wantRefusal(t, err, CodeVersionUnavailable)
		if !slices.Equal(e.Rows, []string{rows[0].ID}) {
			t.Fatalf("rows %v", e.Rows)
		}
		if v := x.view(j).SeasonDub.Version; v.Rows[0].Problem != EpisodeDouble || v.Rows[0].Name != "" {
			t.Fatalf("view %+v", v.Rows)
		}
	})
	t.Run("a file that holds another episode", func(t *testing.T) {
		x := newEpvEnv(t, nil)
		x.series.Eps[showID][1].FileID = x.series.Eps[showID][0].FileID
		j := x.epvJob()
		_, err := x.confirmEpv(j, "")
		e := wantRefusal(t, err, CodeVersionUnavailable)
		if len(e.Rows) != 2 || !strings.Contains(e.Message, EpisodeSharedFile) {
			t.Fatalf("refusal %+v", e)
		}
	})
	t.Run("a folder outside the library roots", func(t *testing.T) {
		x := newEpvEnv(t, func(c *config.Config) {
			c.Paths.LibraryRoots = []string{filepath.Join(c.Paths.LibraryRoots[0], "elsewhere")}
		})
		_, err := x.confirmEpv(x.epvJob(), "")
		e := wantRefusal(t, err, CodeVersionUnavailable)
		if !strings.Contains(e.Message, EpisodeOutsideLibrary) {
			t.Fatalf("refusal %+v", e)
		}
	})
	t.Run("the name is taken", func(t *testing.T) {
		x := newEpvEnv(t, nil)
		j := x.epvJob()
		rows := x.rows(j)
		taken := filepath.Join(x.season, "Some Show - S01E02 - Español.mkv")
		if err := os.WriteFile(taken, []byte("someone's"), 0o644); err != nil {
			t.Fatal(err)
		}
		_, err := x.confirmEpv(j, "")
		e := wantRefusal(t, err, CodeVersionExists)
		if !slices.Equal(e.Rows, []string{rows[1].ID}) {
			t.Fatalf("rows %v", e.Rows)
		}
		if b, _ := os.ReadFile(taken); string(b) != "someone's" {
			t.Fatal("the file was replaced")
		}
		// Another label is free.
		x.sonarrSays(j, rows[0].ID, "Some Show - S01E01 - Latino.mkv", 101, true)
		x.sonarrSays(j, rows[1].ID, "Some Show - S01E02 - Latino.mkv", 102, true)
		if _, err := x.confirmEpv(j, "Latino"); err != nil {
			t.Fatal(err)
		}
	})
	t.Run("a season folder that takes no link", func(t *testing.T) {
		x := newEpvEnv(t, nil)
		x.eng.checkEpisodeFolder = func(string) error { return errors.New("EXDEV") }
		_, err := x.confirmEpv(x.epvJob(), "")
		_ = wantRefusal(t, err, CodeStorageUnavailable)
	})
	t.Run("version without the policy", func(t *testing.T) {
		x := newEpvEnv(t, nil)
		j := x.epvJob()
		_, err := x.eng.Confirm(x.ctx, "user1", "", true, j, ConfirmRequest{SeasonDub: PolicyReplace, Version: &VersionRequest{Label: "Español"}})
		_ = wantRefusal(t, err, CodeBadRequest)
		// A show's job takes a version label, never a merge (#55's field is not ignored).
		_, err = x.eng.Confirm(x.ctx, "user1", "", true, j, ConfirmRequest{SeasonDub: PolicyVersion, Merge: &MergeRequest{}})
		_ = wantRefusal(t, err, CodeBadRequest)
	})
}

// Between the confirmation and the placement the name was taken, or Sonarr's file of the
// episode moved: nothing is replaced, and the job fails saying why.
func TestEpisodeVersionPlacementChecksAgain(t *testing.T) {
	x := newEpvEnv(t, nil)
	j := x.epvJob()
	if _, err := x.confirmEpv(j, ""); err != nil {
		t.Fatal(err)
	}
	taken := filepath.Join(x.season, "Some Show - S01E01 - Español.mkv")
	if err := os.WriteFile(taken, []byte("someone's"), 0o644); err != nil {
		t.Fatal(err)
	}
	if j = x.drive(x.eng, j); j.State != store.Done {
		t.Fatalf("job %s (%s: %s)", j.State, j.ErrorCode, j.ErrorText)
	}
	x.refusedRow(j, 0, CodeVersionExists, "nothing is replaced")
	if b, _ := os.ReadFile(taken); string(b) != "someone's" {
		t.Fatal("the file was replaced")
	}

	y := newEpvEnv(t, nil)
	jy := y.epvJob()
	if _, err := y.confirmEpv(jy, ""); err != nil {
		t.Fatal(err)
	}
	y.series.Files[showID][0].Path = appTV + "/Some Show/Specials/elsewhere.mkv"
	if jy = y.drive(y.eng, jy); jy.State != store.Done {
		t.Fatalf("job %s (%s: %s)", jy.State, jy.ErrorCode, jy.ErrorText)
	}
	y.refusedRow(jy, 0, CodeVersionFailed, "another folder")

	z := newEpvEnv(t, nil)
	jz := z.epvJob()
	if _, err := z.confirmEpv(jz, ""); err != nil {
		t.Fatal(err)
	}
	z.series.Eps[showID][0].FileID = 0 // the episode's file was deleted in Sonarr
	if jz = z.drive(z.eng, jz); jz.State != store.Done {
		t.Fatalf("job %s (%s: %s)", jz.State, jz.ErrorCode, jz.ErrorText)
	}
	z.refusedRow(jz, 0, CodeVersionFailed, "no file in Sonarr")
	if slices.Contains(z.libraryNames(), "Some Show - S01E01 - Español.mkv") {
		t.Fatal("a version was placed next to no file")
	}
}

// A repeat finds its own links and asks Sonarr nothing more; a crash between the record and
// the link (the record still placing) goes on; another job's record of the name is never
// taken over.
func TestEpisodeVersionPlacementRepeats(t *testing.T) {
	x := newEpvEnv(t, nil)
	j := x.epvJob()
	rows := x.rows(j)
	x.edit(j, rows[2].ID, Edit{Skip: ptr(true)})
	if _, err := x.confirmEpv(x.jobNow(j), ""); err != nil {
		t.Fatal(err)
	}
	// A crash after the first record, before its link.
	tg, err := x.st.Target(x.ctx, j.ID)
	if err != nil {
		t.Fatal(err)
	}
	c, _ := episodeVersionsOf(tg)
	if _, err := x.st.PutVersion(x.ctx, episodeVersionRecord(store.Version{}, j, tg, c, c.Versions[0], store.VersionPlacing)); err != nil {
		t.Fatal(err)
	}
	if j, err = x.step(x.eng, j); err != nil || j.State != store.Imported {
		t.Fatalf("job %s, %v (%s)", j.State, err, j.ErrorText)
	}
	n := len(x.series.RecheckCalls())
	placed, err := x.eng.placeEpisodeVersions(x.ctx, j, tg, c)
	if err != nil || !placed || len(x.series.RecheckCalls()) != n {
		t.Fatalf("repeat: %v %v, %d more re-checks", placed, err, len(x.series.RecheckCalls())-n)
	}
	recs, _ := x.st.VersionsOf(x.ctx, j.ID)
	if len(recs) != 2 || recs[0].Status != store.VersionLinked || recs[1].Status != store.VersionLinked {
		t.Fatalf("records %+v", recs)
	}

	y := newEpvEnv(t, nil)
	jy := y.epvJob()
	if _, err := y.confirmEpv(jy, ""); err != nil {
		t.Fatal(err)
	}
	if _, err := y.st.PutVersion(y.ctx, store.Version{JobID: "another", App: AppSonarr, Path: filepath.Join(y.season, "Some Show - S01E01 - Español.mkv"), Status: store.VersionMissing}); err != nil {
		t.Fatal(err)
	}
	if jy = y.drive(y.eng, jy); jy.State != store.Done {
		t.Fatalf("job %s (%s: %s)", jy.State, jy.ErrorCode, jy.ErrorText)
	}
	y.refusedRow(jy, 0, CodeVersionExists, "nothing is taken over")
}

// refusedRow checks that the placement refused the version of row i (of the job's rows)
// with code and a reason containing want, and placed the others.
func (x *epvEnv) refusedRow(j store.Job, i int, code, want string) {
	x.t.Helper()
	rows := x.rows(j)
	_, rv, err := x.eng.Detail(x.ctx, j, true)
	if err != nil {
		x.t.Fatal(err)
	}
	for k, v := range rv.EpisodeVersions.Versions {
		switch {
		case v.Row == rows[i].ID && (v.Status != VersionRefused || v.Refusal != code || !strings.Contains(v.Detail, want)):
			x.t.Fatalf("version %+v, want refused %s (%s)", v, code, want)
		case v.Row != rows[i].ID && v.Status == VersionRefused:
			x.t.Fatalf("version %d refused too: %+v", k, v)
		}
	}
	x.wantRowResults(j, map[string]string{rows[i].ID: store.ResultFailed})
}

// A record left placing by a crash, whose job was then cancelled, is settled like a film's.
func TestEpisodeVersionStrandedRecordIsSettled(t *testing.T) {
	x := newEpvEnv(t, nil)
	j := x.epvJob()
	if _, err := x.confirmEpv(j, ""); err != nil {
		t.Fatal(err)
	}
	if j = x.drive(x.eng, j); j.State != store.Done {
		t.Fatalf("job %s", j.State)
	}
	recs, _ := x.st.VersionsOf(x.ctx, j.ID)
	rec := recs[0]
	rec.Status = store.VersionPlacing
	if rec, _ = x.st.PutVersion(x.ctx, rec); rec.Status != store.VersionPlacing {
		t.Fatal(rec)
	}
	settled, linked, err := SettlePlacing(x.ctx, x.st, x.root, rec)
	if err != nil || !settled || !linked {
		t.Fatalf("settled %v linked %v, %v", settled, linked, err)
	}
}

// A refusal that names rows is fixed for just those rows (#102): the user leaves them out (a
// skip edit on a row with an existing_file warning), and the confirmation goes through. The row
// that is left out is neither imported nor kept as a version, and its library file is
// untouched; the others are placed. Who skipped the row is in the proposal.
func TestEpisodeVersionsLeaveOutARefusedRow(t *testing.T) {
	x := newEpvEnv(t, func(c *config.Config) { c.SeasonDub.Policy = PolicyVersion }) // fixed: no other policy to fall back on
	j := x.epvJob(2)                                                                 // S01E02's version is an upgrade for Sonarr
	rows := x.rows(j)
	library := map[string]string{}
	for _, name := range x.libraryNames() {
		b, err := os.ReadFile(filepath.Join(x.season, name))
		if err != nil {
			t.Fatal(err)
		}
		library[name] = string(b)
	}
	_, err := x.confirmEpv(j, "")
	if e := wantRefusal(t, err, CodeVersionUpgrade); !slices.Equal(e.Rows, []string{rows[1].ID}) {
		t.Fatalf("rows %v", e.Rows)
	}

	// The server skips both rows that have a file; the user records a skip for the refused one.
	by := func() map[string]string {
		out := map[string]string{}
		for _, r := range x.view(j).Rows {
			out[r.ID] = r.SkippedBy
		}
		return out
	}
	if got := by(); got[rows[0].ID] != SkippedByServer || got[rows[1].ID] != SkippedByServer || got[rows[2].ID] != "" {
		t.Fatalf("skippedBy %v", got)
	}
	if v := x.edit(j, rows[1].ID, Edit{Skip: ptr(true)}); !v.Skip || v.SkippedBy != SkippedByUser {
		t.Fatalf("row %+v", v)
	}
	if got := by(); got[rows[0].ID] != SkippedByServer || got[rows[1].ID] != SkippedByUser {
		t.Fatalf("skippedBy %v", got)
	}
	if view := x.view(j).SeasonDub; view == nil || view.Version == nil || len(view.Version.Rows) != 1 || view.Version.Rows[0].Row != rows[0].ID {
		t.Fatalf("version rows %+v", view)
	}

	c, err := x.confirmEpv(x.jobNow(j), "")
	if err != nil || c.SeasonDub != PolicyVersion {
		t.Fatalf("confirmation %+v, %v", c, err)
	}
	if !slices.Equal(c.Rows, []string{rows[2].ID}) {
		t.Fatalf("rows %v", c.Rows)
	}
	j = x.drive(x.eng, j)
	if j.State != store.Done {
		t.Fatalf("job %s (%s: %s)", j.State, j.ErrorCode, j.ErrorText)
	}
	// S01E01 has its version; S01E02 has none, no record, no import; every library file is as it was.
	names := x.libraryNames()
	if !slices.Contains(names, "Some Show - S01E01 - Español.mkv") || slices.Contains(names, "Some Show - S01E02 - Español.mkv") {
		t.Fatalf("library %v", names)
	}
	if _, err := x.st.VersionByPath(x.ctx, filepath.Join(x.season, "Some Show - S01E02 - Español.mkv")); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("a record for the row that was left out: %v", err)
	}
	for name, was := range library {
		if b, err := os.ReadFile(filepath.Join(x.season, name)); err != nil || string(b) != was {
			t.Fatalf("%s changed: %q, %v", name, b, err)
		}
	}
	if len(x.series.Imports) != 1 || len(x.series.Imports[0].Rows) != 1 || x.series.Imports[0].Rows[0].EpisodeIDs[0] != 103 {
		t.Fatalf("imports %+v", x.series.Imports)
	}
	x.wantRowResults(j, map[string]string{rows[0].ID: ResultVersion, rows[1].ID: store.ResultSkipped, rows[2].ID: store.ResultImported})
}

// Leaving out every row that has a file leaves the new episodes to import, and no version;
// taking the skip back hands the row to the server's again.
func TestEpisodeVersionsLeaveOutEveryRowWithAFile(t *testing.T) {
	x := newEpvEnv(t, nil)
	j := x.epvJob()
	rows := x.rows(j)
	x.edit(j, rows[0].ID, Edit{Skip: ptr(true)})
	x.edit(j, rows[1].ID, Edit{Skip: ptr(true)})
	// Nothing is left to choose for: the proposal has no season-dub question, and no policy is sent.
	if v := x.view(j); v.SeasonDub != nil {
		t.Fatalf("season dub %+v", v.SeasonDub)
	}
	if _, err := x.eng.Confirm(x.ctx, "user1", "user1@example.com", true, x.jobNow(j), ConfirmRequest{}); err != nil {
		t.Fatal(err)
	}
	if j = x.drive(x.eng, j); j.State != store.Done || len(x.series.Imports) != 1 {
		t.Fatalf("job %s (%s), imports %+v", j.State, j.ErrorText, x.series.Imports)
	}
	if len(x.libraryNames()) != 2 {
		t.Fatalf("library %v", x.libraryNames())
	}
	y := newEpvEnv(t, nil)
	jy := y.epvJob()
	ry := y.rows(jy)
	y.edit(jy, ry[0].ID, Edit{Skip: ptr(true)})
	if v := y.edit(jy, ry[0].ID, Edit{Skip: ptr(false)}); v.SkippedBy != SkippedByServer {
		t.Fatalf("row %+v", v)
	}
}

// A refused label says why, as the proposal's label problem does.
func TestEpisodeVersionLabelRefusalCarriesTheReason(t *testing.T) {
	x := newEpvEnv(t, nil)
	j := x.epvJob()
	_, err := x.confirmEpv(j, "S02E05")
	if e := wantRefusal(t, err, CodeVersionLabel); e.Reason != LabelEpisode || e.Token != "" {
		t.Fatalf("error %+v", e)
	}
	_, err = x.confirmEpv(j, "Bluray")
	if e := wantRefusal(t, err, CodeVersionLabel); e.Reason != LabelForbidden || e.Token != "bluray" {
		t.Fatalf("error %+v", e)
	}
}

// A label that starts with an episode number would make the name a double episode
// ("S01E02 - E03"): refused as label_episode.
func TestEpisodeLabelWithAnEpisodeNumber(t *testing.T) {
	x := newEpvEnv(t, nil)
	s := &session{p: x.p}
	for _, label := range []string{"E03", "e3", "-E03", "E03 Doblaje", "S02E05", "2x05"} {
		if _, p := checkEpisodeLabel(label, s); p == nil || p.Reason != LabelEpisode {
			t.Errorf("%q: %+v", label, p)
		}
	}
	for _, label := range []string{"Español", "Latino 2", "Edición E", "Ep"} {
		if _, p := checkEpisodeLabel(label, s); p != nil {
			t.Errorf("%q refused: %+v", label, p)
		}
	}
}

func TestAtLeast(t *testing.T) {
	for v, want := range map[string]bool{"12.4.0": true, "12.4": true, "v12.10.0": true, "13.0.0": true, "12.3.99": false, "10.11.5": false, "12.4-rc1": true} {
		if got, ok := atLeast(v, minEpisodeGrouping); !ok || got != want {
			t.Errorf("%s: %v %v, want %v", v, got, ok, want)
		}
	}
	if _, ok := atLeast("12", minEpisodeGrouping); ok {
		t.Error("a bare major parses")
	}
}

func TestEpisodeLanguage(t *testing.T) {
	pr := func(langs ...string) media.Probe {
		var p media.Probe
		for i, l := range langs {
			p.Audio = append(p.Audio, media.AudioTrack{Index: i + 1, Language: l})
		}
		return p
	}
	for _, c := range []struct {
		probe    media.Probe
		original string
		want     string
	}{
		{pr("eng", "spa"), "English", "es"},
		{pr("spa"), "English", "es"},
		{pr("eng"), "English", "en"},
		{pr("und", "fre"), "", "fr"},
		{pr(), "English", ""},
	} {
		if got := episodeLanguage(c.probe, c.original); got != c.want {
			t.Errorf("%+v, %s: %q, want %q", c.probe.Audio, c.original, got, c.want)
		}
	}
}
