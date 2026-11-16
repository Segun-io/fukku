package uploads

import (
	"bytes"
	"context"
	"errors"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/segun-io/fukku/internal/config"
	"github.com/segun-io/fukku/internal/jobdir"
	"github.com/segun-io/fukku/internal/store"
)

func TestUploadBecomesJobFile(t *testing.T) {
	for _, tc := range []struct {
		name, rel string
		kind      store.FileKind
	}{
		{"Some Film (2001).mkv", "files/Some Film (2001).mkv", store.KindVideo},
		{"Some Film (2001).es.SRT", "files/Some Film (2001).es.SRT", store.KindSubtitle},
		{"Season 01.zip", "archives/Season 01.zip", store.KindArchive},
	} {
		t.Run(tc.name, func(t *testing.T) {
			e := newEnv(t, nil)
			j := e.job("user1")
			c := e.client("user1")
			data := randomData(3*testChunk + 1000)

			r, id := c.create(j.ID, tc.name, int64(len(data)))
			if r.status != http.StatusCreated {
				t.Fatalf("create: %d %s", r.status, r.body)
			}
			if !jobdir.ValidID(id) || r.header.Get("Location") != "/api/uploads/"+id {
				t.Fatalf("Location %q", r.header.Get("Location"))
			}
			checkExpires(t, r, time.Now())
			before, err := os.Stat(e.incoming(id))
			if err != nil {
				t.Fatal(err)
			}

			// First chunk: the row follows the offset.
			r = c.patch(id, 0, data[:testChunk])
			if r.status != http.StatusNoContent || r.offset() != testChunk {
				t.Fatalf("PATCH: %d %s", r.status, r.body)
			}
			checkExpires(t, r, time.Now())
			if u, ok := e.uploadRow(id); !ok || u.Offset != testChunk || u.Owner != "user1" || u.Name != tc.name {
				t.Fatalf("row %+v", u)
			}
			if r := c.head(id); r.status != http.StatusOK || r.offset() != testChunk ||
				r.header.Get("Upload-Length") != strconv.Itoa(len(data)) {
				t.Fatalf("HEAD: %d %v", r.status, r.header)
			}

			// The rest: when the last PATCH is answered, the file is part of the job.
			r = c.send(id, data, testChunk)
			if r.offset() != int64(len(data)) {
				t.Fatalf("final offset %d", r.offset())
			}
			path := e.jobFile(j, tc.rel)
			after, err := os.Stat(path)
			if err != nil {
				t.Fatal(err)
			}
			if !os.SameFile(before, after) {
				t.Fatal("the file has another inode than in .incoming/")
			}
			if !bytes.Equal(readFile(t, path), data) {
				t.Fatal("content differs")
			}
			files := e.files(j)
			if len(files) != 1 || files[0].Path != tc.rel || files[0].Kind != tc.kind ||
				files[0].Status != store.FilePending || files[0].Size != int64(len(data)) {
				t.Fatalf("files %+v", files)
			}
			if _, ok := e.uploadRow(id); ok {
				t.Fatal("the upload row is still there")
			}
			if exists(e.incoming(id)) || exists(e.incoming(id)+".info") {
				t.Fatal(".incoming/ still has the upload")
			}
			// The upload stays complete for its owner (the answer to the last PATCH may
			// have been lost), and only for its owner.
			if r := c.head(id); r.status != http.StatusOK || r.offset() != int64(len(data)) ||
				r.header.Get("Upload-Length") != strconv.Itoa(len(data)) {
				t.Fatalf("HEAD after completion: %d %v", r.status, r.header)
			}
			if r := e.client("user2").head(id); r.status != http.StatusNotFound {
				t.Fatalf("HEAD by another user: %d", r.status)
			}
		})
	}
}

