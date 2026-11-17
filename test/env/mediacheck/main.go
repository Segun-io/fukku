// Command mediacheck measures media for the end-to-end tests (test/e2e), which run in the
// Playwright image: it has no ffmpeg, and its Chromium cannot decode the H.264 and AAC of the
// sync-check clips. dev-up builds it into .cache/bin and the dev image runs it (it has ffmpeg
// and mkvmerge), as service mediacheck of test/env/compose.yaml. It makes no outbound
// connection and changes no file. It is for local tests only.
//
//	POST /sync             the body is a clip (at most 64 MiB): how far its first audio track
//	                       is from its first video track, measured as the #53 tests do it
//	                       (fixtures.SyncOffset: the flash against the beep), as
//	                       {"offsetMs": -200, "flashes": 20, "beeps": 20}; 422 {"error": "…"}
//	                       when there is too little to measure
//	GET  /identify?path=   mkvmerge's identification (mkvmerge -J) of a file under -data,
//	                       as mkvmerge prints it
//	GET  /healthz          200
package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/segun-io/fukku/test/fixtures"
)

// maxClip is the largest body POST /sync takes: a 20 s clip at the preview's rates is about
// 2 MiB, a full-length one at most a few tens.
const maxClip = 64 << 20

func main() {
	os.Exit(run(os.Args[1:]))
}

func run(args []string) int {
	set := flag.NewFlagSet("mediacheck", flag.ContinueOnError)
	listen := set.String("listen", ":8080", "address to listen on")
	data := set.String("data", "/data", "the only folder GET /identify reads from")
	health := set.String("healthcheck", "", "check the service at this address and exit")
	if err := set.Parse(args); err != nil {
		return 2
	}
	if *health != "" {
		return healthcheck(*health)
	}
	srv := &http.Server{Addr: *listen, Handler: handler(*data), ReadHeaderTimeout: 10 * time.Second}
	errc := make(chan error, 1)
	go func() { errc <- srv.ListenAndServe() }()
	log.Printf("mediacheck: listening on %s, files under %s", *listen, *data)
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	code := 0
	select {
	case err := <-errc:
		log.Printf("mediacheck: %v", err)
		code = 1
	case <-ctx.Done():
	}
	_ = srv.Close()
	return code
}

func healthcheck(addr string) int {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "http://"+addr+"/healthz", nil)
	if err != nil {
		return 1
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return 1
	}
	return 0
}

func handler(data string) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) })
	mux.HandleFunc("POST /sync", syncCheck)
	mux.HandleFunc("GET /identify", func(w http.ResponseWriter, r *http.Request) { identify(w, r, data) })
	return mux
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func fail(w http.ResponseWriter, status int, err error) {
	writeJSON(w, status, map[string]string{"error": err.Error()})
}

type syncJSON struct {
	OffsetMs int64 `json:"offsetMs"`
	Flashes  int   `json:"flashes"`
	Beeps    int   `json:"beeps"`
}

func syncCheck(w http.ResponseWriter, r *http.Request) {
	f, err := os.CreateTemp("", "clip-*.mp4")
	if err != nil {
		fail(w, http.StatusInternalServerError, err)
		return
	}
	defer func() { _ = os.Remove(f.Name()) }()
	_, err = io.Copy(f, http.MaxBytesReader(w, r.Body, maxClip))
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		fail(w, http.StatusBadRequest, fmt.Errorf("read the clip: %w", err))
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 2*time.Minute)
	defer cancel()
	rep, err := fixtures.SyncOffset(ctx, f.Name())
	if err != nil {
		writeJSON(w, http.StatusUnprocessableEntity, map[string]any{
			"error": err.Error(), "flashes": rep.Flashes, "beeps": rep.Beeps,
		})
		return
	}
	writeJSON(w, http.StatusOK, syncJSON{OffsetMs: rep.Offset.Milliseconds(), Flashes: rep.Flashes, Beeps: rep.Beeps})
}

// under reports whether p is a clean absolute path inside dir.
func under(p, dir string) bool {
	if !filepath.IsAbs(p) || filepath.Clean(p) != p {
		return false
	}
	rel, err := filepath.Rel(dir, p)
	return err == nil && rel != "." && rel != ".." && !strings.HasPrefix(rel, "../")
}

func identify(w http.ResponseWriter, r *http.Request, data string) {
	p := r.URL.Query().Get("path")
	if !under(p, data) {
		fail(w, http.StatusBadRequest, fmt.Errorf("path must be a clean absolute path under %s", data))
		return
	}
	// The real file, still under the folder: no symbolic link leads out of it.
	real, err := filepath.EvalSymlinks(p)
	if err == nil && !under(real, data) {
		err = errors.New("the path leads outside " + data)
	}
	var info os.FileInfo
	if err == nil {
		info, err = os.Stat(real)
	}
	if err == nil && !info.Mode().IsRegular() {
		err = errors.New("not a regular file")
	}
	if err != nil {
		fail(w, http.StatusNotFound, err)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), time.Minute)
	defer cancel()
	// Without the charsets a name such as "Español (Latino)" is cut at its first non-ASCII
	// character in a C locale.
	out, err := exec.CommandContext(ctx, "mkvmerge", "--ui-language", "en_US",
		"--command-line-charset", "UTF-8", "--output-charset", "UTF-8", "-J", real).Output()
	// mkvmerge -J exits 0, or 1 for warnings, with its JSON on stdout.
	var ee *exec.ExitError
	warned := errors.As(err, &ee) && ee.ExitCode() == 1 && json.Valid(out)
	if err != nil && !warned {
		fail(w, http.StatusUnprocessableEntity, fmt.Errorf("mkvmerge -J: %w", err))
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_, _ = w.Write(out)
}
