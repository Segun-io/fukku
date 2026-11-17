package main

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/base64"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// What check does, in order (it stops at the first failure):
//
//  1. the proxy refuses a request that is not signed in; signing in works through it
//  2. /api/session knows the user, an admin only through the admin group, and a header the
//     client sends (Remote-Groups, Remote-Email) does not count (docs/reverse-proxy.md,
//     "Check that it works")
//  3. a request that skips the proxy is refused with 403
//  4. /readyz, as an admin, says ready
//  5. flow 1: a small film is uploaded, matched, confirmed and imported by Radarr
//  6. flow 3: a season archive is uploaded, matched, confirmed and imported by Sonarr
//
// It drives the HTTP API as test/e2e/helpers/api.ts does, but through the real reverse proxy
// and with the browser's Origin.

type manifest struct {
	Film struct {
		TMDBID int    `json:"tmdbId"`
		Title  string `json:"title"`
		Year   int    `json:"year"`
		File   string `json:"file"`
	} `json:"film"`
	Season struct {
		TVDBID   int    `json:"tvdbId"`
		Title    string `json:"title"`
		File     string `json:"file"`
		Episodes []int  `json:"episodes"`
	} `json:"season"`
}

// reply is what came back, with the body already read and closed.
type reply struct {
	StatusCode int
	Status     string
	Header     http.Header
}

type checker struct {
	ctx       context.Context
	public    string // https://fukku.example.com
	direct    string // http://fukku-web:8080
	authBase  string
	fixtures  string
	data      string
	radarr    *api
	sonarr    *api
	m         manifest
	web       *http.Client
	cookies   map[string]string
	chunkSize int64
}

func check(args []string) error {
	fs := flag.NewFlagSet("check", flag.ContinueOnError)
	public := fs.String("public", "https://fukku.example.com", "the address users type (server.publicBaseUrl)")
	direct := fs.String("direct", "http://fukku-web:8080", "the web container, bypassing the proxy")
	authBase := fs.String("auth", "http://auth:9091", "the forward-authentication stub")
	radarr := fs.String("radarr", "http://radarr:7878", "Radarr")
	sonarr := fs.String("sonarr", "http://sonarr:8989", "Sonarr")
	fixtures := fs.String("fixtures", "/fixtures", "the generated media and its manifest.json")
	data := fs.String("data", "/data", "the library filesystem, as the apps see it")
	if err := fs.Parse(args); err != nil {
		return err
	}
	c := &checker{
		ctx: context.Background(), public: strings.TrimRight(*public, "/"), direct: *direct, authBase: *authBase,
		fixtures: *fixtures, data: *data, cookies: map[string]string{},
		radarr: newAPI(*radarr, map[string]string{"X-Api-Key": os.Getenv("RADARR_API_KEY")}),
		sonarr: newAPI(*sonarr, map[string]string{"X-Api-Key": os.Getenv("SONARR_API_KEY")}),
		web: &http.Client{
			Timeout: 15 * time.Minute,
			// The proxy's certificate is from its own CA (tls internal): only for the test.
			Transport:     &http.Transport{TLSClientConfig: &tls.Config{InsecureSkipVerify: true}},
			CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
		},
	}
	raw, err := os.ReadFile(filepath.Join(c.fixtures, "manifest.json"))
	if err != nil {
		return err
	}
	if err := json.Unmarshal(raw, &c.m); err != nil {
		return err
	}
	for _, s := range []struct {
		name string
		fn   func() error
	}{
		{"not signed in: the proxy sends the browser to the log-in page", c.unauthenticated},
		{"sign in through the proxy as an admin and as an ordinary user", c.signIn},
		{"/api/session: the admin group makes an admin, client headers do not count", c.session},
		{"a request that skips the proxy is refused (403)", c.skipsProxy},
		{"/readyz as an admin is ready", c.readyz},
		{"flow 1: a film is uploaded and imported by Radarr", c.film},
		{"flow 3: a season archive is imported by Sonarr", c.season},
	} {
		start := time.Now()
		if err := s.fn(); err != nil {
			fmt.Printf("FAIL  %s\n      %v\n", s.name, err)
			return errors.New("a check failed")
		}
		fmt.Printf("ok    %s (%s)\n", s.name, time.Since(start).Round(time.Second))
	}
	return nil
}

