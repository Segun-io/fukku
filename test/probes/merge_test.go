//go:build probes

package probes

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/segun-io/fukku/internal/arr"
	"github.com/segun-io/fukku/internal/config"
	"github.com/segun-io/fukku/internal/media"
	"github.com/segun-io/fukku/internal/merge"
	"github.com/segun-io/fukku/test/testenv"
)

// M3-merge (#55): flow 2a's import. The film has a tracked file; the worker's merge task
// (package merge, real mkvmerge) writes the merged file into a job's merged/1/ in the
// intake, and internal/arr's client imports it as Fukku does: the proposal of that folder,
// the row re-checked with the film's id, the existing file's quality and both languages,
// then ManualImport (move). Radarr must replace its file: a new movie-file record of the
// merged file's size with both languages, the old file gone from disk, nothing else in the
// film folder. Then the film is unmonitored (SetMonitored), as merge.unmonitorAfterMerge does.
func TestM3MergeImport(t *testing.T) {
	p := newProbe(t, "M3-merge", "Radarr replaces the film's file with a merged file through Manual Import",
		"Plan §8 2a: a ManualImport of the merged file (in the job's merged/<n>/) with the film's id, the existing file's quality and both languages replaces Radarr's file, which is then unmonitored",
		true, "radarr")
	rc := radarrClient()
	f := elephantsDream
	movie := addFilm(p, f)
	id := testenv.ID(movie)
	before := importFilm(p, movie, f, f.dotted()+".1080p.WEB-DL", "eng")
	folder := fmt.Sprint(movie["path"])
	m, err := rc.Movie(p.ctx, id)
	p.must(err, "Movie")
	p.check(m.File != nil && m.File.ID == testenv.ID(before), "the film tracks the imported file")
	old := *m.File
	libHash := hashFile(p, old.Path)

	// The job: an upload of the same cut with a Spanish audio track, merged by the worker's
	// own code into merged/1/.
	job := p.fresh(intake("M3-merge/jobs/" + strings.Repeat("ab", 16)))
	upload := filepath.Join(job, "files", f.dotted()+".SPANISH.mkv")
	p.video(upload, long(f.minutes, "spa"))
	root, err := os.OpenRoot(job)
	p.must(err, "open the job directory")
	defer func() { _ = root.Close() }()
	cfg := config.Defaults()
	cfg.Paths.LibraryRoots = []string{filepath.Join(env.Data, "media", "movies")}
	cfg.Upload.FreeSpaceMargin = 0
	keep := true
	out, err := merge.Run(p.ctx, root, root, cfg.Paths.LibraryRoots, merge.Args{
		Library: old.Path, Upload: "files/" + filepath.Base(upload), Tracks: []int{1},
		LanguageTag: "es-419", TrackName: "Español (Latino)", KeepOriginalDefault: &keep, Dest: "merged/1",
	}, merge.OptionsFrom(cfg))
	p.must(err, "merge")
	p.check(out.OK, fmt.Sprintf("the merge is verified: %s %s", out.Refusal, out.Detail))
	merged := filepath.Join(job, filepath.FromSlash(out.Path))
	p.check(hashFile(p, old.Path) == libHash, "the merge left the library file as it was")

	// Fukku's row: the folder's proposal, then the film, the existing quality and the
	// existing languages plus Spanish (Latino).
	rows, err := rc.Proposal(p.ctx, filepath.Dir(merged))
	p.must(err, "Proposal")
	i := slices.IndexFunc(rows, func(r arr.Row) bool { return r.Path == merged })
	p.check(i >= 0, "the merged file is in the proposal of its folder")
	row := rows[i]
	parsed := row.Quality.Name
	langs, err := rc.Languages(p.ctx)
	p.must(err, "Languages")
	li := slices.IndexFunc(langs, func(l arr.Language) bool { return l.Name == "Spanish (Latino)" })
	p.check(li >= 0, "Radarr knows Spanish (Latino)")
	row.MovieID = id
	row.Quality = old.Quality
	row.Languages = append(slices.Clone(old.Languages), langs[li])
	checked, err := rc.Recheck(p.ctx, []arr.Row{row})
	p.must(err, "Recheck")
	p.check(len(checked) == 1 && checked[0].MovieID == id, "the re-check keeps the film")
	var rejected []string
	for _, r := range checked[0].Rejections {
		rejected = append(rejected, r.Reason)
	}
	sent := checked[0]
	sent.Quality, sent.Languages = old.Quality, row.Languages

	cid, err := rc.Import(p.ctx, []arr.Row{sent}, "move")
	p.must(err, "Import")
	var cmd arr.Command
	p.must(testenv.WaitFor(p.ctx, cmdTimeout, 500*time.Millisecond, "import command", func() (bool, error) {
		var err error
		cmd, err = rc.Command(p.ctx, cid)
		return cmd.Status != "queued" && cmd.Status != "started", err
	}), "Command")
	got, err := rc.Movie(p.ctx, id)
	p.must(err, "Movie")
	p.check(got.File != nil, "the film has a tracked file after the import")
	nf := *got.File
	var langNames []string
	for _, l := range nf.Languages {
		langNames = append(langNames, l.Name)
	}
	audio := []string{}
	if pr, err := media.RunLibrary(p.ctx, cfg.Paths.LibraryRoots, media.LibraryProbeArgs{Path: nf.Path}, media.OptionsFrom(cfg)); err == nil && pr.OK && pr.Media != nil {
		for _, a := range pr.Media.Audio {
			audio = append(audio, a.Language)
		}
	}
	// The old file is gone, or the new one took its name.
	replaced := nf.ID != old.ID && nf.Size == out.Size && (nf.Path == old.Path || !exists(old.Path))
	p.evidence("before", M{"file": filepath.Base(old.Path), "id": old.ID, "size": old.Size, "quality": old.Quality.Name})
	p.evidence("merged", M{"file": filepath.Base(out.Path), "size": out.Size, "parsedQuality": parsed, "rejections": rejected})
	p.evidence("command", M{"status": cmd.Status, "result": cmd.Result, "message": cmd.Message})
	p.evidence("after", M{"file": filepath.Base(nf.Path), "id": nf.ID, "size": nf.Size, "quality": nf.Quality.Name, "languages": langNames, "audio": audio})
	p.observe("tracked file before: %s (id %d, %s, %d bytes); merged file %s (%d bytes), parsed as %s",
		filepath.Base(old.Path), old.ID, old.Quality.Name, old.Size, filepath.Base(out.Path), out.Size, parsed)
	p.observe("re-checked with the film, quality %s and languages %v: rejections %v", old.Quality.Name, langNames, rejected)
	p.observe("ManualImport (move): %s %s %q", cmd.Status, cmd.Result, cmd.Message)
	p.observe("tracked file after: %s (id %d, %s, %d bytes), languages %v, audio tracks %v; merged file gone: %v; film folder %v",
		filepath.Base(nf.Path), nf.ID, nf.Quality.Name, nf.Size, langNames, audio, !exists(merged), listDir(folder))

	p.must(rc.SetMonitored(p.ctx, id, false), "SetMonitored")
	after, err := rc.Movie(p.ctx, id)
	p.must(err, "Movie")
	p.observe("unmonitored: %v", !after.Monitored)

	switch {
	case cmd.Status != "completed":
		p.differ("the import did not complete: %s %s", cmd.Status, cmd.Message)
	case !replaced:
		p.differ("Radarr did not replace its file with the merged one (tracked %s, id %d → %d, size %d, merged %d)", filepath.Base(nf.Path), old.ID, nf.ID, nf.Size, out.Size)
	case nf.Quality.ID != old.Quality.ID:
		p.differ("the new file has quality %s, not the existing %s", nf.Quality.Name, old.Quality.Name)
	case len(nf.Languages) != len(row.Languages):
		p.differ("the new file has languages %v, not both", langNames)
	case len(audio) != 2:
		p.differ("the tracked file has %d audio tracks, not the original's and the dub", len(audio))
	case after.Monitored:
		p.differ("the film is still monitored")
	default:
		p.confirm()
	}
}

func hashFile(p *probe, path string) string {
	p.t.Helper()
	fh, err := os.Open(path)
	p.must(err, "open "+filepath.Base(path))
	defer func() { _ = fh.Close() }()
	h := sha256.New()
	_, err = io.Copy(h, fh)
	p.must(err, "hash "+filepath.Base(path))
	return hex.EncodeToString(h.Sum(nil))
}
