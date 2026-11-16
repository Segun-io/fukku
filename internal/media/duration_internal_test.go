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
