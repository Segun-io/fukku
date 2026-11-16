//go:build probes

package probes

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/segun-io/fukku/test/fixtures"
	"github.com/segun-io/fukku/test/testenv"
)

var (
	specB3 = spec{"B3", "Radarr adopts an untracked upgrade in the film folder on rescan",
		"On rescan, an untracked video that is an upgrade (e.g. Bluray-1080p over WEBDL-1080p) becomes the film's file; one that is not (a label with no quality token) is left alone", []string{"radarr"}}
	specC3 = spec{"C3", "Radarr movie editor sets monitoring",
		"`PUT /api/v3/movie/editor {movieIds, monitored}` (spec in the plan)", []string{"radarr"}}
	specC2 = spec{"C2", "Radarr re-checks edited Manual Import rows",
		"`POST /api/v3/manualimport` with edited items returns a fresh decision (spec in the plan)", []string{"radarr"}}
)

// C1: the ManualImport command imports a staged file (plan §6 "Import", unverified).
// B3: Radarr adopts an untracked file in the film folder on rescan if it is an upgrade.
func TestB3RadarrAdoptsUpgradesOnRescan(t *testing.T) {
	c1 := newProbe(t, "C1", "Radarr Manual Import command imports a staged file by rename",
		"`POST /api/v3/command {name: ManualImport, importMode: move, files}` imports; poll `/command/<id>`; `/moviefile` confirms (unverified in the plan)",
		true, "radarr")
	c1.alsoSkips(specB3)
	movie := addFilm(c1, bigBuckBunny)
	id := testenv.ID(movie)
	dir := c1.fresh(intake("C1"))
	src := filepath.Join(dir, bigBuckBunny.dotted()+".1080p.WEB-DL.mkv")
	c1.video(src, long(bigBuckBunny.minutes, "eng"))
	srcInode := inode(src)
	item := itemFor(proposalForMovie(c1, dir, id), src)
	c1.check(item != nil, "staged file is in the proposal")
	c1.evidence("proposal", summary(item))
	cmd := manualImport(c1, env.Radarr, []M{item}, M{"movieId": id}, "move")
	c1.evidence("command", M{"status": cmd.Status, "result": cmd.Result, "message": cmd.Message})
	mf := movieFile(c1, id)
	if mf == nil || cmd.Status != "completed" {
		c1.differ("ManualImport did not produce a movie file (status %s %s)", cmd.Status, cmd.Message)
		return
	}
	tracked := filepath.Join(movie["path"].(string), fmt.Sprint(mf["relativePath"]))
	sameInode := inode(tracked) == srcInode
	c1.evidence("movieFile", M{"relativePath": mf["relativePath"], "quality": qualityName(mf), "sameInode": sameInode, "sourceGone": !exists(src)})
	c1.observe("command %s/%s; file %s (%s); same inode: %v; source gone: %v", cmd.Status, cmd.Result, mf["relativePath"], qualityName(mf), sameInode, !exists(src))
	if !sameInode {
		c1.partial("imported, but not by rename (inode changed)")
	} else {
		c1.confirm()
	}

	t.Run("B3", func(t *testing.T) {
		p := specB3.probe(t)
		folder := movie["path"].(string)
		losing := filepath.Join(folder, "Big Buck Bunny (2008) - Español.mkv")
		p.video(losing, long(bigBuckBunny.minutes, "spa"))

		// What Radarr says about the file before it is linked (the B3 rule relies on it).
		prop := proposal(p, env.Radarr, folder, "movieId", fmt.Sprint(id))
		p.evidence("losingProposal", summary(itemFor(prop, losing)))
		var parsed M
		p.must(env.Radarr.Get(p.ctx, "/parse"+testenv.Q("title", filepath.Base(losing)), &parsed), "parse")
		if pi, ok := parsed["parsedMovieInfo"].(M); ok {
			p.evidence("losingParse", M{"quality": qualityName(pi), "languages": languageNames(pi)})
		}
		rescanMovie(p, id)
		after := movieFile(p, id)
		losingAdopted := after != nil && strings.Contains(fmt.Sprint(after["relativePath"]), "Español")
		p.evidence("afterFirstRescan", M{"tracked": after["relativePath"], "movieFileRecords": movieFileRecords(p, id)})
		p.observe("label without quality token (%s, proposal rejections %v): adopted on rescan: %v",
			qualityName(itemFor(prop, losing)), rejections(itemFor(prop, losing)), losingAdopted)

		winning := filepath.Join(folder, "Big Buck Bunny (2008) Bluray-1080p.mkv")
		p.video(winning, long(bigBuckBunny.minutes, "eng"))
		prop = proposal(p, env.Radarr, folder, "movieId", fmt.Sprint(id))
		p.evidence("winningProposal", summary(itemFor(prop, winning)))
		rescanMovie(p, id)
		after = movieFile(p, id)
		winningAdopted := after != nil && strings.Contains(fmt.Sprint(after["relativePath"]), "Bluray-1080p")
		p.evidence("afterRescan", M{"relativePath": after["relativePath"], "previousFileStillOnDisk": exists(tracked), "folder": listDir(folder), "movieFileRecords": movieFileRecords(p, id)})
		p.observe("Bluray-1080p name (proposal rejections %v): adopted on rescan: %v; files now %v",
			rejections(itemFor(prop, winning)), winningAdopted, listDir(folder))

		records := movieFileRecords(p, id)
		p.observe("tracked file stays %s; /moviefile now lists %d record(s): %v", after["relativePath"], len(records), records)

		// Follow-ups that decide whether second versions are safe: does a rename touch the
		// extra records, and does a later import delete them?
		restore := setMovieNaming(p, "{Movie Title} ({Release Year}) - {Quality Full}")
		cmd, err := env.Radarr.RunCommand(p.ctx, M{"name": "RenameMovie", "movieIds": []int{id}}, cmdTimeout)
		p.must(err, "RenameMovie")
		restore()
		afterRename := listDir(folder)
		p.evidence("afterRename", afterRename)
		p.observe("after RenameMovie (%s) with the \" - \" format: folder %v", cmd.Status, afterRename)

		up := p.fresh(intake("B3-upgrade"))
		remux := filepath.Join(up, bigBuckBunny.dotted()+".1080p.BluRay.REMUX.AVC.mkv")
		p.video(remux, long(bigBuckBunny.minutes, "eng"))
		item := itemFor(proposalForMovie(p, up, id), remux)
		p.check(item != nil, "upgrade file is in the proposal")
		cmd = manualImport(p, env.Radarr, []M{item}, M{"movieId": id}, "move")
		afterUpgrade := listDir(folder)
		p.evidence("afterUpgradeImport", M{"folder": afterUpgrade, "movieFileRecords": movieFileRecords(p, id), "tracked": movieFile(p, id)["relativePath"]})
		p.observe("after a Manual Import of a Remux upgrade (%s): folder %v; records %v", cmd.Status, afterUpgrade, movieFileRecords(p, id))
		versionSurvives := false
		for _, f := range afterUpgrade {
			versionSurvives = versionSurvives || strings.Contains(f, "Español")
		}
		p.evidence("versionFileSurvivesUpgrade", versionSurvives)

		switch {
		case losingAdopted || winningAdopted:
			p.confirm()
		case len(records) > 1:
			p.differ("Radarr does not switch the tracked file; it registers every video in the film folder as an extra movie-file record (%d records); version file survives a later upgrade import: %v", len(records), versionSurvives)
		default:
			p.differ("an upgrade-named file was neither adopted nor registered on rescan")
		}
	})
}