// do sends a request through the proxy as user ("" for nobody) with the browser's Origin.
func (c *checker) do(user, method, path string, body io.Reader, headers map[string]string) (*reply, []byte, error) {
	return c.doURL(user, method, c.public+path, body, headers)
}

func (c *checker) doURL(user, method, target string, body io.Reader, headers map[string]string) (*reply, []byte, error) {
	req, err := http.NewRequestWithContext(c.ctx, method, target, body)
	if err != nil {
		return nil, nil, err
	}
	req.Header.Set("Origin", c.public)
	if cookie := c.cookies[user]; cookie != "" {
		req.AddCookie(&http.Cookie{Name: stubCookie, Value: cookie})
	}
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	res, err := c.web.Do(req)
	if err != nil {
		return nil, nil, err
	}
	defer func() { _ = res.Body.Close() }()
	raw, err := io.ReadAll(res.Body)
	return &reply{StatusCode: res.StatusCode, Status: res.Status, Header: res.Header}, raw, err
}

func (c *checker) unauthenticated() error {
	res, _, err := c.do("", "GET", "/", nil, nil)
	if err != nil {
		return err
	}
	if res.StatusCode != http.StatusFound || !strings.Contains(res.Header.Get("Location"), "/login") {
		return fmt.Errorf("GET / without a session answered %s (Location %q), want a redirect to the log-in page", res.Status, res.Header.Get("Location"))
	}
	return nil
}

func (c *checker) signIn() error {
	for user := range stubUsers {
		// The log-in page of the stub is on the auth network, not behind Fukku's address.
		req, err := http.NewRequestWithContext(c.ctx, http.MethodGet, c.authBase+"/login?user="+user, nil)
		if err != nil {
			return err
		}
		res, err := c.web.Do(req)
		if err != nil {
			return err
		}
		_ = res.Body.Close()
		for _, ck := range res.Cookies() {
			if ck.Name == stubCookie {
				c.cookies[user] = ck.Value
			}
		}
		if c.cookies[user] == "" {
			return fmt.Errorf("signing %s in gave no session cookie", user)
		}
	}
	return nil
}

type sessionAnswer struct {
	User struct {
		Name  string `json:"name"`
		Email string `json:"email"`
		Admin bool   `json:"admin"`
	} `json:"user"`
}

func (c *checker) sessionOf(user string, headers map[string]string) (sessionAnswer, error) {
	var s sessionAnswer
	res, raw, err := c.do(user, "GET", "/api/session", nil, headers)
	if err != nil {
		return s, err
	}
	if res.StatusCode != http.StatusOK {
		return s, fmt.Errorf("GET /api/session as %s: %s %s", user, res.Status, truncate(string(raw), 200))
	}
	return s, json.Unmarshal(raw, &s)
}

func (c *checker) session() error {
	admin, err := c.sessionOf("admin1", nil)
	if err != nil {
		return err
	}
	if admin.User.Name != "admin1" || !admin.User.Admin || admin.User.Email != "admin1@example.com" {
		return fmt.Errorf("admin1 is %+v, want name admin1, admin true, email admin1@example.com", admin.User)
	}
	plain, err := c.sessionOf("user1", nil)
	if err != nil {
		return err
	}
	if plain.User.Name != "user1" || plain.User.Admin {
		return fmt.Errorf("user1 is %+v, want an ordinary user", plain.User)
	}
	spoof, err := c.sessionOf("user1", map[string]string{"Remote-Groups": "media-admins", "Remote-Email": "someone@example.com", "Remote-User": "admin1"})
	if err != nil {
		return err
	}
	if spoof.User.Admin || spoof.User.Name != "user1" || spoof.User.Email != "user1@example.com" {
		return fmt.Errorf("client-supplied headers counted: %+v", spoof.User)
	}
	return nil
}

