package media

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
	"unicode/utf8"
)

func TestStreamDuration(t *testing.T) {
	for _, tc := range []struct {
		duration string
		tags     map[string]string
		want     int64
	}{
		{"20.020000", nil, 20020},
		{"", map[string]string{"DURATION": "00:00:20.020000000"}, 20020},
		{"", map[string]string{"DURATION-eng": "01:30:00.500000000"}, 5_400_500},
		{"", map[string]string{"duration": "00:01:00.000"}, 60_000},
		{"N/A", map[string]string{"DURATION": "garbage"}, 0},
		{"", map[string]string{"DURATION": "00:61:00.0"}, 0},
		{"", map[string]string{"DURATION": "1:2"}, 0},
		{"-1", nil, 0},
		{"inf", nil, 0},
		{"", nil, 0},
		{"12.5", map[string]string{"DURATION": "00:00:20.0"}, 12500}, // ffprobe's own wins
	} {
		if got := streamDuration(tc.duration, tc.tags); got != tc.want {
			t.Errorf("streamDuration(%q, %v) = %d, want %d", tc.duration, tc.tags, got, tc.want)
		}
	}
}

// Durations beyond the int64 range are capped, not converted undefined.
func TestHugeDurationsAreCapped(t *testing.T) {
	for _, s := range []string{"1e300", "9.3e15", "1.7976931348623157e308"} {
		if got := secondsMs(s); got != maxDurationMs {
			t.Errorf("secondsMs(%q) = %d, want the cap %d", s, got, int64(maxDurationMs))
		}
	}
	for _, s := range []string{"99999999999:00:00.0", "2147483647:59:59.9"} {
		if got := clockMs(s); got != maxDurationMs {
			t.Errorf("clockMs(%q) = %d, want the cap", s, got)
		}
	}
	if got := clockMs("00:00:NaN"); got != 0 {
		t.Errorf("clockMs NaN = %d", got)
	}
	raw := rawProbe{}
	raw.Format.Duration = "1e300"
	if m, _ := raw.toProbe(1, Options{}.limits()); m.DurationMs != maxDurationMs {
		t.Errorf("format duration %d", m.DurationMs)
	}
	if got := secondsMs("20.020000"); got != 20020 {
		t.Errorf("ordinary duration %d", got)
	}
}

// What a probe keeps of a file is bounded, whatever the file holds.
func TestProbeKeepsBoundedStrings(t *testing.T) {
	huge := strings.Repeat("é", 2<<20) // 4 MiB of two-byte characters
	raw := rawProbe{}
	raw.Format.FormatName = huge
	raw.Streams = hostileStreams(defaultMaxStreamsPerFile, huge)
	m, tooMany := raw.toProbe(1, Options{}.limits())
	if tooMany || len(m.Audio) != defaultMaxStreamsPerFile {
		t.Fatalf("%d audio tracks kept, too many: %v", len(m.Audio), tooMany)
	}
	a := m.Audio[0]
	if len(a.Title) != defaultMaxTitleBytes || len(a.Language) != defaultMaxTagBytes || len(a.Codec) != defaultMaxTagBytes ||
		len(m.Container) != defaultMaxTagBytes {
		t.Errorf("title %d, language %d, codec %d, container %d bytes", len(a.Title), len(a.Language), len(a.Codec), len(m.Container))
	}
	if !utf8.ValidString(a.Title) {
		t.Errorf("title cut inside a character")
	}
	// The settings change the bounds.
	raw.Streams = raw.Streams[:5]
	m, tooMany = raw.toProbe(1, Options{MaxTitleBytes: 10, MaxTagBytes: 5, MaxStreamsPerFile: 5}.limits())
	if tooMany || len(m.Audio[0].Title) != 10 || len(m.Audio[0].Language) != 4 { // 5 bytes cut to a boundary
		t.Errorf("custom bounds: title %d, language %d", len(m.Audio[0].Title), len(m.Audio[0].Language))
	}
}

