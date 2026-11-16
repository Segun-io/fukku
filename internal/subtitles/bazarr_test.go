package subtitles_test

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/segun-io/fukku/internal/subtitles"
)

const testKey = "sekret-api-key-0000"

// fakeBazarr answers like Bazarr 1.6.2 (probes C5, C5b-C5d): uploads return 204 with no
// body, a film or episode Bazarr does not know is 404, and the history has one entry per
// written upload.
type fakeBazarr struct {
	mu      sync.Mutex
	queries []url.Values
	paths   []string
	files   []string
	known   bool
	write   bool // whether an accepted upload ends up in the history
	history []string
	// delay: the entry appears only after this many history reads following the upload
	delay   int
	pending []string
	reads   int
}

func newFakeBazarr() *fakeBazarr { return &fakeBazarr{known: true, write: true} }

func (f *fakeBazarr) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if r.Header.Get("X-API-KEY") != testKey {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	switch {
	case r.URL.Path == "/api/system/status":
		fmt.Fprint(w, `{"data":{"bazarr_version":"1.6.2"}}`)
	case r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/subtitles"):
		q := r.URL.Query()
		for _, k := range []string{"language", "forced", "hi"} {
			if !q.Has(k) {
				http.Error(w, "missing "+k, http.StatusBadRequest)
				return
			}
		}
		if !f.known {
			http.Error(w, "not found", http.StatusNotFound)
			return
		}
		if err := r.ParseMultipartForm(1 << 20); err != nil {
			http.Error(w, "bad form", http.StatusBadRequest)
			return
		}
		file, hdr, err := r.FormFile("file")
		if err != nil {
			http.Error(w, "no file", http.StatusBadRequest)
			return
		}
		data, _ := io.ReadAll(file)
		f.queries = append(f.queries, q)
		f.paths = append(f.paths, r.URL.Path)
		f.files = append(f.files, hdr.Filename+":"+string(data))
		if f.write {
			// Bazarr compares the flags with the lower-case string "true" only.
			entry := fmt.Sprintf(`{"action":4,"provider":"manual","subtitles_path":"/media/x.%s.srt","language":{"code2":%q,"forced":%t,"hi":%t}}`,
				q.Get("language"), q.Get("language"), q.Get("forced") == "true", q.Get("hi") == "true")
			f.pending = append(f.pending, entry)
			f.reads = 0
		}
		w.WriteHeader(http.StatusNoContent)
	case strings.HasSuffix(r.URL.Path, "/history"):
		f.reads++
		if f.reads > f.delay {
			f.history = append(f.history, f.pending...)
			f.pending = nil
		}
		fmt.Fprintf(w, `{"data":[%s]}`, strings.Join(f.history, ","))
	default:
		http.NotFound(w, r)
	}
}

func newBazarr(t *testing.T) (*subtitles.Bazarr, *fakeBazarr) {
	f := newFakeBazarr()
	srv := httptest.NewServer(f)
	t.Cleanup(srv.Close)
	return subtitles.NewBazarr(srv.URL+"/", testKey, 5*time.Second), f
}

func upload(app string, forced, hi bool) subtitles.Upload {
	u := subtitles.Upload{App: app, TitleID: 7, Language: "es", Forced: forced, HI: hi, Name: "Film.es.srt", Content: strings.NewReader("1\n")}
	if app == "sonarr" {
		u.TitleID, u.EpisodeID = 9, 501
	}
	return u
}

func TestBazarrVersion(t *testing.T) {
	b, _ := newBazarr(t)
	if v, err := b.Version(context.Background()); err != nil || v != "1.6.2" {
		t.Fatalf("%q %v", v, err)
	}
}

func TestBazarrFlagsAreLowerCaseAndAllParametersAreSent(t *testing.T) {
	cases := []struct {
		forced, hi         bool
		wantForced, wantHI string
	}{
		{false, false, "false", "false"},
		{true, false, "true", "false"},
		{false, true, "false", "true"},
		{true, true, "true", "true"},
	}
	for _, c := range cases {
		b, f := newBazarr(t)
		if err := b.Upload(context.Background(), upload("radarr", c.forced, c.hi)); err != nil {
			t.Fatal(err)
		}
		q := f.queries[0]
		if q.Get("forced") != c.wantForced || q.Get("hi") != c.wantHI || q.Get("radarrid") != "7" || q.Get("language") != "es" {
			t.Errorf("query %v", q)
		}
		if f.paths[0] != "/api/movies/subtitles" || f.files[0] != "Film.es.srt:1\n" {
			t.Errorf("%v %v", f.paths, f.files)
		}
	}
}

func TestBazarrEpisodeUpload(t *testing.T) {
	b, f := newBazarr(t)
	if err := b.Upload(context.Background(), upload("sonarr", false, false)); err != nil {
		t.Fatal(err)
	}
	q := f.queries[0]
	if f.paths[0] != "/api/episodes/subtitles" || q.Get("seriesid") != "9" || q.Get("episodeid") != "501" || q.Get("forced") != "false" || q.Get("hi") != "false" {
		t.Errorf("%v %v", f.paths, q)
	}
	noEpisode := upload("sonarr", false, false)
	noEpisode.EpisodeID = 0
	if err := b.Upload(context.Background(), noEpisode); err == nil {
		t.Error("an episode upload without an episode id was sent")
	}
}

