package fixtures

import (
	"context"
	"errors"
	"fmt"
	"math"
	"slices"
	"time"
)

// Sync analysis of a file made from the generated media: the white flash in the top sixth
// of the picture and the beep come once a second, together, so the time from each flash
// to the nearest beep says how far the audio is from the video (plan §18 item 7). It
// decodes with ffmpeg, so a test can assert what a person would see and hear.

const (
	syncFPS        = 100  // video frames sampled per second (the flash is found to 10 ms, within a source frame)
	syncRate       = 8000 // audio samples per second
	syncWindow     = 80   // samples per audio window: 10 ms
	flashLuma      = 200  // mean luma of the top band during a flash (white, 235) and above the test pattern
	beepShare      = 0.4  // a window is a beep above this share of the loudest window's RMS
	silence        = 0.01 // RMS, of full scale, below which nothing is a beep
	minSyncSamples = 2
)

// SyncReport is what SyncOffset measured.
type SyncReport struct {
	Offset  time.Duration // median of (nearest beep − flash): positive when the audio is late
	Flashes int
	Beeps   int
}

// SyncOffset measures the audio's offset against the video in path, from the flash and
// beep onsets of its first video and first audio stream. The offset is ambiguous by
// whole seconds: one 800 ms late is reported as 200 ms early.
func SyncOffset(ctx context.Context, path string) (SyncReport, error) {
	video, err := output(ctx, "ffmpeg", "-v", "error", "-i", path, "-map", "0:v:0",
		"-vf", fmt.Sprintf("crop=iw:ih/6:0:0,scale=8:2,format=gray,fps=%d:start_time=0", syncFPS),
		"-f", "rawvideo", "pipe:1")
	if err != nil {
		return SyncReport{}, err
	}
	audio, err := output(ctx, "ffmpeg", "-v", "error", "-i", path, "-map", "0:a:0",
		"-af", "aresample=async=1:first_pts=0", "-ac", "1", "-ar", fmt.Sprint(syncRate),
		"-f", "s16le", "pipe:1")
	if err != nil {
		return SyncReport{}, err
	}
	const frame = 16 // 8×2 gray pixels
	var flashes []time.Duration
	on := false
	for i := 0; i+frame <= len(video); i += frame {
		sum := 0
		for _, b := range video[i : i+frame] {
			sum += int(b)
		}
		bright := sum/frame >= flashLuma
		if bright && !on {
			flashes = append(flashes, time.Duration(i/frame)*time.Second/syncFPS)
		}
		on = bright
	}
	var rms []float64
	loudest := 0.0
	for i := 0; i+2*syncWindow <= len(audio); i += 2 * syncWindow {
		var sq float64
		for j := i; j < i+2*syncWindow; j += 2 {
			s := float64(int16(uint16(audio[j])|uint16(audio[j+1])<<8)) / 32768
			sq += s * s
		}
		r := math.Sqrt(sq / syncWindow)
		rms = append(rms, r)
		loudest = max(loudest, r)
	}
	var beeps []time.Duration
	on = false
	for i, r := range rms {
		loud := r >= max(silence, beepShare*loudest)
		if loud && !on {
			beeps = append(beeps, time.Duration(i)*time.Second*syncWindow/syncRate)
		}
		on = loud
	}
	rep := SyncReport{Flashes: len(flashes), Beeps: len(beeps)}
	if len(flashes) < minSyncSamples || len(beeps) < minSyncSamples {
		return rep, errors.New("fixtures: too few flashes or beeps to measure sync")
	}
	diffs := make([]time.Duration, 0, len(flashes))
	for _, f := range flashes {
		best := beeps[0] - f
		for _, b := range beeps[1:] {
			if d := b - f; d.Abs() < best.Abs() {
				best = d
			}
		}
		diffs = append(diffs, best)
	}
	slices.Sort(diffs)
	rep.Offset = diffs[len(diffs)/2]
	return rep, nil
}