// B4 (Radarr side): Radarr ignores -trailer/-other files and extras/ folders on rescan.
func TestB4RadarrSkipsExtras(t *testing.T) {
	p := newProbe(t, "B4-radarr", "Radarr ignores -trailer, -other and extras/ files on rescan",
		"Radarr's filters skip `-other`, `-trailer`… files and `extras/`-style folders, even when the names claim a better quality",
		true, "radarr")
	movie := addFilm(p, sintel)
	id := testenv.ID(movie)
	mf := importFilm(p, movie, sintel, sintel.dotted()+".720p.WEB-DL", "eng")
	folder := movie["path"].(string)
	for _, name := range []string{"Sintel (2010) Bluray-1080p-trailer.mkv", "Sintel (2010) Bluray-1080p-other.mkv", "extras/Sintel (2010) Bluray-1080p.mkv"} {
		p.video(filepath.Join(folder, name), long(sintel.minutes, "eng"))
	}
	rescanMovie(p, id)
	after := movieFile(p, id)
	prop := proposal(p, env.Radarr, folder, "movieId", fmt.Sprint(id))
	var listed []string
	for _, it := range prop {
		listed = append(listed, filepath.Base(fmt.Sprint(it["path"])))
	}
	p.evidence("trackedBefore", mf["relativePath"])
	p.evidence("trackedAfter", after["relativePath"])
	p.evidence("filesInProposal", listed)
	p.observe("tracked file before %s, after rescan %s; files Manual Import offers: %v", mf["relativePath"], after["relativePath"], listed)
	if after["relativePath"] == mf["relativePath"] {
		p.confirm()
	} else {
		p.differ("Radarr adopted an extra (%s)", after["relativePath"])
	}
}

