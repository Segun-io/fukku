package fixtures

import (
	"archive/zip"
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"
)

// SeasonZip writes a well-formed season archive: one short video per episode named
// "<show> - S<ss>E<ee>.mkv" inside "Season <ss>/", plus an English SRT for the first
// episode. Videos are stored, not deflated, as real archives of video are.
func SeasonZip(ctx context.Context, dir, show string, season, episodes int, spec Video) (string, error) {
	work, err := os.MkdirTemp(dir, ".season-")
	if err != nil {
		return "", err
	}
	defer func() { _ = os.RemoveAll(work) }()

	path := filepath.Join(dir, fmt.Sprintf("%s - Season %02d.zip", show, season))
	f, err := os.Create(path)
	if err != nil {
		return "", err
	}
	defer func() { _ = f.Close() }()
	zw := zip.NewWriter(f)
	folder := fmt.Sprintf("Season %02d/", season)
	for ep := 1; ep <= episodes; ep++ {
		name := fmt.Sprintf("%s - S%02dE%02d.mkv", show, season, ep)
		src := filepath.Join(work, name)
		if err := Generate(ctx, src, spec); err != nil {
			return "", err
		}
		if err := addFile(zw, folder+name, src, zip.Store); err != nil {
			return "", err
		}
	}
	srt, err := zw.Create(folder + fmt.Sprintf("%s - S%02dE01.en.srt", show, season))
	if err != nil {
		return "", err
	}
	if _, err := io.WriteString(srt, SRT(spec.withDefaults().Duration, "eng")); err != nil {
		return "", err
	}
	if err := zw.Close(); err != nil {
		return "", err
	}
	return path, f.Close()
}

func addFile(zw *zip.Writer, name, src string, method uint16) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer func() { _ = in.Close() }()
	w, err := zw.CreateHeader(&zip.FileHeader{Name: name, Method: method, Modified: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)})
	if err != nil {
		return err
	}
	_, err = io.Copy(w, in)
	return err
}
