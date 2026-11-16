//go:build probes

package probes

import (
	"bytes"
	"fmt"
	"mime/multipart"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/segun-io/fukku/test/fixtures"
	"github.com/segun-io/fukku/test/testenv"
)

// C5: Bazarr accepts an uploaded subtitle for a Radarr film (plan §6 "Subtitles", spec).
func TestC5BazarrSubtitleUpload(t *testing.T) {
	p := newProbe(t, "C5", "Bazarr accepts a subtitle upload for a film",
		"`POST /api/movies/subtitles?radarrid=&language=&forced=&hi=` with multipart field `file`; all four query parameters required",
		true, "bazarr", "radarr")
	movie := addFilm(p, tearsOfSteel)
	id := testenv.ID(movie)
	importFilm(p, movie, tearsOfSteel, tearsOfSteel.dotted()+".1080p.WEB-DL", "eng")

	// Bazarr learns about films from Radarr on a schedule; ask it to sync now.
	form := url.Values{"taskid": {"update_movies"}}
	p.must(env.Bazarr.DoRaw(p.ctx, "POST", "/api/system/tasks", "application/x-www-form-urlencoded",
		strings.NewReader(form.Encode()), nil, nil), "start the Bazarr movie sync")
	p.must(testenv.WaitFor(p.ctx, 3*time.Minute, 3*time.Second, "Bazarr to know the film", func() (bool, error) {
		var res struct {
			Data []M `json:"data"`
		}
		err := env.Bazarr.Do(p.ctx, "GET", fmt.Sprintf("/api/movies?radarrid[]=%d", id), nil, &res)
		return len(res.Data) > 0, err
	}), "Bazarr sync")
	// The film shows up before the sync job ends, and the job can still rewrite its record;
	// an upload processed in that window fails inside Bazarr. Wait for the job to finish.
	p.must(testenv.WaitFor(p.ctx, 3*time.Minute, 2*time.Second, "Bazarr sync to finish", func() (bool, error) {
		var res struct {
			Data []struct {
				JobID      string `json:"job_id"`
				JobRunning bool   `json:"job_running"`
			} `json:"data"`
		}
		err := env.Bazarr.Do(p.ctx, "GET", "/api/system/tasks", nil, &res)
		for _, j := range res.Data {
			if j.JobID == "update_movies" && j.JobRunning {
				return false, nil
			}
		}
		return err == nil, err
	}), "Bazarr sync")

	upload := func(query string) error {
		var body bytes.Buffer
		mw := multipart.NewWriter(&body)
		fw, err := mw.CreateFormFile("file", "upload.es.srt")
		if err != nil {
			return err
		}
		if _, err := fw.Write([]byte(fixtures.SRT(time.Minute, "spa"))); err != nil {
			return err
		}
		if err := mw.Close(); err != nil {
			return err
		}
		return env.Bazarr.DoRaw(p.ctx, "POST", "/api/movies/subtitles"+query, mw.FormDataContentType(), &body, nil, nil)
	}
	missing := upload(fmt.Sprintf("?radarrid=%d&language=es", id))
	p.evidence("withoutForcedAndHi", testenv.StatusOf(missing))
	p.observe("upload without forced and hi: HTTP %d", testenv.StatusOf(missing))

	folder := movie["path"].(string)
	// Bazarr writes the file from a background job, behind its own sync jobs on a fresh
	// environment; wait for the exact file name expected.
	waitSRT := func(want string) string {
		var got string
		_ = testenv.WaitFor(p.ctx, 3*time.Minute, 2*time.Second, "subtitle file", func() (bool, error) {
			for _, f := range listDir(folder) {
				if strings.HasSuffix(f, want) {
					got = f
					return true, nil
				}
			}
			return false, nil
		})
		return got
	}
	removeSRTs := func() {
		for _, f := range listDir(folder) {
			if filepath.Ext(f) == ".srt" {
				_ = os.Remove(filepath.Join(folder, f))
			}
		}
	}

	removeSRTs()
	err := upload(testenv.Q("radarrid", fmt.Sprint(id), "language", "es", "forced", "False", "hi", "False"))
	plain := waitSRT(".es.srt")
	p.observe("forced=False&hi=False: error %v; wrote %q", err, plain)

	removeSRTs()
	lerr := upload(testenv.Q("radarrid", fmt.Sprint(id), "language", "es", "forced", "false", "hi", "false"))
	lower := waitSRT(".es.hi.srt")
	p.observe("forced=false&hi=false (lower case): error %v; wrote %q", lerr, lower)
	p.evidence("capitalisedFlags", plain)
	p.evidence("lowerCaseFlags", lower)
	removeSRTs()

	const flags = "Bazarr reads forced and hi as Python strings: send True/False capitalised; lower-case \"false\" counts as true"
	switch {
	case err == nil && plain != "" && missing != nil:
		p.confirm()
		if strings.HasSuffix(lower, ".hi.srt") {
			p.observe("note: %s", flags)
		}
	case err == nil && missing != nil && lower != "":
		p.observe("note: %s", flags)
		p.partial("uploads are written by a background job: the first upload after a Radarr sync was accepted (2xx) but not written within 3 min, while a later one was written within seconds; Fukku must confirm through Bazarr's history and retry")
	default:
		p.differ("no upload produced a subtitle file (%v, %v)", err, lerr)
	}
}