func (c *checker) skipsProxy() error {
	res, raw, err := c.doURL("", "GET", c.direct+"/api/session", nil, map[string]string{"Remote-User": "anyone"})
	if err != nil {
		return err
	}
	if res.StatusCode != http.StatusForbidden {
		return fmt.Errorf("GET %s/api/session with a Remote-User header answered %s %s, want 403", c.direct, res.Status, truncate(string(raw), 200))
	}
	return nil
}

func (c *checker) readyz() error {
	res, raw, err := c.do("admin1", "GET", "/readyz", nil, nil)
	if err != nil {
		return err
	}
	var r struct {
		Status string
		Checks []struct {
			Name   string
			OK     bool
			Detail string
		}
	}
	if err := json.Unmarshal(raw, &r); err != nil {
		return fmt.Errorf("GET /readyz: %s %s", res.Status, truncate(string(raw), 300))
	}
	if res.StatusCode != http.StatusOK || r.Status != "ready" {
		return fmt.Errorf("GET /readyz: %s %s", res.Status, truncate(string(raw), 600))
	}
	for _, ck := range r.Checks {
		fmt.Printf("        readyz %-16s ok=%v\n", ck.Name, ck.OK)
	}
	// An ordinary user does not see it.
	if res, _, err = c.do("user1", "GET", "/readyz", nil, nil); err != nil {
		return err
	} else if res.StatusCode != http.StatusForbidden {
		return fmt.Errorf("GET /readyz as an ordinary user answered %s, want 403", res.Status)
	}
	return nil
}

// ---- the job API (test/e2e/helpers/api.ts) ----------------------------------------------

type job struct {
	ID    string `json:"id"`
	State string `json:"state"`
	Error *struct {
		Code, Message string
	} `json:"error"`
	Files []struct {
		Name, Kind, Status string
	} `json:"files"`
	Import *struct {
		Status string
		Rows   []struct{ Name, Result string }
	} `json:"import"`
	Result *struct {
		PostProcessing []struct{ Step, Status, Detail string }
	} `json:"result"`
}

type proposal struct {
	State string
	Rows  []struct {
		Name       string
		Skip       bool
		Importable bool
		Episodes   []struct{ Number int }
		Rejections []struct{ Reason string }
	}
}

func (c *checker) call(user, method, path string, body any, want int, out any) error {
	var rd io.Reader
	headers := map[string]string{}
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return err
		}
		rd = bytes.NewReader(b)
		headers["Content-Type"] = "application/json"
	}
	res, raw, err := c.do(user, method, path, rd, headers)
	if err != nil {
		return err
	}
	if res.StatusCode != want {
		return fmt.Errorf("%s %s: %s %s", method, path, res.Status, truncate(string(raw), 400))
	}
	if out != nil && len(raw) > 0 {
		return json.Unmarshal(raw, out)
	}
	return nil
}

// until polls the job until done says so; a failed or cancelled job is an error.
func (c *checker) until(user, id, what string, timeout time.Duration, done func(job) bool) (job, error) {
	deadline := time.Now().Add(timeout)
	for {
		var j job
		if err := c.call(user, "GET", "/api/jobs/"+id, nil, http.StatusOK, &j); err != nil {
			return j, err
		}
		if done(j) {
			return j, nil
		}
		if j.State == "failed" || j.State == "cancelled" || time.Now().After(deadline) {
			return j, fmt.Errorf("job %s is %s (%+v) while waiting for %s", id, j.State, j.Error, what)
		}
		time.Sleep(500 * time.Millisecond)
	}
}

