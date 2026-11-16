package uploads

import (
	"bufio"
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"math/rand/v2"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/segun-io/fukku/internal/auth"
	"github.com/segun-io/fukku/internal/config"
	"github.com/segun-io/fukku/internal/store"
	"github.com/segun-io/fukku/internal/store/storetest"
)

const (
	testChunk  = 64 << 10
	testMax    = 1 << 20
	testMargin = 1 << 20
)

// env is a handler over a temporary intake root and a fake store, served over HTTP.
type env struct {
	t    *testing.T
	cfg  config.Config
	root string
	st   *storetest.Fake
	free atomic.Int64
	skew atomic.Int64 // added to the handler's clock
	h    *Handler
	srv  *httptest.Server
}

func newEnv(t *testing.T, mod func(*config.Config)) *env {
	t.Helper()
	cfg := config.Defaults()
	cfg.Paths.IntakeRoot = t.TempDir()
	cfg.Upload.ChunkSize = testChunk
	cfg.Upload.MaxFileSize = testMax
	cfg.Upload.MaxInFlight = 3 * testMax
	cfg.Upload.MaxParallelPerUser = 2
	cfg.Upload.FreeSpaceMargin = testMargin
	if mod != nil {
		mod(&cfg)
	}
	e := &env{t: t, cfg: cfg, root: cfg.Paths.IntakeRoot, st: storetest.NewFake(nil)}
	e.free.Store(1 << 50)
	e.start()
	t.Cleanup(func() { e.srv.Close() })
	return e
}

// start builds a handler over the env's directories and store, as a web process does at
// start-up, and serves it.
func (e *env) start() {
	e.t.Helper()
	h, err := New(Options{
		Config:    e.cfg,
		Log:       slog.New(slog.NewTextHandler(testWriter{e.t}, &slog.HandlerOptions{Level: slog.LevelDebug})),
		Store:     e.st,
		FreeSpace: func(string) (int64, error) { return e.free.Load(), nil },
		Now:       func() time.Time { return time.Now().Add(time.Duration(e.skew.Load())) },
	})
	if err != nil {
		e.t.Fatal(err)
	}
	h.Start(e.t.Context())
	e.h = h
	e.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if name := r.Header.Get("X-Test-User"); name != "" {
			r = r.WithContext(auth.WithUser(r.Context(), auth.User{Name: name, Admin: r.Header.Get("X-Test-Admin") != ""}))
		}
		h.ServeHTTP(w, r)
	}))
}

// restart stops the server and starts a new handler over the same directories and store.
func (e *env) restart() {
	e.t.Helper()
	e.srv.Close()
	e.start()
}

func (e *env) job(owner string) store.Job {
	e.t.Helper()
	j, err := e.st.CreateJob(context.Background(), owner)
	if err != nil {
		e.t.Fatal(err)
	}
	return j
}

func (e *env) transition(j store.Job, to store.State) {
	e.t.Helper()
	cur, err := e.st.Job(context.Background(), j.ID)
	if err != nil {
		e.t.Fatal(err)
	}
	if _, err := e.st.Transition(context.Background(), j.ID, cur.State, to, store.Change{Actor: "test"}); err != nil {
		e.t.Fatal(err)
	}
}

func (e *env) incoming(id string) string { return filepath.Join(e.root, ".incoming", id) }

func (e *env) jobFile(j store.Job, rel string) string {
	return filepath.Join(e.root, "jobs", j.ID, filepath.FromSlash(rel))
}

func (e *env) uploadRow(id string) (store.Upload, bool) {
	u, err := e.st.Upload(context.Background(), id)
	return u, err == nil
}

func (e *env) files(j store.Job) []store.File {
	e.t.Helper()
	fs, err := e.st.Files(context.Background(), j.ID)
	if err != nil {
		e.t.Fatal(err)
	}
	return fs
}

func (e *env) client(user string) *tusClient {
	return &tusClient{t: e.t, base: e.srv.URL, user: user}
}

// tusClient is a minimal tus 1.0 client.
type tusClient struct {
	t     *testing.T
	base  string
	user  string
	admin bool
}

type response struct {
	status int
	header http.Header
	body   string
}

func (r response) offset() int64 {
	n, err := strconv.ParseInt(r.header.Get("Upload-Offset"), 10, 64)
	if err != nil {
		return -1
	}
	return n
}

// code is the error code of an error response.
func (r response) code() string {
	var body struct {
		Error struct{ Code, Message string } `json:"error"`
	}
	_ = json.Unmarshal([]byte(r.body), &body)
	return body.Error.Code
}