// B5: existing subtitles are tracked as extra files with importExtraFiles off, and follow a rename.
func TestB5SubtitlesFollowRename(t *testing.T) {
	p := newProbe(t, "B5", "Radarr tracks existing subtitles and moves them on rename",
		"With `importExtraFiles` off, an `.srt` next to the film file is tracked as an extra file and renamed with the film",
		true, "radarr")
	movie := addFilm(p, tearsOfSteel)
	id := testenv.ID(movie)
	mf := importFilm(p, movie, tearsOfSteel, tearsOfSteel.dotted()+".1080p.WEB-DL", "eng")
	folder := movie["path"].(string)
	base := strings.TrimSuffix(fmt.Sprint(mf["relativePath"]), ".mkv")
	p.must(os.WriteFile(filepath.Join(folder, base+".en.srt"), []byte(fixtures.SRT(time.Duration(tearsOfSteel.minutes)*time.Minute, "eng")), 0o640), "write srt")
	rescanMovie(p, id)
	var extras []M
	p.must(env.Radarr.Get(p.ctx, fmt.Sprintf("/extrafile?movieId=%d", id), &extras), "extra files")
	var tracked []string
	for _, e := range extras {
		tracked = append(tracked, fmt.Sprint(e["relativePath"]))
	}
	p.observe("after rescan, extra files tracked: %v", tracked)

	restore := setMovieNaming(p, "{Movie Title} ({Release Year}) - {Quality Full}")
	defer restore()
	cmd, err := env.Radarr.RunCommand(p.ctx, M{"name": "RenameMovie", "movieIds": []int{id}}, cmdTimeout)
	p.must(err, "RenameMovie")
	files := listDir(folder)
	p.evidence("extraFilesTracked", tracked)
	p.evidence("filesAfterRename", files)
	p.observe("rename %s; folder now %v", cmd.Status, files)
	newBase := "Tears of Steel (2012) - WEBDL-1080p"
	srtMoved := exists(filepath.Join(folder, newBase+".en.srt")) && !exists(filepath.Join(folder, base+".en.srt"))
	switch {
	case len(tracked) > 0 && srtMoved:
		p.confirm()
	case len(tracked) == 0:
		p.differ("the subtitle was not tracked as an extra file")
	default:
		p.differ("the subtitle was tracked but did not follow the rename")
	}
}

// B7 (Radarr side) and C2: language handling on import, and the reprocess endpoint.
func TestB7RadarrLanguageOnImport(t *testing.T) {
	p := newProbe(t, "B7-radarr", "Radarr's profile language does not reject imports; custom formats are scored",
		"The profile language (Original) is used when choosing releases, not on import; the \"not original language\" custom format is applied on import",
		true, "radarr")
	p.alsoSkips(specC2)
	movie := addFilm(p, cosmosLaundromat)
	id := testenv.ID(movie)
	dir := p.fresh(intake("B7-radarr"))
	named := filepath.Join(dir, cosmosLaundromat.dotted()+".1080p.WEB-DL.SPANISH.mkv")
	tagged := filepath.Join(dir, "spanish-audio-only", cosmosLaundromat.dotted()+".1080p.WEB-DL.mkv")
	p.video(named, long(cosmosLaundromat.minutes, "spa"))
	p.video(tagged, long(cosmosLaundromat.minutes, "spa"))
	items := proposalForMovie(p, dir, id)
	sn, st := summary(itemFor(items, named)), summary(itemFor(items, tagged))
	p.evidence("spanishInName", sn)
	p.evidence("spanishAudioTagOnly", st)
	p.observe("SPANISH in the name: languages %v, formats %v, score %v, rejections %v", sn["languages"], sn["customFormats"], sn["customFormatScore"], sn["rejections"])
	p.observe("Spanish audio tag only: languages %v, formats %v, score %v, rejections %v", st["languages"], st["customFormats"], st["customFormatScore"], st["rejections"])

	// C2: re-check an edited row (POST /manualimport), here forcing the language to Spanish.
	c2 := specC2.probe(t)
	edit := itemFor(items, tagged)
	edit["languages"] = []M{{"id": 3, "name": "Spanish"}}
	edit["movieId"] = id
	var rechecked []M
	c2.must(env.Radarr.Post(c2.ctx, "/manualimport", []M{edit}, &rechecked), "reprocess")
	if len(rechecked) == 1 {
		c2.evidence("rechecked", summary(rechecked[0]))
		c2.observe("re-checked with language Spanish: formats %v, score %v, rejections %v", formatNames(rechecked[0]), rechecked[0]["customFormatScore"], rejections(rechecked[0]))
		c2.confirm()
	} else {
		c2.differ("reprocess returned %d items", len(rechecked))
	}

	langRejected := hasRejection(sn["rejections"].([]string), "language") || hasRejection(st["rejections"].([]string), "language")
	nameDetected := strings.Contains(fmt.Sprint(sn["languages"]), "Spanish")
	cfApplied := strings.Contains(fmt.Sprint(sn["customFormats"]), "Not Original Language")
	switch {
	case langRejected:
		p.differ("Radarr rejects the import because of the profile language")
	case nameDetected && cfApplied:
		if !strings.Contains(fmt.Sprint(st["languages"]), "Spanish") {
			p.observe("note: a Spanish audio tag alone is not detected as Spanish; Fukku must set the language itself")
		}
		p.confirm()
	default:
		p.partial("no language rejection, but the custom format did not match the Spanish-named file")
	}
}