func TestLostLastAnswer(t *testing.T) {
	// The last PATCH completed the upload, but its 204 never reached the client (a proxy
	// timed out). Whatever the client does next, it learns that the upload is complete.
	e := newEnv(t, nil)
	j := e.job("user1")
	c := e.client("user1")
	data := randomData(2*testChunk + 3)
	id := c.mustCreate(j.ID, "film.mkv", int64(len(data)))
	c.send(id, data, 0)
	last := int64(2 * testChunk)

	if r := c.patch(id, last, data[last:]); r.status != http.StatusNoContent || r.offset() != int64(len(data)) {
		t.Fatalf("repeated last PATCH: %d offset %d %s", r.status, r.offset(), r.body)
	}
	if r := c.patch(id, int64(len(data)), nil); r.status != http.StatusNoContent || r.offset() != int64(len(data)) {
		t.Fatalf("empty PATCH at the end: %d offset %d", r.status, r.offset())
	}
	if r := c.patch(id, int64(len(data))+1, nil); r.status != http.StatusConflict || r.code() != "offset_mismatch" {
		t.Fatalf("PATCH past the end: %d %s", r.status, r.body)
	}
	if r := e.client("user2").patch(id, last, data[last:]); r.status != http.StatusNotFound {
		t.Fatalf("PATCH by another user: %d", r.status)
	}
	if r := c.del(id); r.status != http.StatusNotFound {
		t.Fatalf("DELETE of a finished upload: %d", r.status)
	}
	if len(e.files(j)) != 1 || !bytes.Equal(readFile(t, e.jobFile(j, "files/film.mkv")), data) {
		t.Fatal("the job's file changed")
	}

	// Until upload.idleUploadExpiry; after that the upload is unknown.
	e.skew.Store(int64(49 * time.Hour))
	if r := c.head(id); r.status != http.StatusNotFound {
		t.Fatalf("HEAD after the expiry: %d", r.status)
	}
}

func TestHeadWhileAnotherRequestFinishes(t *testing.T) {
	// The HEAD found the row, but by the time tusd looked, the upload had been finished
	// (here: the state finish leaves, with the row put back). It answers complete and does not
	// drop anything.
	e := newEnv(t, nil)
	j := e.job("user1")
	c := e.client("user1")
	id := c.mustCreate(j.ID, "film.mkv", 10)
	row, _ := e.uploadRow(id)
	e.h.remember(row)
	for _, p := range []string{e.incoming(id), e.incoming(id) + ".info"} {
		if err := os.Remove(p); err != nil {
			t.Fatal(err)
		}
	}
	if r := c.head(id); r.status != http.StatusOK || r.offset() != 10 {
		t.Fatalf("HEAD: %d offset %d", r.status, r.offset())
	}
	if _, ok := e.uploadRow(id); !ok {
		t.Fatal("the row was dropped")
	}
}

func TestTusNotFoundForgetsOnlyWhenDataIsGone(t *testing.T) {
	e := newEnv(t, nil)
	j := e.job("user1")
	c := e.client("user1")
	id := c.mustCreate(j.ID, "film.mkv", 10)

	// Only the .info file is missing: tusd cannot serve it, but the data is there.
	if err := os.Remove(e.incoming(id) + ".info"); err != nil {
		t.Fatal(err)
	}
	if r := c.head(id); r.status != http.StatusNotFound {
		t.Fatalf("HEAD: %d", r.status)
	}
	if _, ok := e.uploadRow(id); !ok {
		t.Fatal("the row was dropped while its data is there")
	}

	// Both gone: the upload is forgotten.
	if err := os.Remove(e.incoming(id)); err != nil {
		t.Fatal(err)
	}
	if r := c.head(id); r.status != http.StatusNotFound {
		t.Fatalf("HEAD: %d", r.status)
	}
	if _, ok := e.uploadRow(id); ok {
		t.Fatal("the row of an upload without data is still there")
	}
}

func TestErrorsDoNotLeakDetails(t *testing.T) {
	e := newEnv(t, nil)
	j := e.job("user1")
	c := e.client("user1")
	id := c.mustCreate(j.ID, "film.mkv", 10)
	// tusd fails reading the .info file with an OS error that names its path.
	info := e.incoming(id) + ".info"
	if err := os.Remove(info); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(info, 0o755); err != nil {
		t.Fatal(err)
	}
	r := c.patch(id, 0, []byte("0123"))
	if r.status != http.StatusInternalServerError || r.code() != "internal" || strings.Contains(r.body, e.root) {
		t.Fatalf("PATCH: %d %s", r.status, r.body)
	}

	// A missing sentinel is reported without its path.
	e2 := newEnv(t, func(c *config.Config) {
		c.Paths.Sentinels = []string{filepath.Join(c.Paths.IntakeRoot, ".mounted")}
	})
	r, _ = e2.client("user1").create(e2.job("user1").ID, "a.mkv", 10)
	if r.status != http.StatusServiceUnavailable || strings.Contains(r.body, e2.root) || strings.Contains(r.body, ".mounted") {
		t.Fatalf("create: %d %s", r.status, r.body)
	}
}

