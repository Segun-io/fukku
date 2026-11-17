package main

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/segun-io/fukku/test/fixtures"
)

func needTools(t *testing.T) {
	t.Helper()
	for _, tool := range []string{"ffmpeg", "ffprobe", "mkvmerge"} {
		if _, err := exec.LookPath(tool); err != nil {
			t.Skipf("%s not found (run in the dev image: make test)", tool)
		}
	}
}

func TestUnder(t *testing.T) {
	for p, want := range map[string]bool{
		"/data/media/film.mkv": true,
		"/data":                false,
		"/data/../etc/passwd":  false,
		"/datax/film.mkv":      false,
		"data/film.mkv":        false,
		"/data/./film.mkv":     false,
		"/data/..":             false,
	} {
		if got := under(p, "/data"); got != want {
			t.Errorf("under(%q) = %v, want %v", p, got, want)
		}
	}
}

// A clip whose audio is 300 ms late measures so, and identify reads only files under the
// folder it is given.
func TestHandler(t *testing.T) {
	needTools(t)
	dir := t.TempDir()
	ctx := t.Context()
	src := filepath.Join(dir, "film.mkv")
	if err := fixtures.Generate(ctx, src, fixtures.Video{Duration: 6 * time.Second, Width: 160, Height: 90,
		Audio: []fixtures.Audio{{Language: "spa", Title: "Español"}}}); err != nil {
		t.Fatal(err)
	}
	late := filepath.Join(dir, "late.mkv")
	if err := fixtures.ShiftAudio(ctx, src, late, 300*time.Millisecond); err != nil {
		t.Fatal(err)
	}
	s := httptest.NewServer(handler(dir))
	t.Cleanup(s.Close)

	body, err := os.ReadFile(late)
	if err != nil {
		t.Fatal(err)
	}
	resp, err := http.Post(s.URL+"/sync", "video/x-matroska", bytes.NewReader(body)) //nolint:noctx // a test
	if err != nil {
		t.Fatal(err)
	}
	var got syncJSON
	if err := json.NewDecoder(resp.Body).Decode(&got); err != nil || resp.StatusCode != http.StatusOK {
		t.Fatalf("sync: %d %v", resp.StatusCode, err)
	}
	_ = resp.Body.Close()
	if d := got.OffsetMs - 300; d < -30 || d > 30 || got.Flashes < 4 {
		t.Errorf("sync of a clip 300 ms late: %+v", got)
	}

	resp, err = http.Post(s.URL+"/sync", "video/mp4", bytes.NewReader([]byte("not a clip"))) //nolint:noctx // a test
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusUnprocessableEntity {
		t.Errorf("sync of garbage: %d", resp.StatusCode)
	}

	for p, want := range map[string]int{src: http.StatusOK, "/etc/passwd": http.StatusBadRequest, filepath.Join(dir, "none.mkv"): http.StatusNotFound} {
		resp, err := http.Get(s.URL + "/identify?path=" + url.QueryEscape(p)) //nolint:noctx // a test
		if err != nil {
			t.Fatal(err)
		}
		data, _ := io.ReadAll(resp.Body)
		_ = resp.Body.Close()
		if resp.StatusCode != want {
			t.Errorf("identify %s: %d %s", p, resp.StatusCode, data)
			continue
		}
		if want == http.StatusOK {
			var id struct {
				Tracks []struct {
					Type       string `json:"type"`
					Properties struct {
						Language  string `json:"language"`
						TrackName string `json:"track_name"`
					} `json:"properties"`
				} `json:"tracks"`
			}
			if err := json.Unmarshal(data, &id); err != nil || len(id.Tracks) != 2 || id.Tracks[1].Properties.TrackName != "Español" {
				t.Errorf("identify %s: %s (%v)", p, data, err)
			}
		}
	}
}