// B8 (Radarr side): an explicit Manual Import goes ahead despite a rejection.
func TestB8RadarrImportDespiteRejection(t *testing.T) {
	p := newProbe(t, "B8-radarr", "Radarr Manual Import with an explicit movie imports despite rejections",
		"Manual Import with an explicit title id is expected to import even when the proposal lists rejections (unverified)",
		true, "radarr")
	movie := addFilm(p, bigBuckBunny)
	id := testenv.ID(movie)
	before := importFilm(p, movie, bigBuckBunny, bigBuckBunny.dotted()+".1080p.BluRay.x264", "eng")
	dir := p.fresh(intake("B8-radarr"))
	worse := filepath.Join(dir, bigBuckBunny.dotted()+".720p.HDTV.x264.mkv")
	p.video(worse, longAt(bigBuckBunny.minutes, 1280, 720, "spa"))
	item := itemFor(proposalForMovie(p, dir, id), worse)
	rs := rejections(item)
	p.evidence("proposal", summary(item))
	p.check(len(rs) > 0, "the worse file must have a rejection for this probe to mean anything")
	cmd := manualImport(p, env.Radarr, []M{item}, M{"movieId": id}, "move")
	after := movieFile(p, id)
	imported := after != nil && testenv.ID(after) != testenv.ID(before) && qualityName(after) == qualityName(item)
	p.evidence("command", M{"status": cmd.Status, "result": cmd.Result, "message": cmd.Message})
	p.evidence("movieFileAfter", after["relativePath"])
	p.observe("proposal rejections %v; ManualImport %s/%s; movie file now %s; imported: %v", rs, cmd.Status, cmd.Result, after["relativePath"], imported)
	if imported {
		p.confirm()
	} else {
		p.differ("the explicit import did not replace the file: the override flow must change")
	}
}

// C4: naming config and parse endpoints (used for the B1 fix and the B3 check).
func TestC4NamingAndParse(t *testing.T) {
	p := newProbe(t, "C4", "Radarr naming config and parse endpoints",
		"`GET /config/naming` and `GET /parse?title=` return the format and the quality, languages and custom formats of a name",
		false, "radarr")
	var naming M
	p.must(env.Radarr.Get(p.ctx, "/config/naming", &naming), "naming")
	var parsed M
	p.must(env.Radarr.Get(p.ctx, "/parse"+testenv.Q("title", "Some Film (2001) - Español"), &parsed), "parse")
	pi, _ := parsed["parsedMovieInfo"].(M)
	p.evidence("standardMovieFormat", naming["standardMovieFormat"])
	p.evidence("parseLabel", M{"quality": qualityName(pi), "languages": languageNames(pi), "customFormats": formatNames(parsed)})
	p.observe("naming format %q; parse of \"Some Film (2001) - Español\": quality %s, languages %v", naming["standardMovieFormat"], qualityName(pi), languageNames(pi))
	if naming["standardMovieFormat"] != nil && pi != nil {
		p.confirm()
	} else {
		p.differ("naming or parse did not return the expected fields")
	}
}

// C6: how the proposal endpoint scopes a scan. Plan §6 said "folder + movieId"; Radarr 6.4.4
// ignores the folder when movieId is given and scans the film's own folder instead.
func TestC6ProposalScope(t *testing.T) {
	p := newProbe(t, "C6", "Manual Import proposal: folder and title scoping",
		"Plan §6: `GET /manualimport?folder=<job dir>&filterExistingFiles=false` + `movieId` proposes the job's files for that film",
		true, "radarr", "sonarr")
	movie := addFilm(p, bigBuckBunny)
	id := testenv.ID(movie)
	dir := p.fresh(intake("C6"))
	src := filepath.Join(dir, bigBuckBunny.dotted()+".1080p.WEB-DL.mkv")
	p.video(src, long(bigBuckBunny.minutes, "eng"))
	byFolder := itemFor(proposal(p, env.Radarr, dir), src) != nil
	withID := itemFor(proposal(p, env.Radarr, dir, "movieId", fmt.Sprint(id)), src) != nil
	re := itemFor(proposalForMovie(p, dir, id), src)
	reOK := re != nil && testenv.ID(M{"id": re["movieId"]}) == id
	p.evidence("radarr", M{"folderOnlyFindsFile": byFolder, "folderPlusMovieIdFindsFile": withID, "recheckSetsMovie": reOK})
	p.observe("Radarr: folder only finds the file: %v; folder + movieId finds it: %v; re-check with movieId sets the film: %v", byFolder, withID, reOK)
	switch {
	case byFolder && withID:
		p.confirm()
	case byFolder && !withID && reOK:
		p.differ("with movieId the job folder is ignored and the film's own folder is scanned; use the folder-only proposal, then re-check rows with movieId (POST /manualimport)")
	default:
		p.differ("proposal scoping: folder %v, folder+movieId %v, re-check %v", byFolder, withID, reOK)
	}
}

// ---- B3 follow-ups (issue #51: evidence for decision #20) -------------------------------

const claimB3x = "Decision #20: Radarr 6.4.4 registers every video in the film folder as an extra movie-file record (B3); what it does with that record is unverified"

// b3Film is a film with a tracked file and a label file "<folder> - Español.mkv" that Radarr
// has registered as a second movie-file record.
type b3Film struct {
	movie                  M
	id                     int
	folder, tracked, label string
}