func TestMoveWithoutHardLinks(t *testing.T) {
	// A filesystem without hard links: the upload is renamed instead, never over a file.
	e := newEnv(t, nil)
	orig := link
	link = func(*os.Root, string, string) error { return syscall.EPERM }
	t.Cleanup(func() { link = orig })

	j := e.job("user1")
	c := e.client("user1")
	id := c.mustCreate(j.ID, "film.mkv", 3)
	before, err := os.Stat(e.incoming(id))
	if err != nil {
		t.Fatal(err)
	}
	c.send(id, []byte("abc"), 0)
	after, err := os.Stat(e.jobFile(j, "files/film.mkv"))
	if err != nil || !os.SameFile(before, after) || exists(e.incoming(id)) {
		t.Fatalf("not moved: %v", err)
	}

	// The rename refuses to replace a file that appeared in between.
	id2 := c.mustCreate(j.ID, "other.mkv", 3)
	dst := e.jobFile(j, "files/other.mkv")
	root, err := os.OpenRoot(e.root)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = root.Close() }()
	if err := os.WriteFile(dst, []byte("old"), 0o600); err != nil {
		t.Fatal(err)
	}
	c.send(id2, []byte("abc")[:2], 0)
	appendFile(t, e.incoming(id2), []byte("c"))
	if err := moveNoReplace(root, filepath.Join(".incoming", id2), filepath.Join("jobs", j.ID, "files", "other.mkv"), 3); !errors.Is(err, errTargetTaken) {
		t.Fatalf("move over a file: %v", err)
	}
	if string(readFile(t, dst)) != "old" || !exists(e.incoming(id2)) {
		t.Fatal("the file was replaced")
	}
}

func checkExpires(t *testing.T, r response, progress time.Time) {
	t.Helper()
	got, err := http.ParseTime(r.header.Get("Upload-Expires"))
	if err != nil {
		t.Fatalf("Upload-Expires %q: %v", r.header.Get("Upload-Expires"), err)
	}
	want := progress.Add(48 * time.Hour)
	if d := got.Sub(want); d < -5*time.Second || d > 5*time.Second {
		t.Fatalf("Upload-Expires %s, want about %s", got, want)
	}
}

func TestResumeAfterDroppedConnection(t *testing.T) {
	e := newEnv(t, nil)
	j := e.job("user1")
	c := e.client("user1")
	data := randomData(2*testChunk + 77)
	id := c.mustCreate(j.ID, "film.mkv", int64(len(data)))

	// Half a chunk is sent, then the connection drops.
	half := int64(testChunk / 2)
	conn := e.rawPatch("user1", id, 0, testChunk, data[:half])
	_ = conn.Close()

	// Wait until the server has read what was sent (a HEAD sent earlier would cut the
	// request short, and the client would resume from an earlier offset, which is fine but
	// not what this test is about).
	e.waitData(id, half)
	c.waitOffset(id, half)
	if u, _ := e.uploadRow(id); u.Offset != half {
		t.Fatalf("row offset %d, want %d", u.Offset, half)
	}
	c.send(id, data, half)
	if !bytes.Equal(readFile(t, e.jobFile(j, "files/film.mkv")), data) {
		t.Fatal("content differs")
	}
}

func TestRestartContinuesUpload(t *testing.T) {
	e := newEnv(t, nil)
	j := e.job("user1")
	data := randomData(3 * testChunk)
	id := e.client("user1").mustCreate(j.ID, "film.mkv", int64(len(data)))
	e.client("user1").send(id, data[:testChunk+100], 0)

	e.restart()
	c := e.client("user1")
	if r := c.head(id); r.status != http.StatusOK || r.offset() != testChunk+100 {
		t.Fatalf("HEAD after restart: %d offset %d", r.status, r.offset())
	}
	c.send(id, data, testChunk+100)
	if !bytes.Equal(readFile(t, e.jobFile(j, "files/film.mkv")), data) {
		t.Fatal("content differs")
	}
}

func TestStalledRequestIsClosed(t *testing.T) {
	idle := 300 * time.Millisecond
	e := newEnv(t, func(c *config.Config) { c.Upload.RequestIdleTimeout = config.Duration(idle) })
	j := e.job("user1")
	c := e.client("user1")
	data := randomData(testChunk + 10)
	id := c.mustCreate(j.ID, "film.mkv", int64(len(data)))

	// 10 KiB arrive, then nothing: the server must give up on the request.
	start := time.Now()
	conn := e.rawPatch("user1", id, 0, testChunk, data[:10<<10])
	defer func() { _ = conn.Close() }()
	_ = conn.SetReadDeadline(time.Now().Add(10 * time.Second))
	status := readStatus(conn)
	took := time.Since(start)
	if took < idle || took > 5*time.Second {
		t.Fatalf("the stalled request ended after %s (idle timeout %s)", took, idle)
	}
	if status != 0 && status < 400 {
		t.Fatalf("status %d", status)
	}

	c.waitOffset(id, 10<<10)
	c.send(id, data, 10<<10)
	if !bytes.Equal(readFile(t, e.jobFile(j, "files/film.mkv")), data) {
		t.Fatal("content differs")
	}
}