// upload sends one file with the tus protocol, in chunks of the default upload.chunkSize, so a
// big file crosses the proxy as several full chunks.
func (c *checker) upload(user, jobID, file string) error {
	f, err := os.Open(file)
	if err != nil {
		return err
	}
	defer func() { _ = f.Close() }()
	st, err := f.Stat()
	if err != nil {
		return err
	}
	b64 := func(s string) string { return base64.StdEncoding.EncodeToString([]byte(s)) }
	res, raw, err := c.do(user, "POST", "/api/uploads/", nil, map[string]string{
		"Tus-Resumable":   "1.0.0",
		"Upload-Length":   strconv.FormatInt(st.Size(), 10),
		"Upload-Metadata": "jobId " + b64(jobID) + ",filename " + b64(filepath.Base(file)),
	})
	if err != nil {
		return err
	}
	if res.StatusCode != http.StatusCreated {
		return fmt.Errorf("create upload: %s %s", res.Status, truncate(string(raw), 300))
	}
	loc, err := url.Parse(res.Header.Get("Location"))
	if err != nil || loc.Path == "" {
		return fmt.Errorf("create upload: Location %q", res.Header.Get("Location"))
	}
	if c.chunkSize == 0 {
		c.chunkSize = 16 << 20 // upload.chunkSize, the default (docs/settings.md)
	}
	buf := make([]byte, c.chunkSize)
	for offset := int64(0); offset < st.Size(); {
		n, err := io.ReadFull(f, buf)
		if err != nil && !errors.Is(err, io.ErrUnexpectedEOF) && !errors.Is(err, io.EOF) {
			return err
		}
		res, raw, err := c.do(user, "PATCH", loc.Path, bytes.NewReader(buf[:n]), map[string]string{
			"Tus-Resumable": "1.0.0",
			"Content-Type":  "application/offset+octet-stream",
			"Upload-Offset": strconv.FormatInt(offset, 10),
		})
		if err != nil {
			return err
		}
		if res.StatusCode != http.StatusNoContent {
			return fmt.Errorf("upload at %d: %s %s", offset, res.Status, truncate(string(raw), 300))
		}
		offset += int64(n)
	}
	return nil
}

// importThrough is the whole of flow 1 and flow 3 up to the app's import: upload, probe,
// match (the title is added through the app's metadata service), proposal, confirm, done.
func (c *checker) importThrough(user, file string, match obj, wantRows int) (job, proposal, error) {
	var j job
	var p proposal
	if err := c.call(user, "POST", "/api/jobs", nil, http.StatusCreated, &j); err != nil {
		return j, p, err
	}
	if err := c.upload(user, j.ID, file); err != nil {
		return j, p, err
	}
	if err := c.call(user, "POST", "/api/jobs/"+j.ID+"/submit", nil, http.StatusOK, nil); err != nil {
		return j, p, err
	}
	var err error
	if j, err = c.until(user, j.ID, "probed", 5*time.Minute, func(j job) bool { return j.State != "uploaded" && j.State != "probing" }); err != nil {
		return j, p, err
	}
	if j.State != "probed" {
		return j, p, fmt.Errorf("job %s is %s (%+v), want probed", j.ID, j.State, j.Error)
	}
	if err := c.call(user, "POST", "/api/jobs/"+j.ID+"/match", match, http.StatusOK, nil); err != nil {
		return j, p, fmt.Errorf("%w (adding a title needs the internet for the app's metadata service)", err)
	}
	if _, err := c.until(user, j.ID, "awaiting-confirmation", 3*time.Minute, func(j job) bool { return j.State == "awaiting-confirmation" }); err != nil {
		return j, p, err
	}
	if err := c.call(user, "GET", "/api/jobs/"+j.ID+"/proposal", nil, http.StatusOK, &p); err != nil {
		return j, p, err
	}
	if len(p.Rows) != wantRows {
		return j, p, fmt.Errorf("the proposal has %d rows, want %d: %+v", len(p.Rows), wantRows, p.Rows)
	}
	for _, r := range p.Rows {
		if !r.Importable || len(r.Rejections) > 0 {
			return j, p, fmt.Errorf("row %s is not importable: %+v", r.Name, r)
		}
	}
	if err := c.call(user, "POST", "/api/jobs/"+j.ID+"/confirm", obj{}, http.StatusAccepted, nil); err != nil {
		return j, p, err
	}
	if j, err = c.until(user, j.ID, "done", 5*time.Minute, func(j job) bool { return j.State == "done" }); err != nil {
		return j, p, err
	}
	if j.Import == nil || j.Import.Status != "done" {
		return j, p, fmt.Errorf("job %s is done but its import is %+v", j.ID, j.Import)
	}
	if j.Result != nil {
		for _, s := range j.Result.PostProcessing {
			fmt.Printf("        post step %-22s %s %s\n", s.Step, s.Status, s.Detail)
		}
	}
	return j, p, nil
}