func (c *tusClient) do(method, path string, hdr map[string]string, body []byte) response {
	c.t.Helper()
	var rd io.Reader
	if body != nil {
		rd = bytes.NewReader(body)
	}
	req, err := http.NewRequestWithContext(c.t.Context(), method, c.base+path, rd)
	if err != nil {
		c.t.Fatal(err)
	}
	req.Header.Set("Tus-Resumable", "1.0.0")
	if c.user != "" {
		req.Header.Set("X-Test-User", c.user)
	}
	if c.admin {
		req.Header.Set("X-Test-Admin", "1")
	}
	for k, v := range hdr {
		req.Header.Set(k, v)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		c.t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	b, _ := io.ReadAll(resp.Body)
	return response{resp.StatusCode, resp.Header, string(b)}
}

func metadata(kv ...string) string {
	var parts []string
	for i := 0; i+1 < len(kv); i += 2 {
		parts = append(parts, kv[i]+" "+base64.StdEncoding.EncodeToString([]byte(kv[i+1])))
	}
	return strings.Join(parts, ",")
}

// create starts an upload and returns the response and the new upload's id.
func (c *tusClient) create(jobID, name string, size int64) (response, string) {
	c.t.Helper()
	r := c.do(http.MethodPost, BasePath, map[string]string{
		"Upload-Length":   strconv.FormatInt(size, 10),
		"Upload-Metadata": metadata("filename", name, "jobId", jobID),
	}, nil)
	return r, strings.TrimPrefix(r.header.Get("Location"), BasePath)
}

func (c *tusClient) mustCreate(jobID, name string, size int64) string {
	c.t.Helper()
	r, id := c.create(jobID, name, size)
	if r.status != http.StatusCreated {
		c.t.Fatalf("create %s: %d %s", name, r.status, r.body)
	}
	return id
}

func (c *tusClient) head(id string) response {
	c.t.Helper()
	return c.do(http.MethodHead, BasePath+id, nil, nil)
}

func (c *tusClient) patch(id string, offset int64, data []byte) response {
	c.t.Helper()
	return c.do(http.MethodPatch, BasePath+id, map[string]string{
		"Upload-Offset": strconv.FormatInt(offset, 10),
		"Content-Type":  "application/offset+octet-stream",
	}, data)
}

func (c *tusClient) del(id string) response {
	c.t.Helper()
	return c.do(http.MethodDelete, BasePath+id, nil, nil)
}

// send uploads data[from:] in chunks and returns the last response.
func (c *tusClient) send(id string, data []byte, from int64) response {
	c.t.Helper()
	var r response
	for off := from; off < int64(len(data)); {
		end := min(off+testChunk, int64(len(data)))
		r = c.patch(id, off, data[off:end])
		if r.status != http.StatusNoContent {
			c.t.Fatalf("PATCH at %d: %d %s", off, r.status, r.body)
		}
		if r.offset() != end {
			c.t.Fatalf("PATCH at %d: offset %d, want %d", off, r.offset(), end)
		}
		off = end
	}
	return r
}

// rawPatch opens a connection and sends a PATCH that announces length bytes but sends only
// part. The caller closes the connection or lets it stall.
func (e *env) rawPatch(user, id string, offset, length int64, part []byte) net.Conn {
	e.t.Helper()
	var d net.Dialer
	conn, err := d.DialContext(e.t.Context(), "tcp", strings.TrimPrefix(e.srv.URL, "http://"))
	if err != nil {
		e.t.Fatal(err)
	}
	fmt.Fprintf(conn, "PATCH %s%s HTTP/1.1\r\nHost: example.com\r\nTus-Resumable: 1.0.0\r\n"+
		"X-Test-User: %s\r\nUpload-Offset: %d\r\nContent-Type: application/offset+octet-stream\r\n"+
		"Content-Length: %d\r\n\r\n", BasePath, id, user, offset, length)
	if _, err := conn.Write(part); err != nil {
		e.t.Fatal(err)
	}
	return conn
}

// readStatus reads the status of the response on conn, or 0 when the server closed it
// without one.
func readStatus(conn net.Conn) int {
	resp, err := http.ReadResponse(bufio.NewReader(conn), nil)
	if err != nil {
		return 0
	}
	_ = resp.Body.Close()
	return resp.StatusCode
}

// waitOffset polls HEAD until the upload's offset is want.
func (c *tusClient) waitOffset(id string, want int64) {
	c.t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for {
		r := c.head(id)
		if r.status == http.StatusOK && r.offset() == want {
			return
		}
		if time.Now().After(deadline) {
			c.t.Fatalf("HEAD: %d offset %d, want %d", r.status, r.offset(), want)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

// waitData waits until the upload's data in .incoming/ has size bytes.
func (e *env) waitData(id string, size int64) {
	e.t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for {
		fi, err := os.Stat(e.incoming(id))
		if err == nil && fi.Size() == size {
			return
		}
		if time.Now().After(deadline) {
			e.t.Fatalf("data of %s: %v %v, want %d bytes", id, fi, err, size)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func randomData(n int) []byte {
	r := rand.New(rand.NewPCG(uint64(n), 7))
	b := make([]byte, n)
	for i := range b {
		b[i] = byte(r.Uint32())
	}
	return b
}

func readFile(t *testing.T, path string) []byte {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func exists(path string) bool {
	_, err := os.Lstat(path)
	return err == nil
}

// testWriter sends log lines to t.Log.
type testWriter struct{ t *testing.T }

func (w testWriter) Write(p []byte) (int, error) {
	w.t.Helper()
	w.t.Log(strings.TrimRight(string(p), "\n"))
	return len(p), nil
}