func TestDeadlinesDoNotOutliveTheRequest(t *testing.T) {
	// tusd puts deadlines on the connection (upload.requestIdleTimeout); the next request on
	// the same keep-alive connection, long after, must not inherit them. net/http clears
	// them after each request; this keeps it so.
	idle := 100 * time.Millisecond
	e := newEnv(t, func(c *config.Config) { c.Upload.RequestIdleTimeout = config.Duration(idle) })
	j := e.job("user1")
	c := e.client("user1")
	id := c.mustCreate(j.ID, "film.mkv", 2*testChunk)
	c.send(id, randomData(testChunk), 0)
	time.Sleep(4 * idle)
	if r := c.patch(strings.Repeat("d", 32), 0, []byte("x")); r.status != http.StatusNotFound {
		t.Fatalf("status %d", r.status)
	}
}

func TestRefusals(t *testing.T) {
	type check func(t *testing.T, e *env) response
	createAs := func(user string, jobID func(e *env) string, name, length string) check {
		return func(t *testing.T, e *env) response {
			hdr := map[string]string{"Upload-Metadata": metadata("filename", name, "jobId", jobID(e))}
			if length != "" {
				hdr["Upload-Length"] = length
			}
			return e.client(user).do(http.MethodPost, BasePath, hdr, nil)
		}
	}
	own := func(e *env) string { return e.job("user1").ID }
	create := func(name string, size int64) check {
		return createAs("user1", own, name, strconv.FormatInt(size, 10))
	}

	cases := []struct {
		name   string
		mod    func(*config.Config)
		run    check
		status int
		code   string
	}{
		{"empty name", nil, create("", 10), 400, "bad_name"},
		{"blank name", nil, create(" \t", 10), 400, "bad_name"},
		{"slash", nil, create("a/b.mkv", 10), 400, "bad_name"},
		{"backslash", nil, create(`a\b.mkv`, 10), 400, "bad_name"},
		{"dot dot", nil, create("..", 10), 400, "bad_name"},
		{"leading dot", nil, create(".hidden.mkv", 10), 400, "bad_name"},
		{"too long", nil, create(strings.Repeat("n", 197)+".mkv", 10), 400, "bad_name"},
		{"no length", nil, createAs("user1", own, "a.mkv", ""), 400, "bad_request"},
		{"bad length", nil, createAs("user1", own, "a.mkv", "-1"), 400, "bad_request"},
		{"deferred length", nil, func(t *testing.T, e *env) response {
			return e.client("user1").do(http.MethodPost, BasePath, map[string]string{
				"Upload-Defer-Length": "1", "Upload-Metadata": metadata("filename", "a.mkv", "jobId", e.job("user1").ID),
			}, nil)
		}, 400, "bad_request"},
		{"no job id", nil, func(t *testing.T, e *env) response {
			return e.client("user1").do(http.MethodPost, BasePath, map[string]string{
				"Upload-Length": "10", "Upload-Metadata": metadata("filename", "a.mkv"),
			}, nil)
		}, 400, "bad_request"},
		{"no file name", nil, func(t *testing.T, e *env) response {
			return e.client("user1").do(http.MethodPost, BasePath, map[string]string{
				"Upload-Length": "10", "Upload-Metadata": metadata("jobId", e.job("user1").ID),
			}, nil)
		}, 400, "bad_request"},
		{"data in creation", nil, func(t *testing.T, e *env) response {
			return e.client("user1").do(http.MethodPost, BasePath, map[string]string{
				"Upload-Length": "3", "Upload-Metadata": metadata("filename", "a.mkv", "jobId", e.job("user1").ID),
				"Content-Type": "application/offset+octet-stream",
			}, []byte("abc"))
		}, 400, "bad_request"},
		{"no such job", nil, createAs("user1", func(*env) string { return strings.Repeat("a", 32) }, "a.mkv", "10"), 404, "not_found"},
		{"malformed job id", nil, createAs("user1", func(*env) string { return "../../etc" }, "a.mkv", "10"), 404, "not_found"},
		{"someone else's job", nil, createAs("user2", own, "a.mkv", "10"), 404, "not_found"},
		{"no such upload", nil, func(t *testing.T, e *env) response {
			return e.client("user1").patch(strings.Repeat("b", 32), 0, []byte("x"))
		}, 404, "not_found"},
		{"no such upload, HEAD", nil, func(t *testing.T, e *env) response {
			return e.client("user1").head(strings.Repeat("b", 32))
		}, 404, ""},
		{"malformed upload id", nil, func(t *testing.T, e *env) response {
			return e.client("user1").patch("..%2f..%2fx", 0, []byte("x"))
		}, 404, "not_found"},
		{"submitted job", nil, func(t *testing.T, e *env) response {
			j := e.job("user1")
			e.transition(j, store.Uploaded)
			r, _ := e.client("user1").create(j.ID, "a.mkv", 10)
			return r
		}, 409, "job_not_uploading"},
		{"PATCH into a cancelled job", nil, func(t *testing.T, e *env) response {
			j := e.job("user1")
			c := e.client("user1")
			id := c.mustCreate(j.ID, "a.mkv", 10)
			e.transition(j, store.Cancelled)
			return c.patch(id, 0, []byte("0123456789"))
		}, 409, "job_not_uploading"},
		{"duplicate of a file", nil, func(t *testing.T, e *env) response {
			j := e.job("user1")
			if _, err := e.st.AddFile(context.Background(), store.File{JobID: j.ID, Path: "files/Film.mkv", Kind: store.KindVideo}); err != nil {
				t.Fatal(err)
			}
			r, _ := e.client("user1").create(j.ID, "film.MKV", 10)
			return r
		}, 409, "duplicate_name"},
		{"duplicate of an unfinished upload", nil, func(t *testing.T, e *env) response {
			j := e.job("user1")
			c := e.client("user1")
			c.mustCreate(j.ID, "Season 01.zip", 10)
			r, _ := c.create(j.ID, "SEASON 01.ZIP", 10)
			return r
		}, 409, "duplicate_name"},
		{"too large", nil, create("a.mkv", testMax+1), 413, "file_too_large"},
		{"chunk too large", nil, func(t *testing.T, e *env) response {
			c := e.client("user1")
			id := c.mustCreate(e.job("user1").ID, "a.mkv", testMax)
			return c.patch(id, 0, make([]byte, testChunk+1))
		}, 413, "too_large"},
		{"extension", nil, create("a.exe", 10), 415, "extension_not_allowed"},
		{"no extension", nil, create("README", 10), 415, "extension_not_allowed"},
		{"too many uploads", nil, func(t *testing.T, e *env) response {
			c := e.client("user1")
			j := e.job("user1")
			c.mustCreate(j.ID, "a.mkv", 10)
			c.mustCreate(e.job("user1").ID, "b.mkv", 10)
			// Another user is not affected.
			e.client("user2").mustCreate(e.job("user2").ID, "c.mkv", 10)
			r, _ := c.create(j.ID, "d.mkv", 10)
			return r
		}, 429, "too_many_uploads"},
		{"in flight", nil, func(t *testing.T, e *env) response {
			e.client("user1").mustCreate(e.job("user1").ID, "a.mkv", testMax)
			e.client("user1").mustCreate(e.job("user1").ID, "b.mkv", testMax)
			e.client("user2").mustCreate(e.job("user2").ID, "c.mkv", testMax-10)
			r, _ := e.client("user2").create(e.job("user2").ID, "d.mkv", 11)
			return r
		}, 507, "in_flight_limit"},
		{"free space", nil, func(t *testing.T, e *env) response {
			e.free.Store(testMax + testMargin - 1)
			r, _ := e.client("user1").create(e.job("user1").ID, "a.mkv", testMax)
			return r
		}, 507, "insufficient_space"},
		{"free space taken by other uploads", nil, func(t *testing.T, e *env) response {
			c := e.client("user2")
			id := c.mustCreate(e.job("user2").ID, "a.mkv", testMax)
			c.send(id, randomData(testChunk), 0)
			// Free space counts what the other upload still needs, not what it has.
			e.free.Store(testMargin + testMax + (testMax - testChunk) - 1)
			r, _ := e.client("user1").create(e.job("user1").ID, "b.mkv", testMax)
			return r
		}, 507, "insufficient_space"},
		{"sentinel missing", func(c *config.Config) {
			c.Paths.Sentinels = []string{filepath.Join(c.Paths.IntakeRoot, ".mounted")}
		}, create("a.mkv", 10), 503, "storage_unavailable"},
		{"sentinel missing on HEAD", func(c *config.Config) {
			c.Paths.Sentinels = []string{filepath.Join(c.Paths.IntakeRoot, ".mounted")}
		}, func(t *testing.T, e *env) response {
			return e.client("user1").head(strings.Repeat("c", 32))
		}, 503, ""},
		{"not signed in", nil, createAs("", own, "a.mkv", "10"), 403, "forbidden"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			e := newEnv(t, tc.mod)
			r := tc.run(t, e)
			// An empty code: a HEAD response, which has no body.
			if r.status != tc.status || r.code() != tc.code {
				t.Fatalf("got %d %q (%s), want %d %q", r.status, r.code(), r.body, tc.status, tc.code)
			}
			if r.header.Get("Content-Type") != "application/json" {
				t.Fatalf("Content-Type %q", r.header.Get("Content-Type"))
			}
		})
	}
}