func (c *checker) mediaServerStep(j job) error {
	if j.Result != nil {
		for _, s := range j.Result.PostProcessing {
			if s.Step == "mediaServerRefresh" && s.Status != "ok" {
				return fmt.Errorf("the media server step is %s: %s", s.Status, s.Detail)
			}
		}
	}
	return nil
}

func sizeOf(path string) (int64, error) {
	st, err := os.Stat(path)
	if err != nil {
		return 0, err
	}
	return st.Size(), nil
}

func (c *checker) film() error {
	file := filepath.Join(c.fixtures, c.m.Film.File)
	want, err := sizeOf(file)
	if err != nil {
		return err
	}
	j, _, err := c.importThrough("user1", file, obj{"app": "radarr", "tmdbId": c.m.Film.TMDBID}, 1)
	if err != nil {
		return err
	}
	if err := c.mediaServerStep(j); err != nil {
		return err
	}
	var movies []struct {
		HasFile   bool
		Path      string
		MovieFile struct {
			Path string
			Size int64
		}
	}
	if err := c.radarr.do(c.ctx, "GET", "/api/v3/movie?tmdbId="+strconv.Itoa(c.m.Film.TMDBID), nil, &movies); err != nil {
		return err
	}
	if len(movies) != 1 || !movies[0].HasFile {
		return fmt.Errorf("radarr does not have a file for %s: %+v", c.m.Film.Title, movies)
	}
	got, err := sizeOf(movies[0].MovieFile.Path)
	if err != nil {
		return fmt.Errorf("radarr's file is not on the library filesystem: %w", err)
	}
	if got != want || movies[0].MovieFile.Size != want {
		return fmt.Errorf("the file in the library is %d bytes (Radarr says %d), the upload was %d", got, movies[0].MovieFile.Size, want)
	}
	fmt.Printf("        Radarr imported %s\n", movies[0].MovieFile.Path)
	return nil
}

func (c *checker) season() error {
	file := filepath.Join(c.fixtures, c.m.Season.File)
	j, p, err := c.importThrough("user1", file, obj{"app": "sonarr", "tvdbId": c.m.Season.TVDBID}, len(c.m.Season.Episodes))
	if err != nil {
		return err
	}
	if err := c.mediaServerStep(j); err != nil {
		return err
	}
	for i, r := range p.Rows {
		if len(r.Episodes) != 1 || r.Episodes[0].Number != c.m.Season.Episodes[i] {
			return fmt.Errorf("row %s maps to %+v, want episode %d", r.Name, r.Episodes, c.m.Season.Episodes[i])
		}
	}
	var series []struct{ ID int }
	if err := c.sonarr.do(c.ctx, "GET", "/api/v3/series?tvdbId="+strconv.Itoa(c.m.Season.TVDBID), nil, &series); err != nil {
		return err
	}
	if len(series) != 1 {
		return fmt.Errorf("sonarr has %d series for tvdb id %d, want 1", len(series), c.m.Season.TVDBID)
	}
	var files []struct {
		Path         string
		SeasonNumber int
	}
	if err := c.sonarr.do(c.ctx, "GET", "/api/v3/episodefile?seriesId="+strconv.Itoa(series[0].ID), nil, &files); err != nil {
		return err
	}
	if len(files) != len(c.m.Season.Episodes) {
		return fmt.Errorf("sonarr has %d episode files, want %d: %+v", len(files), len(c.m.Season.Episodes), files)
	}
	for _, f := range files {
		if _, err := os.Stat(f.Path); err != nil {
			return fmt.Errorf("sonarr's file is not on the library filesystem: %w", err)
		}
		fmt.Printf("        Sonarr imported %s\n", f.Path)
	}
	return nil
}
