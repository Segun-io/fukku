//go:build probes

package probes

import (
	"fmt"
	"os"
	"path/filepath"
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

// B6: a better release without the dub would be accepted over a merged file.
// C3: the monitoring editor works.
func TestB6UpgradeWouldDropDub(t *testing.T) {
	p := newProbe(t, "B6", "With upgrades allowed, a better release without the dub replaces a merged file",
		"A merged file (original + dub) can be replaced by a better release that lacks the dub; unmonitoring is the protection",
		true, "radarr")
	p.alsoSkips(specC3)
	movie := addFilm(p, elephantsDream)
	id := testenv.ID(movie)
	importFilm(p, movie, elephantsDream, elephantsDream.dotted()+".1080p.WEB-DL", "eng", "spa")
	dir := p.fresh(intake("B6-upgrade"))
	better := filepath.Join(dir, elephantsDream.dotted()+".1080p.BluRay.x264.mkv")
	p.video(better, long(elephantsDream.minutes, "eng"))
	item := itemFor(proposalForMovie(p, dir, id), better)
	p.evidence("betterReleaseProposal", summary(item))
	accepted := !hasRejection(rejections(item), "upgrade", "cutoff")
	p.observe("a Bluray-1080p release without the dub, over the merged WEBDL-1080p: rejections %v (accepted as an upgrade: %v)", rejections(item), accepted)

	c3 := specC3.probe(t)
	c3.must(env.Radarr.Put(c3.ctx, "/movie/editor", M{"movieIds": []int{id}, "monitored": false}, nil), "movie editor")
	var m M
	c3.must(env.Radarr.Get(c3.ctx, fmt.Sprintf("/movie/%d", id), &m), "get movie")
	c3.observe("monitored after the editor call: %v", m["monitored"])
	if m["monitored"] == false {
		c3.confirm()
	} else {
		c3.differ("the movie is still monitored")
	}

	item = itemFor(proposalForMovie(p, dir, id), better)
	p.observe("after unmonitoring, Manual Import still offers it with rejections %v (Manual Import ignores monitoring)", rejections(item))
	if !accepted {
		p.differ("the better release is rejected; merged files are not at risk from upgrades")
		return
	}
	p.partial("the risk is shown through Manual Import's upgrade decision; that an unmonitored title is skipped by RSS and search needs an indexer and is not probed")
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