func TestRefusedCreationLeavesNothing(t *testing.T) {
	e := newEnv(t, nil)
	j := e.job("user1")
	e.free.Store(0)
	if r, _ := e.client("user1").create(j.ID, "a.mkv", 10); r.status != http.StatusInsufficientStorage {
		t.Fatalf("status %d", r.status)
	}
	ups, _ := e.st.Uploads(context.Background(), "", "")
	entries, _ := os.ReadDir(filepath.Join(e.root, ".incoming"))
	if len(ups) != 0 || len(entries) != 0 {
		t.Fatalf("rows %v, files %v", ups, entries)
	}
}

func TestOtherUsersUploadIsNotFound(t *testing.T) {
	e := newEnv(t, nil)
	j := e.job("user1")
	data := randomData(2 * testChunk)
	id := e.client("user1").mustCreate(j.ID, "film.mkv", int64(len(data)))
	e.client("user1").send(id, data[:testChunk], 0)

	other := e.client("user2")
	admin := &tusClient{t: t, base: e.srv.URL, user: "admin1", admin: true}
	for _, c := range []*tusClient{other, admin} {
		for _, r := range []response{c.head(id), c.patch(id, testChunk, data[testChunk:]), c.del(id)} {
			if r.status != http.StatusNotFound || (r.body != "" && r.code() != "not_found") {
				t.Fatalf("%s: %d %s", c.user, r.status, r.body)
			}
		}
	}
	// Nothing changed for the owner.
	c := e.client("user1")
	if r := c.head(id); r.status != http.StatusOK || r.offset() != testChunk {
		t.Fatalf("HEAD by the owner: %d offset %d", r.status, r.offset())
	}
	c.send(id, data, testChunk)
}

