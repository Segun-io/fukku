// Command ffmpegcheck compares the FFmpeg version pinned in the Dockerfile with the upstream
// releases listed at ffmpeg.org (no credentials). It exits 1 when the pin is behind the latest
// release of its own line, and prints a GitHub Actions warning when a newer line exists.
// See docs/image.md.
package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"time"
)

func main() {
	dockerfile := flag.String("dockerfile", "Dockerfile", "Dockerfile holding `ARG FFMPEG_VERSION=`")
	url := flag.String("index", "https://ffmpeg.org/releases/", "release index to read")
	flag.Parse()
	os.Exit(run(*dockerfile, *url, os.Stdout, os.Stderr))
}

func run(dockerfile, indexURL string, stdout, stderr io.Writer) int {
	data, err := os.ReadFile(dockerfile) //nolint:gosec // a path given on the command line by the maintainer or CI
	if err != nil {
		_, _ = fmt.Fprintln(stderr, "error:", err)
		return 2
	}
	pinned, err := pinFromDockerfile(string(data))
	if err != nil {
		_, _ = fmt.Fprintln(stderr, "error:", err)
		return 2
	}
	index, err := fetch(indexURL)
	if err != nil {
		_, _ = fmt.Fprintln(stderr, "error:", err)
		return 2
	}
	releases := releasesFromIndex(index)
	if len(releases) == 0 {
		_, _ = fmt.Fprintln(stderr, "error: no releases found at", indexURL)
		return 2
	}
	res, err := evaluate(pinned, releases)
	if err != nil {
		_, _ = fmt.Fprintln(stderr, "error:", err)
		return 2
	}
	_, _ = fmt.Fprintf(stdout, "pinned ffmpeg %s; latest in its %d.%d line %s\n", res.Pinned, res.Pinned.major, res.Pinned.minor, res.LatestLine)
	for _, v := range res.NewerLines {
		_, _ = fmt.Fprintf(stdout, "::warning::a newer FFmpeg line exists: %s (pinned %s); decide whether to move (docs/image.md)\n", v, res.Pinned)
	}
	if res.Behind {
		_, _ = fmt.Fprintf(stdout, "::error::pinned ffmpeg %s is behind %s: bump FFMPEG_VERSION and FFMPEG_SHA256 (docs/image.md)\n", res.Pinned, res.LatestLine)
		return 1
	}
	return 0
}

func fetch(url string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return "", err
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("%s: %s", url, resp.Status)
	}
	b, err := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	return string(b), err
}