func b3Start(p *probe, f film) b3Film {
	movie := addFilm(p, f)
	id := testenv.ID(movie)
	mf := importFilm(p, movie, f, f.dotted()+".1080p.WEB-DL", "eng")
	folder := movie["path"].(string)
	label := filepath.Join(folder, filepath.Base(folder)+" - Español.mkv")
	p.video(label, long(f.minutes, "spa"))
	rescanMovie(p, id)
	recs := movieFileRecords(p, id)
	p.check(len(recs) == 2, fmt.Sprintf("Radarr registers the label file as a second record (records: %v)", recs))
	return b3Film{movie: movie, id: id, folder: folder, tracked: filepath.Join(folder, fmt.Sprint(mf["relativePath"])), label: label}
}

// movieFileList is GET /moviefile?movieId=.
func movieFileList(p *probe, id int) []M {
	var files []M
	p.must(env.Radarr.Get(p.ctx, fmt.Sprintf("/moviefile?movieId=%d", id), &files), "movie files")
	return files
}

// recordFor returns the movie-file record whose relative path contains part.
func recordFor(files []M, part string) M {
	for _, f := range files {
		if strings.Contains(fmt.Sprint(f["relativePath"]), part) {
			return f
		}
	}
	return nil
}

// B3a: deleting the film, without and with "delete files".
func TestB3aDeleteFilm(t *testing.T) {
	p := newProbe(t, "B3a", "Radarr: deleting a film that has a label file", claimB3x, true, "radarr")
	s := b3Start(p, bigBuckBunny)
	p.must(env.Radarr.Delete(p.ctx, fmt.Sprintf("/movie/%d?deleteFiles=false&addImportExclusion=false", s.id)), "delete without files")
	left := listDir(s.folder)
	var recs []M
	err := env.Radarr.Get(p.ctx, fmt.Sprintf("/moviefile?movieId=%d", s.id), &recs)
	p.evidence("withoutDeleteFiles", M{"filesLeft": left, "movieFileRecordsAfter": len(recs), "movieFileQueryStatus": testenv.StatusOf(err)})
	p.observe("deleted without \"delete files\": both videos stay on disk %v; /moviefile?movieId= now answers %d record(s), HTTP status %d", left, len(recs), testenv.StatusOf(err))

	movie := addFilmAt(p, bigBuckBunny, false) // the folder and its files stay
	id := testenv.ID(movie)
	rescanMovie(p, id)
	mf := movieFile(p, id)
	after := movieFileRecords(p, id)
	p.evidence("readded", M{"tracked": mf["relativePath"], "records": after})
	p.observe("the film added again over the same folder, after a rescan: tracked file %v, records %v", mf["relativePath"], after)

	start := time.Now()
	p.must(env.Radarr.Delete(p.ctx, fmt.Sprintf("/movie/%d?deleteFiles=true&addImportExclusion=false", id)), "delete with files")
	// Radarr removes the files after the call returns: wait for the folder to go.
	gone := testenv.WaitFor(p.ctx, 30*time.Second, 250*time.Millisecond, "the film folder to be removed", func() (bool, error) {
		return !exists(s.folder), nil
	}) == nil
	files := listDir(s.folder)
	p.evidence("withDeleteFiles", M{"folderGone": gone, "secondsToRemove": time.Since(start).Seconds(), "filesLeft": files})
	p.observe("deleted with \"delete files\": the film folder is gone: %v (after %.1f s); files left %v; the label file is %s", gone, time.Since(start).Seconds(), files,
		map[bool]string{true: "kept", false: "deleted with the film"}[exists(s.label)])
	p.observed()
}

// B3b: RenameMovie with the Jellyfin-friendly format.
func TestB3bRenameWithoutCollision(t *testing.T) {
	p := newProbe(t, "B3b", "Radarr: renaming a film that has a label file", claimB3x, true, "radarr")
	s := b3Start(p, sintel)
	preview := func(format string) []string {
		restore := setMovieNaming(p, format)
		defer restore()
		var pv []M
		p.must(env.Radarr.Get(p.ctx, fmt.Sprintf("/rename?movieId=%d", s.id), &pv), "rename preview")
		var pairs []string
		for _, r := range pv {
			pairs = append(pairs, fmt.Sprintf("%s -> %s", filepath.Base(fmt.Sprint(r["existingPath"])), filepath.Base(fmt.Sprint(r["newPath"]))))
		}
		return pairs
	}
	const friendly = "{Movie Title} ({Release Year}) - {Quality Full}"
	const withAudio = "{Movie Title} ({Release Year}) - {Quality Full} {MediaInfo AudioLanguages}"
	pv1 := preview(friendly)
	pv2 := preview(withAudio)
	p.evidence("previewJellyfinFriendly", pv1)
	p.evidence("previewWithAudioLanguages", pv2)
	p.observe("preview (GET /rename) with %q: %v", friendly, pv1)
	p.observe("preview with %q: %v", withAudio, pv2)

	restore := setMovieNaming(p, friendly)
	cmd, err := env.Radarr.RunCommand(p.ctx, M{"name": "RenameMovie", "movieIds": []int{s.id}}, cmdTimeout)
	restore()
	p.must(err, "RenameMovie")
	files := listDir(s.folder)
	recs := movieFileRecords(p, s.id)
	p.evidence("afterRename", M{"status": cmd.Status, "message": cmd.Message, "folder": files, "records": recs})
	p.observe("RenameMovie with %q (%s %s): folder %v; records %v", friendly, cmd.Status, cmd.Message, files, recs)
	kept := slices.ContainsFunc(files, func(f string) bool { return strings.Contains(f, "Español") })
	p.observe("the label file %s its name: the preview shows its new name colliding with the tracked file's, and Radarr then leaves it alone", map[bool]string{true: "kept", false: "lost"}[kept])

	// With a format whose names cannot collide, the label file is renamed too.
	restore = setMovieNaming(p, withAudio)
	cmd, err = env.Radarr.RunCommand(p.ctx, M{"name": "RenameMovie", "movieIds": []int{s.id}}, cmdTimeout)
	restore()
	p.must(err, "RenameMovie (audio languages)")
	files2 := listDir(s.folder)
	p.evidence("afterRenameWithAudioLanguages", M{"status": cmd.Status, "folder": files2})
	p.observe("RenameMovie with %q (%s): folder %v", withAudio, cmd.Status, files2)
	p.observed()
}

