package media

import "testing"

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
	if m := raw.toProbe(1); m.DurationMs != maxDurationMs {
		t.Errorf("format duration %d", m.DurationMs)
	}
	if got := secondsMs("20.020000"); got != 20020 {
		t.Errorf("ordinary duration %d", got)
	}
}
