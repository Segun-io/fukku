package arr

import (
	"encoding/json"
	"testing"
)

func TestParseRunTime(t *testing.T) {
	for in, want := range map[string]int64{
		`"9:00"`:           540_000,
		`"59:59"`:          3_599_000,
		`"00:09:56"`:       596_000,
		`"1:23:45"`:        5_025_000,
		`"00:00:01.5"`:     1_500,
		`"1.02:03:04"`:     93_784_000,
		`"garbage"`:        0,
		`""`:               0,
		`null`:             0,
		`120`:              120_000,
		`{"unexpected":1}`: 0,
	} {
		if got := parseRunTime(json.RawMessage(in)); got != want {
			t.Errorf("parseRunTime(%s) = %d, want %d", in, got, want)
		}
	}
}
