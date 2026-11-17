package media

import (
	"encoding/json"
	"strings"
	"testing"
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
	if m := raw.toProbe(1, Options{}.limits()); m.DurationMs != maxDurationMs {
		t.Errorf("format duration %d", m.DurationMs)
	}
	if got := secondsMs("20.020000"); got != 20020 {
		t.Errorf("ordinary duration %d", got)
	}
}

// What a probe keeps of a file is bounded, whatever the file holds: five files with
// multi-megabyte titles must not add up to a result the spool cannot read.
func TestProbeKeepsBoundedStrings(t *testing.T) {
	type stream = struct {
		Index        int               `json:"index"`
		CodecType    string            `json:"codec_type"`
		CodecName    string            `json:"codec_name"`
		Width        int               `json:"width"`
		Height       int               `json:"height"`
		Channels     int               `json:"channels"`
		AvgFrameRate string            `json:"avg_frame_rate"`
		RFrameRate   string            `json:"r_frame_rate"`
		Duration     string            `json:"duration"`
		Tags         map[string]string `json:"tags"`
		Disposition  map[string]int    `json:"disposition"`
	}
	huge := strings.Repeat("é", 2<<20) // 4 MiB of two-byte characters
	raw := rawProbe{}
	raw.Format.FormatName = huge
	for i := range 1000 {
		raw.Streams = append(raw.Streams, stream{Index: i, CodecType: "audio", CodecName: huge,
			Tags: map[string]string{"title": huge, "language": huge}})
	}
	m := raw.toProbe(1, Options{}.limits())
	if len(m.Audio) != defaultMaxStreamsPerFile {
		t.Errorf("%d audio tracks kept, want %d", len(m.Audio), defaultMaxStreamsPerFile)
	}
	a := m.Audio[0]
	if len(a.Title) != defaultMaxTitleBytes || len(a.Language) != defaultMaxTagBytes || len(a.Codec) != defaultMaxTagBytes ||
		len(m.Container) != defaultMaxTagBytes {
		t.Errorf("title %d, language %d, codec %d, container %d bytes", len(a.Title), len(a.Language), len(a.Codec), len(m.Container))
	}
	if !utf8.ValidString(a.Title) {
		t.Errorf("title cut inside a character")
	}
	data, err := json.Marshal(m)
	if err != nil || len(data) > 64<<10 {
		t.Errorf("a probe of a hostile file is %d bytes (%v)", len(data), err)
	}
	// The settings change the bounds.
	m = raw.toProbe(1, Options{MaxTitleBytes: 10, MaxTagBytes: 5, MaxStreamsPerFile: 2}.limits())
	if len(m.Audio) != 2 || len(m.Audio[0].Title) != 10 || len(m.Audio[0].Language) != 4 { // 5 bytes cut to a boundary
		t.Errorf("custom bounds: %d tracks, title %d, language %d", len(m.Audio), len(m.Audio[0].Title), len(m.Audio[0].Language))
	}
}