// B3c: the label file is removed from disk and the film rescanned.
func TestB3cRemoveLabelFileAndRescan(t *testing.T) {
	p := newProbe(t, "B3c", "Radarr: a label file removed from disk, then a rescan", claimB3x, true, "radarr")
	s := b3Start(p, tearsOfSteel)
	before := movieFileList(p, s.id)
	labelRec := recordFor(before, "Español")
	p.check(labelRec != nil, "the label file has a record")
	p.must(os.Remove(s.label), "remove the label file")
	rescanMovie(p, s.id)
	after := movieFileList(p, s.id)
	var rec M
	statusErr := env.Radarr.Get(p.ctx, fmt.Sprintf("/moviefile/%d", testenv.ID(labelRec)), &rec)
	mf := movieFile(p, s.id)
	var events []M
	p.must(env.Radarr.Get(p.ctx, fmt.Sprintf("/history/movie?movieId=%d", s.id), &events), "history")
	var types []string
	for _, e := range events {
		types = append(types, fmt.Sprint(e["eventType"]))
	}
	p.evidence("afterRescan", M{"recordsBefore": len(before), "recordsAfter": len(after), "labelRecordHTTP": testenv.StatusOf(statusErr), "tracked": mf["relativePath"], "historyEvents": types})
	p.observe("records before %d, after the rescan %d; the label record answers HTTP %d; tracked file still %v; history events of the film %v",
		len(before), len(after), testenv.StatusOf(statusErr), mf["relativePath"], types)
	p.observed()
}

// B3d: how Radarr shows the label file, and what deleting its record does.
func TestB3dRadarrViewOfLabelFile(t *testing.T) {
	p := newProbe(t, "B3d", "Radarr: how the film and its records look with a label file", claimB3x, true, "radarr")
	s := b3Start(p, elephantsDream)
	var m M
	p.must(env.Radarr.Get(p.ctx, fmt.Sprintf("/movie/%d", s.id), &m), "movie")
	stats, _ := m["statistics"].(M)
	var sizes []string
	for _, f := range movieFileList(p, s.id) {
		sizes = append(sizes, fmt.Sprintf("%v (%s, %v bytes, runtime %v)", f["relativePath"], qualityName(f), f["size"], runTimeOf(f)))
	}
	trackedSize, labelSize := fileSize(s.tracked), fileSize(s.label)
	mf, _ := m["movieFile"].(M)
	p.evidence("movie", M{"hasFile": m["hasFile"], "sizeOnDisk": m["sizeOnDisk"], "statistics": stats, "movieFile": mf["relativePath"], "records": sizes})
	p.observe("GET /movie/<id>: hasFile %v, movieFile %v, sizeOnDisk %v, statistics.sizeOnDisk %v (tracked file %d bytes, label file %d bytes)",
		m["hasFile"], mf["relativePath"], m["sizeOnDisk"], stats["sizeOnDisk"], trackedSize, labelSize)
	p.observe("GET /moviefile?movieId=: %v", sizes)

	rec := recordFor(movieFileList(p, s.id), "Español")
	p.check(rec != nil, "the label file has a record")
	err := env.Radarr.Delete(p.ctx, fmt.Sprintf("/moviefile/%d", testenv.ID(rec)))
	after := movieFileRecords(p, s.id)
	cur := movieFile(p, s.id)
	p.evidence("deleteRecord", M{"error": fmt.Sprint(err), "labelFileOnDisk": exists(s.label), "trackedOnDisk": exists(s.tracked), "records": after})
	p.observe("DELETE /moviefile/<label record id> (%v): the label file is still on disk: %v; the tracked file still on disk: %v, still tracked: %v; records now %v",
		err, exists(s.label), exists(s.tracked), cur != nil, after)
	p.observed()
}