func TestTerminate(t *testing.T) {
	e := newEnv(t, nil)
	j := e.job("user1")
	c := e.client("user1")
	id := c.mustCreate(j.ID, "film.mkv", 2*testChunk)
	c.send(id, randomData(testChunk), 0)

	if r := c.del(id); r.status != http.StatusNoContent {
		t.Fatalf("DELETE: %d %s", r.status, r.body)
	}
	if _, ok := e.uploadRow(id); ok || exists(e.incoming(id)) || exists(e.incoming(id)+".info") {
		t.Fatal("the upload is still there")
	}
	if r := c.head(id); r.status != http.StatusNotFound {
		t.Fatalf("HEAD: %d", r.status)
	}
	if r := c.del(id); r.status != http.StatusNotFound {
		t.Fatalf("second DELETE: %d", r.status)
	}
	// The name is free again.
	c.mustCreate(j.ID, "film.mkv", 10)
}

func TestTerminateAfterDroppedConnection(t *testing.T) {
	e := newEnv(t, nil)
	j := e.job("user1")
	c := e.client("user1")
	id := c.mustCreate(j.ID, "film.mkv", testChunk)
	conn := e.rawPatch("user1", id, 0, testChunk, randomData(100))
	_ = conn.Close()
	e.waitData(id, 100)
	if r := c.del(id); r.status != http.StatusNoContent {
		t.Fatalf("DELETE: %d %s", r.status, r.body)
	}
	if exists(e.incoming(id)) || exists(e.incoming(id)+".info") {
		t.Fatal("data left")
	}
	if _, ok := e.uploadRow(id); ok {
		t.Fatal("row left")
	}
}

func TestOptions(t *testing.T) {
	e := newEnv(t, func(c *config.Config) { c.Server.PublicBaseURL = "https://fukku.example.com/" })
	c := e.client("user1")
	r := c.do(http.MethodOptions, BasePath, map[string]string{"Origin": "https://other.example.com"}, nil)
	if r.status != http.StatusOK || r.header.Get("Tus-Extension") != "creation,termination,expiration" ||
		r.header.Get("Tus-Version") != "1.0.0" || r.header.Get("Tus-Max-Size") != strconv.Itoa(testMax) {
		t.Fatalf("OPTIONS: %d %v", r.status, r.header)
	}
	r, _ = c.create(e.job("user1").ID, "a.mkv", 10)
	id := path.Base(r.header.Get("Location"))
	for _, resp := range []response{r, c.do(http.MethodPost, BasePath, map[string]string{"Origin": "https://other.example.com"}, nil)} {
		for k := range resp.header {
			if strings.HasPrefix(k, "Access-Control-") {
				t.Fatalf("CORS header %s", k)
			}
		}
	}
	// With server.publicBaseUrl set, Location is built from it, never from the request.
	if want := "https://fukku.example.com/api/uploads/" + id; r.header.Get("Location") != want || !jobdir.ValidID(id) {
		t.Fatalf("Location %q, want %q", r.header.Get("Location"), want)
	}
}

