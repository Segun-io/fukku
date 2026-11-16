package fixtures

import (
	"context"
	"path/filepath"
	"testing"
	"time"
)

// SyncOffset finds the generated flash and beep together, and the audio of ShiftAudio's
// copy as late as it was shifted.
func TestSyncOffset(t *testing.T) {
	needFFmpeg(t)
	ctx := context.Background()
	dir := t.TempDir()
	base := filepath.Join(dir, "film.mkv")
	if err := Generate(ctx, base, Video{Duration: 6 * time.Second, Audio: []Audio{{Language: "eng"}}}); err != nil {
		t.Fatal(err)
	}
	late := filepath.Join(dir, "late.mkv")
	if err := ShiftAudio(ctx, base, late, 300*time.Millisecond); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		path string
		want time.Duration
	}{{base, 0}, {late, 300 * time.Millisecond}} {
		rep, err := SyncOffset(ctx, tc.path)
		if err != nil {
			t.Fatalf("%s: %v (%+v)", tc.path, err, rep)
		}
		if d := rep.Offset - tc.want; d.Abs() > 50*time.Millisecond || rep.Flashes < 5 || rep.Beeps < 5 {
			t.Errorf("%s: %+v, want an offset of %s", filepath.Base(tc.path), rep, tc.want)
		}
	}
}