func fileSize(path string) int64 {
	st, err := os.Stat(path)
	if err != nil {
		return -1
	}
	return st.Size()
}

// B3e: a label file in a subfolder of the film folder, and one outside the root folder
// grouped with Jellyfin's MergeVersions, through a rescan, a rename, an upgrade and a delete.
func TestB3eVersionsOutsideTheFilmFolder(t *testing.T) {
	p := newProbe(t, "B3e", "Radarr: a version in a subfolder, and one outside the root folder merged in Jellyfin", claimB3x, true, "radarr", "jellyfin")
	f := cosmosLaundromat
	movie := addFilm(p, f)
	id := testenv.ID(movie)
	importFilm(p, movie, f, f.dotted()+".1080p.WEB-DL", "eng")
	folder := movie["path"].(string)
	name := filepath.Base(folder)

	sub := filepath.Join(folder, "Versions", name+" - Español.mkv")
	p.video(sub, long(f.minutes, "spa"))
	rescanMovie(p, id)
	recsSub := movieFileRecords(p, id)
	p.evidence("subfolder", recsSub)
	p.observe("a label file in the subfolder Versions/ of the film folder: after a rescan Radarr lists %d record(s): %v", len(recsSub), recsSub)
	p.must(os.RemoveAll(filepath.Join(folder, "Versions")), "remove the subfolder")
	rescanMovie(p, id)

	versionsRoot := filepath.Join(env.Data, "media", "versions")
	vdir := p.fresh(filepath.Join(versionsRoot, name))
	version := filepath.Join(vdir, name+" - Español.mkv")
	p.video(version, long(f.minutes, "spa"))
	rescanMovie(p, id)
	recsOutside := movieFileRecords(p, id)
	p.evidence("outsideRoot", recsOutside)
	p.observe("a version file outside the root folder (a separate versions folder): after a rescan Radarr lists %d record(s): %v", len(recsOutside), recsOutside)

	// Jellyfin: add the versions folder to the Movies library for the length of this probe.
	p.must(env.Jellyfin.AddLibraryPath(p.ctx, "Movies", versionsRoot), "add the versions folder to the Movies library")
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 3*scanTimeout)
		defer cancel()
		_ = env.Jellyfin.RemoveLibraryPath(ctx, "Movies", versionsRoot)
		_ = os.RemoveAll(versionsRoot)
		_ = env.Jellyfin.ScanAndWait(ctx, scanTimeout)
	})
	p.must(env.Jellyfin.ScanAndWait(p.ctx, scanTimeout), "library scan")
	state := func(stage string) []testenv.Item {
		var all []testenv.Item
		seen := map[string]bool{}
		for _, dir := range []string{folder, vdir} {
			items, err := env.Jellyfin.ItemsUnder(p.ctx, dir, "Movie")
			p.must(err, "items "+stage)
			for _, it := range items {
				if !seen[it.ID] {
					seen[it.ID] = true
					all = append(all, it)
				}
			}
		}
		p.observe("Jellyfin %s: %d item(s) in the server's item list %v", stage, len(all), sourcesOf(all))
		p.evidence("jellyfin "+stage, sourcesOf(all))
		user, err := env.Jellyfin.UserMovies(p.ctx, "Cosmos Laundromat")
		p.must(err, "user view "+stage)
		p.observe("Jellyfin %s: a user's library view shows %d entr(ies) %v", stage, len(user), sourcesOf(user))
		p.evidence("jellyfin user view "+stage, sourcesOf(user))
		return all
	}
	items := state("before the merge")
	var filmItem, versionItem *testenv.Item
	for i := range items {
		switch {
		case strings.HasPrefix(items[i].Path, folder+"/"):
			filmItem = &items[i]
		case strings.HasPrefix(items[i].Path, vdir+"/"):
			versionItem = &items[i]
		}
	}
	p.check(filmItem != nil && versionItem != nil, "Jellyfin lists the film and the version as separate items")
	p.must(env.Jellyfin.Do(p.ctx, "POST", "/Videos/MergeVersions"+testenv.Q("ids", filmItem.ID+","+versionItem.ID), nil, nil), "MergeVersions")
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
		defer cancel()
		_ = env.Jellyfin.Unmerge(ctx, filmItem.ID)
	})
	state("after the merge")
	p.observe("Radarr's records after the merge: %v", movieFileRecords(p, id))

	rescanMovie(p, id)
	p.must(env.Jellyfin.ScanAndWait(p.ctx, scanTimeout), "library scan")
	state("after a Radarr rescan and a library scan")

	restore := setMovieNaming(p, "{Movie Title} ({Release Year}) - {Quality Full}")
	cmd, err := env.Radarr.RunCommand(p.ctx, M{"name": "RenameMovie", "movieIds": []int{id}}, cmdTimeout)
	restore()
	p.must(err, "RenameMovie")
	p.observe("RenameMovie (%s): film folder now %v", cmd.Status, listDir(folder))
	p.must(env.Jellyfin.ScanAndWait(p.ctx, scanTimeout), "library scan")
	state("after the rename and a library scan")

	up := p.fresh(intake("B3e-upgrade"))
	remux := filepath.Join(up, f.dotted()+".1080p.BluRay.REMUX.AVC.mkv")
	p.video(remux, long(f.minutes, "eng"))
	item := itemFor(proposalForMovie(p, up, id), remux)
	p.check(item != nil, "upgrade file is in the proposal")
	oldTracked := filepath.Join(folder, fmt.Sprint(movieFile(p, id)["relativePath"]))
	cmd = manualImport(p, env.Radarr, []M{item}, M{"movieId": id}, "move")
	oldGone := testenv.WaitFor(p.ctx, 30*time.Second, 250*time.Millisecond, "the replaced file to be removed", func() (bool, error) {
		return !exists(oldTracked), nil
	}) == nil
	p.observe("a Remux upgrade imported (%s %s %s; proposal rejections %v): the replaced tracked file was removed from disk: %v; film folder now %v; records %v; tracked file now %v",
		cmd.Status, cmd.Result, cmd.Message, rejections(item), oldGone, listDir(folder), movieFileRecords(p, id), movieFile(p, id)["relativePath"])
	p.must(env.Jellyfin.ScanAndWait(p.ctx, scanTimeout), "library scan")
	state("after the upgrade and a library scan")

	p.must(env.Radarr.Delete(p.ctx, fmt.Sprintf("/movie/%d?deleteFiles=true&addImportExclusion=false", id)), "delete with files")
	p.observe("the film deleted with its files: the version file outside the root is still on disk: %v", exists(version))
	p.must(env.Jellyfin.ScanAndWait(p.ctx, scanTimeout), "library scan")
	state("after the film is deleted and a library scan")
	p.observed()
}

