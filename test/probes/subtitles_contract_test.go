//go:build probes

package probes

import (
	"context"
	"fmt"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/segun-io/fukku/internal/subtitles"
	"github.com/segun-io/fukku/test/testenv"
)

// Contract test of internal/subtitles.Bazarr against the test Bazarr (issue #42): the client
// uploads a subtitle for an imported film and sees it written through the history, within
// what decision #23 allows (the confirmation timeout, 20 s by default). Not part of the
// verdict table: it checks Fukku's client, not an upstream behaviour.
// It needs the metadata services to add the film (make probes-online).
func TestSubtitlesClientContract(t *testing.T) {
	p := newProbe(t, "C5g", "internal/subtitles.Bazarr writes an upload and confirms it through the history",
		"Upload with lower-case true/false, then Written through the film history, within the confirm timeout",
		true, "bazarr", "radarr")
	movie := addFilm(p, tearsOfSteel)
	id := testenv.ID(movie)
	importFilm(p, movie, tearsOfSteel, tearsOfSteel.dotted()+".1080p.WEB-DL", "eng")
	p.must(testenv.WaitFor(p.ctx, 2*time.Minute, 500*time.Millisecond, "Bazarr to know the film", func() (bool, error) {
		return bazarrKnowsMovie(p.ctx, id), nil
	}), "Bazarr to know the film")

	folder := movie["path"].(string)
	removeSRTFiles(folder)
	t.Cleanup(func() { removeSRTFiles(folder) })

	// The client is the one Fukku uses; the key is never printed.
	b := subtitles.NewBazarr(env.Bazarr.Base, env.BazarrKey, 30*time.Second)
	version, err := b.Version(p.ctx)
	p.must(err, "Bazarr version")
	p.observe("Bazarr version %s", version)

	const confirmTimeout = 20 * time.Second
	for _, c := range []struct {
		forced, hi bool
		suffix     string
	}{{false, false, ".es.srt"}, {true, false, ".es.forced.srt"}} {
		u := subtitles.Upload{App: "radarr", TitleID: id, Language: "es", Forced: c.forced, HI: c.hi,
			Name: c5Name("client"), Content: strings.NewReader(srtPlain(time.Minute, 1))}
		start := time.Now()
		p.must(b.Upload(p.ctx, u), "upload")
		var written bool
		err := testenv.WaitFor(p.ctx, confirmTimeout, 250*time.Millisecond, "the history to show the upload", func() (bool, error) {
			var err error
			written, err = b.Written(p.ctx, u)
			return written, err
		})
		p.must(err, fmt.Sprintf("confirm forced=%v", c.forced))
		files := listDir(folder)
		onDisk := slices.ContainsFunc(files, func(f string) bool { return strings.HasSuffix(f, c.suffix) && filepath.Ext(f) == ".srt" })
		p.observe("forced=%v: written and confirmed %.1f s after the call; a file ending %s on disk: %v", c.forced, time.Since(start).Seconds(), c.suffix, onDisk)
		p.check(onDisk, "the file Fukku asked for is on disk: "+c.suffix)
	}

	// An upload for a film Bazarr does not know is refused, and the client says so.
	err = b.Upload(context.Background(), subtitles.Upload{App: "radarr", TitleID: 999999, Language: "es", Name: c5Name("client-unknown"), Content: strings.NewReader("1\n")})
	p.check(err != nil, "an upload for an unknown film fails")
	p.observe("unknown film: %v", err)
	p.confirm()
}