// A file with more tracks than the cap is flagged, never cut short: every track of one
// that fits is kept, and cover art and other streams do not count.
func TestTooManyStreamsAreNotTruncated(t *testing.T) {
	raw := rawProbe{Streams: hostileStreams(defaultMaxStreamsPerFile, "x")}
	m, tooMany := raw.toProbe(1, Options{}.limits())
	if tooMany || len(m.Audio) != defaultMaxStreamsPerFile {
		t.Fatalf("a file at the cap: %d tracks, too many %v", len(m.Audio), tooMany)
	}
	raw.Streams = append(raw.Streams, hostileStreams(500, "x")[0])
	raw.Streams[len(raw.Streams)-1].CodecType = "attachment"
	if _, tooMany := raw.toProbe(1, Options{}.limits()); tooMany {
		t.Errorf("an attachment counted as a track")
	}
	raw.Streams[len(raw.Streams)-1].CodecType = "subtitle"
	if _, tooMany := raw.toProbe(1, Options{}.limits()); !tooMany {
		t.Errorf("a file over the cap was not flagged")
	}
	// 31 subtitle tracks plus video and audio: what the old cap of 32 streams broke.
	raw = rawProbe{Streams: hostileStreams(1, "x")}
	raw.Streams[0].CodecType = "video"
	raw.Streams = append(raw.Streams, hostileStreams(40, "x")...)
	raw.Streams[1].CodecType = "audio"
	for i := 2; i < len(raw.Streams); i++ {
		raw.Streams[i].CodecType = "subtitle"
	}
	m, tooMany = raw.toProbe(1, Options{}.limits())
	if tooMany || len(m.Video) != 1 || len(m.Audio) != 1 || len(m.Subtitles) != 39 {
		t.Errorf("%d video, %d audio, %d subtitles, too many %v", len(m.Video), len(m.Audio), len(m.Subtitles), tooMany)
	}
}

func hostileStreams(n int, s string) []rawStream {
	var out []rawStream
	for i := range n {
		out = append(out, rawStream{Index: i, CodecType: "audio", CodecName: s,
			Tags: map[string]string{"title": s, "language": s}})
	}
	return out
}

// The result of a probe task is as large as its JSON, escapes included: characters JSON
// writes as six bytes ('<', '>', '&', control characters) are the worst case. Files past
// the byte budget are refused, so the whole stays under what the spool reads.
func TestProbeResultStaysWithinTheBudget(t *testing.T) {
	// A fake ffprobe that reports the worst case a file can make within the string bounds.
	worst := strings.Repeat("<", 200) + strings.Repeat("\x01", 56)
	var streams []string
	for i := range defaultMaxStreamsPerFile - 1 {
		w, _ := json.Marshal(worst)
		streams = append(streams, fmt.Sprintf(`{"index":%d,"codec_type":"audio","codec_name":%s,"channels":2,"tags":{"title":%s,"language":%s}}`,
			i, w, w, w))
	}
	out := `{"format":{"format_name":"matroska","duration":"60.0"},"streams":[{"index":999,"codec_type":"video","codec_name":"h264","width":1,"height":1,"avg_frame_rate":"24/1"},` +
		strings.Join(streams, ",") + `]}`
	dir := t.TempDir()
	script := filepath.Join(dir, "ffprobe")
	if err := os.WriteFile(script, []byte("#!/bin/sh\ncat <<'EOF'\n"+out+"\nEOF\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	root, err := os.OpenRoot(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = root.Close() }()
	var paths []string
	for i := range 60 {
		name := fmt.Sprintf("f%d.mkv", i)
		if err := os.WriteFile(filepath.Join(dir, name), []byte("x"), 0o600); err != nil {
			t.Fatal(err)
		}
		paths = append(paths, name)
	}
	opt := Options{Timeout: 30 * time.Second, VideoExt: []string{".mkv"}, FFprobe: script}
	res, err := Run(t.Context(), root, ProbeArgs{Paths: paths}, opt)
	if err != nil {
		t.Fatal(err)
	}
	ok, refused := 0, 0
	for _, f := range res.Files {
		switch {
		case f.OK:
			ok++
		case f.Refusal == RefusalResultTooLarge:
			refused++
		default:
			t.Fatalf("%s: %+v", f.Path, f)
		}
	}
	if ok == 0 || refused == 0 {
		t.Fatalf("%d files kept, %d refused for size", ok, refused)
	}
	// The task's whole result, as the worker writes it, is within the spool's read limit.
	data, err := json.Marshal(res)
	if err != nil {
		t.Fatal(err)
	}
	if limit := 16 << 20; len(data) > limit || len(data) > defaultMaxResultBytes+(64<<10) {
		t.Errorf("result of %d bytes, budget %d", len(data), defaultMaxResultBytes)
	}
}
