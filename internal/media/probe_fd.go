package media

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"os"
	"strconv"
	"strings"
)

// Probes of a file the caller holds open (the merge, #54): what is probed is then the very
// file the caller goes on to read, whatever happens to its name meanwhile.

// ProbeOpen probes the open video file f with the content check of the upload probe.
// formats are the ffprobe demuxers it may use; empty means the upload whitelist. name is
// only echoed in the result's Path. An error means ffprobe could not run.
func ProbeOpen(ctx context.Context, f *os.File, name string, formats []string, opt Options) (ProbedFile, error) {
	pf := ProbedFile{Path: name, Kind: "video"}
	if err := ctx.Err(); err != nil {
		return pf, err
	}
	st, err := f.Stat()
	if err != nil || !st.Mode().IsRegular() {
		pf.Refusal = RefusalUnreadable
		return pf, nil
	}
	err = probeOpen(ctx, f, st.Size(), &pf, opt, demuxers(formats))
	return pf, err
}

func demuxers(formats []string) string {
	if len(formats) == 0 {
		return formatWhitelist
	}
	return strings.Join(formats, ",")
}

// VideoEndMs measures how long the first video stream of the open file f lasts, from its
// last packets: the end of the latest packet after a seek to fromMs, less the stream's
// start time. It is for files whose video stream states no duration of its own (a Matroska
// file without statistics tags), where the container's duration may include audio that
// runs on after the picture. ok is false when no video packet was found after fromMs or the
// output cannot be read; an error means ffprobe could not run.
func VideoEndMs(ctx context.Context, f *os.File, fromMs int64, formats []string, opt Options) (ms int64, ok bool, err error) {
	from := fmt.Sprintf("%.3f%%", float64(max(fromMs, 0))/1000)
	out, refusal, err := runFFprobe(ctx, f, opt, demuxers(formats), "-select_streams", "v:0",
		"-show_entries", "packet=pts_time,duration_time:stream=start_time", "-read_intervals", from)
	if err != nil || refusal != "" {
		return 0, false, err
	}
	var raw struct {
		Packets []struct {
			PTS      string `json:"pts_time"`
			Duration string `json:"duration_time"`
		} `json:"packets"`
		Streams []struct {
			Start string `json:"start_time"`
		} `json:"streams"`
	}
	if err := json.Unmarshal(out, &raw); err != nil {
		return 0, false, nil
	}
	var start float64
	if len(raw.Streams) > 0 {
		if s := seconds(raw.Streams[0].Start); !math.IsNaN(s) {
			start = s
		}
	}
	end := math.Inf(-1)
	for _, p := range raw.Packets {
		pts, d := seconds(p.PTS), seconds(p.Duration)
		if p.PTS == "" || math.IsNaN(pts) {
			continue
		}
		if !(d > 0) { // also NaN
			d = 0
		}
		end = math.Max(end, pts+d)
	}
	if math.IsInf(end, -1) || end-start <= 0 {
		return 0, false, nil
	}
	return capMs((end - start) * 1000), true, nil
}

// seconds reads ffprobe's seconds ("12.345"); anything else ("N/A", "") is NaN.
func seconds(s string) float64 {
	v, err := strconv.ParseFloat(s, 64)
	if err != nil || math.IsInf(v, 0) {
		return math.NaN()
	}
	return v
}
