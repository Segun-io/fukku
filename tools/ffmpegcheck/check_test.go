package main

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const index = `<a href="ffmpeg-7.1.tar.xz">x</a> <a href="ffmpeg-7.1.4.tar.xz">x</a> <a href="ffmpeg-7.1.5.tar.xz">x</a>
<a href="ffmpeg-7.1.5.tar.xz.asc">x</a> <a href="ffmpeg-7.1.5.tar.bz2">x</a> <a href="ffmpeg-8.0.3.tar.xz">x</a>
<a href="ffmpeg-8.1.3.tar.xz">x</a> <a href="ffmpeg-8.1.2.tar.xz">x</a> <a href="ffmpeg-snapshot.tar.bz2">x</a> <a href="ffmpeg-6.1.4.tar.xz">x</a>`

func ver(t *testing.T, s string) version {
	t.Helper()
	got, err := parseVersion(s)
	if err != nil {
		t.Fatal(err)
	}
	return got
}

func TestParseVersion(t *testing.T) {
	if got := ver(t, "7.1"); got != (version{7, 1, 0}) {
		t.Fatalf("7.1 parsed as %v", got)
	}
	if got := ver(t, "7.1.5").String(); got != "7.1.5" {
		t.Fatal(got)
	}
	for _, bad := range []string{"", "7", "7.1.5.1", "n7.1", "snapshot"} {
		if _, err := parseVersion(bad); err == nil {
			t.Errorf("%q accepted", bad)
		}
	}
}

func TestReleasesFromIndex(t *testing.T) {
	var got []string
	for _, r := range releasesFromIndex(index) {
		got = append(got, r.String())
	}
	want := "6.1.4 7.1 7.1.4 7.1.5 8.0.3 8.1.2 8.1.3"
	if strings.Join(got, " ") != want {
		t.Fatalf("got %v, want %s", got, want)
	}
}

func TestEvaluate(t *testing.T) {
	rel := releasesFromIndex(index)
	cur, err := evaluate(ver(t, "7.1.5"), rel)
	if err != nil || cur.Behind {
		t.Fatalf("current pin: %+v %v", cur, err)
	}
	if len(cur.NewerLines) != 2 || cur.NewerLines[0].String() != "8.0.3" || cur.NewerLines[1].String() != "8.1.3" {
		t.Fatalf("newer lines: %v", cur.NewerLines)
	}
	old, err := evaluate(ver(t, "7.1.4"), rel)
	if err != nil || !old.Behind || old.LatestLine.String() != "7.1.5" {
		t.Fatalf("behind pin: %+v %v", old, err)
	}
	// 7.1 is 7.1.0: behind 7.1.5 too.
	if r, _ := evaluate(ver(t, "7.1"), rel); !r.Behind {
		t.Fatal("7.1.0 should be behind")
	}
	last, _ := evaluate(ver(t, "8.1.3"), rel)
	if last.Behind || len(last.NewerLines) != 0 {
		t.Fatalf("newest pin: %+v", last)
	}
	if _, err := evaluate(ver(t, "7.1.9"), rel); err == nil {
		t.Fatal("a pin missing from the index must be an error")
	}
}

func TestPinFromDockerfile(t *testing.T) {
	got, err := pinFromDockerfile("FROM x\nARG FFMPEG_VERSION=7.1.5\nARG FFMPEG_SHA256=ab\n")
	if err != nil || got.String() != "7.1.5" {
		t.Fatalf("%v %v", got, err)
	}
	if _, err := pinFromDockerfile("FROM x\n"); err == nil {
		t.Fatal("missing pin accepted")
	}
}

func TestRun(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte(index)) }))
	defer srv.Close()
	dir := t.TempDir()
	write := func(v string) string {
		p := filepath.Join(dir, "Dockerfile"+v)
		if err := os.WriteFile(p, []byte("ARG FFMPEG_VERSION="+v+"\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		return p
	}
	var out, errb bytes.Buffer
	if code := run(write("7.1.5"), srv.URL, &out, &errb); code != 0 || !strings.Contains(out.String(), "::warning::") || strings.Contains(out.String(), "::error::") {
		t.Fatalf("current pin: code %d out %q err %q", code, out.String(), errb.String())
	}
	out.Reset()
	if code := run(write("7.1.4"), srv.URL, &out, &errb); code != 1 || !strings.Contains(out.String(), "::error::") {
		t.Fatalf("behind pin: code %d out %q", code, out.String())
	}
	if code := run(filepath.Join(dir, "missing"), srv.URL, &out, &errb); code != 2 {
		t.Fatalf("missing Dockerfile: code %d", code)
	}
}