func TestMethodOverrideIsIgnored(t *testing.T) {
	// tusd would turn a POST with X-HTTP-Method-Override into that method after the checks
	// here had treated it as a creation. The header is dropped: the POST creates an upload.
	e := newEnv(t, nil)
	j := e.job("user1")
	for _, m := range []string{http.MethodPatch, http.MethodDelete} {
		r := e.client("user1").do(http.MethodPost, BasePath, map[string]string{
			"Upload-Length":          "10",
			"Upload-Metadata":        metadata("filename", "a-"+m+".mkv", "jobId", j.ID),
			"X-HTTP-Method-Override": m,
		}, nil)
		id := path.Base(r.header.Get("Location"))
		if r.status != http.StatusCreated || !jobdir.ValidID(id) {
			t.Fatalf("override %s: %d %s", m, r.status, r.body)
		}
		if _, ok := e.uploadRow(id); !ok {
			t.Fatalf("override %s: no upload row", m)
		}
	}
}

func TestSentinelsGateTheIntakeRoot(t *testing.T) {
	var sentinel string
	e := newEnv(t, func(c *config.Config) {
		sentinel = filepath.Join(c.Paths.IntakeRoot, ".mounted")
		c.Paths.Sentinels = []string{sentinel}
	})
	// Start-up created nothing under the missing mount.
	for _, d := range []string{".incoming", "jobs"} {
		if exists(filepath.Join(e.root, d)) {
			t.Fatalf("%s was created without the sentinel", d)
		}
	}
	if err := os.WriteFile(sentinel, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	c := e.client("user1")
	j := e.job("user1")
	id := c.mustCreate(j.ID, "a.srt", 3)
	c.send(id, []byte("abc"), 0)
	if !exists(e.jobFile(j, "files/a.srt")) {
		t.Fatal("not completed")
	}
}

func TestLostFinalResponse(t *testing.T) {
	// The data is complete but the upload was not finished (the last PATCH's answer was lost
	// before the move): HEAD finishes it and reports it complete.
	e := newEnv(t, nil)
	j := e.job("user1")
	c := e.client("user1")
	data := randomData(testChunk + 5)
	id := c.mustCreate(j.ID, "film.mkv", int64(len(data)))
	c.send(id, data[:testChunk], 0)
	appendFile(t, e.incoming(id), data[testChunk:])

	r := c.head(id)
	if r.status != http.StatusOK || r.offset() != int64(len(data)) || r.header.Get("Upload-Length") != strconv.Itoa(len(data)) {
		t.Fatalf("HEAD: %d %v", r.status, r.header)
	}
	if !bytes.Equal(readFile(t, e.jobFile(j, "files/film.mkv")), data) || len(e.files(j)) != 1 {
		t.Fatal("not completed")
	}
}

func appendFile(t *testing.T, path string, data []byte) {
	t.Helper()
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_APPEND, 0)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.Write(data); err != nil {
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
}

// TestReconcile simulates a crash at each step of completing an upload, and one with the
// data gone, then restarts.
func TestReconcile(t *testing.T) {
	steps := []struct {
		name  string
		crash func(t *testing.T, e *env, j store.Job, id, dst string)
	}{
		{"data complete, not moved", func(*testing.T, *env, store.Job, string, string) {}},
		{"linked, not unlinked", func(t *testing.T, e *env, _ store.Job, id, dst string) {
			mustLink(t, e.incoming(id), dst)
		}},
		{"moved, no file row", func(t *testing.T, e *env, _ store.Job, id, dst string) {
			mustLink(t, e.incoming(id), dst)
			_ = os.Remove(e.incoming(id))
		}},
		{"file row, upload row left", func(t *testing.T, e *env, j store.Job, id, dst string) {
			mustLink(t, e.incoming(id), dst)
			_ = os.Remove(e.incoming(id))
			if _, err := e.st.AddFile(context.Background(), store.File{JobID: j.ID, Path: "files/film.mkv", Kind: store.KindVideo,
				Size: testChunk + 5, Status: store.FilePending}); err != nil {
				t.Fatal(err)
			}
		}},
	}
	for _, s := range steps {
		t.Run(s.name, func(t *testing.T) {
			e := newEnv(t, nil)
			j := e.job("user1")
			c := e.client("user1")
			data := randomData(testChunk + 5)
			id := c.mustCreate(j.ID, "film.mkv", int64(len(data)))
			c.send(id, data[:testChunk], 0)
			appendFile(t, e.incoming(id), data[testChunk:])
			dst := e.jobFile(j, "files/film.mkv")
			if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
				t.Fatal(err)
			}
			s.crash(t, e, j, id, dst)

			e.restart()
			if !bytes.Equal(readFile(t, dst), data) {
				t.Fatal("content differs")
			}
			files := e.files(j)
			if len(files) != 1 || files[0].Path != "files/film.mkv" || files[0].Status != store.FilePending {
				t.Fatalf("files %+v", files)
			}
			if _, ok := e.uploadRow(id); ok || exists(e.incoming(id)) || exists(e.incoming(id)+".info") {
				t.Fatal("the upload is still there")
			}
		})
	}

	t.Run("data gone", func(t *testing.T) {
		e := newEnv(t, nil)
		j := e.job("user1")
		id := e.client("user1").mustCreate(j.ID, "film.mkv", 100)
		if err := os.Remove(e.incoming(id)); err != nil {
			t.Fatal(err)
		}
		e.restart()
		if _, ok := e.uploadRow(id); ok || exists(e.incoming(id)+".info") {
			t.Fatal("the row of an upload without data is still there")
		}
	})

	t.Run("unfinished data kept, offset corrected", func(t *testing.T) {
		e := newEnv(t, nil)
		j := e.job("user1")
		c := e.client("user1")
		data := randomData(2 * testChunk)
		id := c.mustCreate(j.ID, "film.mkv", int64(len(data)))
		c.send(id, data[:testChunk], 0)
		appendFile(t, e.incoming(id), data[testChunk:testChunk+10])
		e.restart()
		if u, ok := e.uploadRow(id); !ok || u.Offset != testChunk+10 {
			t.Fatalf("row %+v", u)
		}
		c = e.client("user1")
		c.send(id, data, testChunk+10)
		if !bytes.Equal(readFile(t, e.jobFile(j, "files/film.mkv")), data) {
			t.Fatal("content differs")
		}
	})
}