func TestBazarrWrittenThroughHistory(t *testing.T) {
	ctx := context.Background()
	b, f := newBazarr(t)
	f.delay = 2 // the history shows the entry from its third look
	u := upload("radarr", false, false)
	if err := b.Upload(ctx, u); err != nil {
		t.Fatal(err)
	}
	var looks []bool
	for range 4 {
		ok, err := b.Written(ctx, u)
		if err != nil {
			t.Fatal(err)
		}
		looks = append(looks, ok)
	}
	if fmt.Sprint(looks) != "[false false true true]" {
		t.Fatalf("looks %v", looks)
	}
}

func TestBazarrRepeatedUploadNeedsAnotherEntry(t *testing.T) {
	ctx := context.Background()
	b, f := newBazarr(t)
	u := upload("radarr", false, false)
	if err := b.Upload(ctx, u); err != nil {
		t.Fatal(err)
	}
	if ok, _ := b.Written(ctx, u); !ok {
		t.Fatal("first upload not seen")
	}
	// The same subtitle again: an old entry is not proof of the new upload.
	u2 := upload("radarr", false, false)
	if err := b.Upload(ctx, u2); err != nil {
		t.Fatal(err)
	}
	ok, err := b.Written(ctx, u2)
	if err != nil || !ok {
		t.Fatalf("second upload: %v %v", ok, err)
	}
	// A third that Bazarr accepts but does not write: the two old entries are not proof.
	f.mu.Lock()
	f.write = false
	f.mu.Unlock()
	u3 := upload("radarr", false, false)
	if err := b.Upload(ctx, u3); err != nil {
		t.Fatal(err)
	}
	if ok, _ := b.Written(ctx, u3); ok {
		t.Fatal("an earlier entry was taken for the new upload")
	}
}

func TestBazarrAcceptedButNotWrittenIsNotWritten(t *testing.T) {
	// With a folder Bazarr cannot write to, the upload is accepted (204) and its queue entry
	// completes, but the history gets nothing (probe C5c): a finished job is not proof.
	ctx := context.Background()
	b, f := newBazarr(t)
	f.write = false
	u := upload("radarr", false, false)
	if err := b.Upload(ctx, u); err != nil {
		t.Fatal(err)
	}
	if ok, err := b.Written(ctx, u); ok || err != nil {
		t.Fatalf("ok=%v err=%v", ok, err)
	}
}

func TestBazarrHistoryOfAnotherLanguageOrForcedStateDoesNotCount(t *testing.T) {
	ctx := context.Background()
	b, f := newBazarr(t)
	f.history = []string{
		`{"action":4,"subtitles_path":"/m/a.en.srt","language":{"code2":"en","forced":false}}`,
		`{"action":4,"subtitles_path":"/m/a.es.forced.srt","language":{"code2":"es","forced":true}}`,
		`{"action":1,"subtitles_path":"/m/a.es.srt","language":{"code2":"es","forced":false}}`,
		`{"action":4,"subtitles_path":"","language":{"code2":"es","forced":false}}`,
	}
	f.write = false
	u := upload("radarr", false, false)
	if err := b.Upload(ctx, u); err != nil {
		t.Fatal(err)
	}
	if ok, _ := b.Written(ctx, u); ok {
		t.Fatal("an entry of another kind counted")
	}
}

func TestBazarrHearingImpairedIsNotCompared(t *testing.T) {
	// Bazarr marks a subtitle hearing-impaired by itself when its cues are bracketed (C5f).
	ctx := context.Background()
	b, f := newBazarr(t)
	f.write = false
	u := upload("radarr", false, false)
	if err := b.Upload(ctx, u); err != nil {
		t.Fatal(err)
	}
	f.history = append(f.history, `{"action":4,"provider":"manual","subtitles_path":"/m/a.es.hi.srt","language":{"code2":"es","forced":false,"hi":true}}`)
	if ok, err := b.Written(ctx, u); !ok || err != nil {
		t.Fatalf("ok=%v err=%v", ok, err)
	}
}

func TestBazarrUnknownTitle(t *testing.T) {
	b, f := newBazarr(t)
	f.known = false
	err := b.Upload(context.Background(), upload("radarr", false, false))
	if !errors.Is(err, subtitles.ErrUnknownTitle) {
		t.Fatalf("err = %v", err)
	}
}

func TestBazarrErrorsNeverCarryTheKey(t *testing.T) {
	ctx := context.Background()
	refused := subtitles.NewBazarr("http://127.0.0.1:1", testKey, time.Second)
	echo := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, "not json")
	}))
	defer echo.Close()
	garbled := subtitles.NewBazarr(echo.URL, testKey, time.Second)
	good := httptest.NewServer(newFakeBazarr())
	defer good.Close()
	wrongKey := subtitles.NewBazarr(good.URL, "wrong-key", time.Second)

	_, err1 := refused.Version(ctx)
	_, err2 := garbled.Version(ctx)
	_, err3 := wrongKey.Version(ctx)
	for name, c := range map[string]struct {
		err error
		key string
	}{
		"refused":   {err1, testKey},
		"garbled":   {err2, testKey},
		"wrong key": {err3, "wrong-key"},
		"upload":    {refused.Upload(ctx, upload("radarr", false, false)), testKey},
	} {
		if c.err == nil {
			t.Errorf("%s: no error", name)
		} else if strings.Contains(c.err.Error(), c.key) {
			t.Errorf("%s: the key is in the error: %v", name, c.err)
		}
	}
}