func runTimeOf(f M) any {
	mi, _ := f["mediaInfo"].(M)
	return mi["runTime"]
}

// B3g: an upgrade imported through Manual Import (the nearest to a grab by search or RSS that
// needs no indexer) when the film folder has, or has had, a label file.
func TestB3gUpgradeWithLabelFile(t *testing.T) {
	p := newProbe(t, "B3g", "Radarr: an upgrade imported through Manual Import when the film has a label file", claimB3x, true, "radarr")
	f := cosmosLaundromat
	type scenario struct {
		name  string
		setup func() b3Film
	}
	plain := func() b3Film {
		movie := addFilm(p, f)
		importFilm(p, movie, f, f.dotted()+".1080p.WEB-DL", "eng")
		folder := movie["path"].(string)
		return b3Film{movie: movie, id: testenv.ID(movie), folder: folder}
	}
	scenarios := []scenario{
		{"no label file (control)", plain},
		{"label file in the film folder", func() b3Film { return b3Start(p, f) }},
		{"label file, film renamed with the Jellyfin-friendly format", func() b3Film {
			s := b3Start(p, f)
			restore := setMovieNaming(p, "{Movie Title} ({Release Year}) - {Quality Full}")
			_, err := env.Radarr.RunCommand(p.ctx, M{"name": "RenameMovie", "movieIds": []int{s.id}}, cmdTimeout)
			restore()
			p.must(err, "RenameMovie")
			return s
		}},
		{"label file added, then removed and rescanned", func() b3Film {
			s := b3Start(p, f)
			p.must(os.Remove(s.label), "remove the label file")
			rescanMovie(p, s.id)
			return s
		}},
	}
	var rows []M
	for _, sc := range scenarios {
		s := sc.setup()
		before := movieFile(p, s.id)
		oldTracked := filepath.Join(s.folder, fmt.Sprint(before["relativePath"]))
		up := p.fresh(intake("B3g"))
		remux := filepath.Join(up, f.dotted()+".1080p.BluRay.REMUX.AVC.mkv")
		p.video(remux, long(f.minutes, "eng"))
		item := itemFor(proposalForMovie(p, up, s.id), remux)
		p.check(item != nil, "the upgrade is in the proposal")
		cmd := manualImport(p, env.Radarr, []M{item}, M{"movieId": s.id}, "move")
		// Radarr removes the replaced file after the command; give it time.
		oldGone := testenv.WaitFor(p.ctx, 5*time.Second, 250*time.Millisecond, "the replaced file to be removed", func() (bool, error) {
			return !exists(oldTracked), nil
		}) == nil
		after := movieFile(p, s.id)
		recs := movieFileRecords(p, s.id)
		remuxTracked := strings.Contains(fmt.Sprint(after["relativePath"]), "Remux")
		p.observe("%s: import %s, rejections %v; the replaced file was removed: %v; the film's tracked file is now %v (the Remux: %v); folder %v; records %v",
			sc.name, cmd.Status, rejections(item), oldGone, after["relativePath"], remuxTracked, listDir(s.folder), recs)
		rows = append(rows, M{"scenario": sc.name, "importStatus": cmd.Status, "replacedFileRemoved": oldGone, "trackedAfter": after["relativePath"],
			"remuxIsTracked": remuxTracked, "folder": listDir(s.folder), "records": recs})
	}
	p.evidence("scenarios", rows)
	p.observed()
}