func mustLink(t *testing.T, src, dst string) {
	t.Helper()
	if err := os.Link(src, dst); err != nil {
		t.Fatal(err)
	}
}

func TestNeverReplacesAFile(t *testing.T) {
	// Something else took the name in the job directory: the upload is not finished over it.
	e := newEnv(t, nil)
	j := e.job("user1")
	c := e.client("user1")
	id := c.mustCreate(j.ID, "film.mkv", 3)
	dst := e.jobFile(j, "files/film.mkv")
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(dst, []byte("old"), 0o600); err != nil {
		t.Fatal(err)
	}
	r := c.patch(id, 0, []byte("new"))
	if r.status != http.StatusConflict || r.code() != "duplicate_name" {
		t.Fatalf("PATCH: %d %s", r.status, r.body)
	}
	if string(readFile(t, dst)) != "old" {
		t.Fatal("the file was replaced")
	}
}

func TestLargeUploadOffsets(t *testing.T) {
	if testing.Short() {
		t.Skip("writes a sparse file above 4 GiB")
	}
	const size = 4<<30 + 2*testChunk
	e := newEnv(t, func(c *config.Config) {
		c.Upload.MaxFileSize = 5 << 30
		c.Upload.MaxInFlight = 5 << 30
	})
	j := e.job("user1")
	c := e.client("user1")
	id := c.mustCreate(j.ID, "big.mkv", size)
	// The first 4 GiB and a bit have arrived (a hole, so the test stays small on disk).
	have := int64(size - testChunk)
	if err := os.Truncate(e.incoming(id), have); err != nil {
		t.Fatal(err)
	}
	if r := c.head(id); r.status != http.StatusOK || r.offset() != have || r.header.Get("Upload-Length") != strconv.Itoa(size) {
		t.Fatalf("HEAD: %d %v", r.status, r.header)
	}
	if u, _ := e.uploadRow(id); u.Offset != have {
		t.Fatalf("row offset %d, want %d", u.Offset, have)
	}
	tail := randomData(testChunk)
	if r := c.patch(id, have, tail); r.status != http.StatusNoContent || r.offset() != size {
		t.Fatalf("PATCH: %d offset %d %s", r.status, r.offset(), r.body)
	}
	path := e.jobFile(j, "files/big.mkv")
	f, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = f.Close() }()
	got := make([]byte, testChunk)
	if _, err := f.ReadAt(got, have); err != nil || !bytes.Equal(got, tail) {
		t.Fatalf("tail differs (%v)", err)
	}
	if files := e.files(j); len(files) != 1 || files[0].Size != size {
		t.Fatalf("files %+v", files)
	}
}
