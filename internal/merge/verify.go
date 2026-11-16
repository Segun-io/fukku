package merge

import (
	"fmt"
	"math"
	"strconv"
	"strings"

	"github.com/segun-io/fukku/internal/media"
)

// expectation is what the output of a merge must be, from its inputs.
type expectation struct {
	lib         media.Probe    // ffprobe of the library file (video duration measured if it had none)
	libID       identification // mkvmerge's view of it
	libDefaults []bool         // the default flag given to each library track, by position in libID.Tracks
	up          media.Probe    // ffprobe of the upload
	added       []track        // the upload's chosen tracks, in id order
	offsetMs    int64
	tag, name   string
	keepDefault bool
	coverageMs  int64 // how much shorter than the film an added audio track may be
}

// verify checks the output of a merge against the plan's rules (§8 2a) and returns what
// fails, empty when it passes:
//
//   - the same video: as many video streams as the library file, each with its codec and
//     resolution, a stream duration within one frame of the library's (and the same frame
//     count where both files carry mkvmerge's statistics). When the library file's video
//     stream has no duration of its own and its last packets could not be measured, only
//     a weaker check is left: the output's video is not longer than the library file;
//   - the duration unchanged: the output is not shorter than the library file by more than
//     a frame, and not longer than its longest track can make it (the library file, or the
//     upload shifted by the offset);
//   - track count = original + added, as ffprobe and as mkvmerge count them;
//   - the library file's tracks first, in their order and unchanged (type, codec, language
//     with none read as "und", name), each with the default flag it was given;
//   - then the added tracks, each of its type and codec (text subtitles may become SubRip),
//     with the language tag, the name and the default flag the task asked for;
//   - each added audio track covers the film: by mkvmerge's statistics of the output it
//     starts no later than the offset and ends no earlier than the video (less a negative
//     offset), both within the coverage tolerance, so a truncated dub is caught.
//
// mkvmerge's exit code is checked before (mux_failed).
func verify(e expectation, out media.Probe, outID identification) []string {
	var bad []string
	add := func(format string, v ...any) { bad = append(bad, fmt.Sprintf(format, v...)) }

	// The video.
	if len(out.Video) != len(e.lib.Video) {
		add("the output has %d video streams, the library file %d", len(out.Video), len(e.lib.Video))
	} else {
		for i, lv := range e.lib.Video {
			ov := out.Video[i]
			if ov.Codec != lv.Codec || ov.Width != lv.Width || ov.Height != lv.Height {
				add("video stream %d is %s %dx%d, the library file's %s %dx%d", i, ov.Codec, ov.Width, ov.Height, lv.Codec, lv.Width, lv.Height)
			}
			tol := frameMs(lv.FrameRate) + 1 // + 1: both durations are rounded to ms
			od := ov.DurationMs
			if od == 0 {
				od = out.DurationMs
			}
			switch {
			case lv.DurationMs == 0 && od > e.lib.DurationMs+tol:
				add("video stream %d lasts %d ms, longer than the library file (%d ms)", i, od, e.lib.DurationMs)
			case lv.DurationMs > 0 && abs(od-lv.DurationMs) > tol:
				add("video stream %d lasts %d ms, the library file's %d ms (more than a frame apart)", i, od, lv.DurationMs)
			}
		}
	}
	lv, ov := e.libID.ofType(typeVideo), outID.ofType(typeVideo)
	for i := range min(len(lv), len(ov)) {
		lf, lerr := strconv.ParseInt(lv[i].Properties.TagNumberOfFrames, 10, 64)
		of, oerr := strconv.ParseInt(ov[i].Properties.TagNumberOfFrames, 10, 64)
		if lerr == nil && oerr == nil && lf != of {
			add("video stream %d has %d frames, the library file's %d", i, of, lf)
		}
	}

	// The duration.
	tol := int64(1)
	if len(e.lib.Video) > 0 {
		tol = frameMs(e.lib.Video[0].FrameRate) + 1
	}
	longest := max(e.lib.DurationMs, e.up.DurationMs+e.offsetMs)
	switch {
	case out.DurationMs < e.lib.DurationMs-tol:
		add("the output lasts %d ms, shorter than the library file's %d ms", out.DurationMs, e.lib.DurationMs)
	case out.DurationMs > longest+tol:
		add("the output lasts %d ms, longer than its tracks can make it (%d ms)", out.DurationMs, longest)
	}

	// The track count.
	if got, want := tracks(out), tracks(e.lib)+len(e.added); got != want {
		add("the output has %d tracks (ffprobe), want %d: the library file's %d and %d added", got, want, tracks(e.lib), len(e.added))
	}
	if got, want := len(outID.Tracks), len(e.libID.Tracks)+len(e.added); got != want {
		add("the output has %d tracks (mkvmerge), want %d: the library file's %d and %d added", got, want, len(e.libID.Tracks), len(e.added))
		return bad // the tracks cannot be paired
	}

	// The library file's tracks, then the added ones.
	for i, lt := range e.libID.Tracks {
		ot := outID.Tracks[i]
		wantDefault := i < len(e.libDefaults) && e.libDefaults[i]
		switch {
		case !sameTrackKind(ot, lt):
			add("track %d is %s %s, the library file's %s %s", i, ot.Type, ot.Codec, lt.Type, lt.Codec)
		case lang(ot.Properties.Language) != lang(lt.Properties.Language) || ot.Properties.TrackName != lt.Properties.TrackName:
			add("track %d is %q %q, the library file's %q %q", i, lang(ot.Properties.Language), ot.Properties.TrackName, lang(lt.Properties.Language), lt.Properties.TrackName)
		case ot.Properties.DefaultTrack != wantDefault:
			add("track %d has the default flag %t, want %t", i, ot.Properties.DefaultTrack, wantDefault)
		}
	}
	videoMs := int64(0)
	if len(out.Video) > 0 {
		videoMs = out.Video[0].DurationMs
	}
	firstAudio := true
	for j, at := range e.added {
		i := len(e.libID.Tracks) + j
		ot := outID.Tracks[i]
		wantDefault := false
		if at.Type == typeAudio {
			wantDefault = !e.keepDefault && firstAudio
			firstAudio = false
		}
		switch {
		case !sameTrackKind(ot, at):
			add("added track %d is %s %s, the upload's %s %s", i, ot.Type, ot.Codec, at.Type, at.Codec)
		case !strings.EqualFold(ot.Properties.LanguageIETF, e.tag):
			add("added track %d has the language %q, want %q", i, ot.Properties.LanguageIETF, e.tag)
		case ot.Properties.TrackName != e.name:
			add("added track %d is named %q, want %q", i, ot.Properties.TrackName, e.name)
		case ot.Properties.DefaultTrack != wantDefault:
			add("added track %d has the default flag %t, want %t", i, ot.Properties.DefaultTrack, wantDefault)
		case ot.Type == typeAudio:
			if why := e.covers(ot, videoMs); why != "" {
				add("added track %d %s", i, why)
			}
		}
	}
	return bad
}

// covers checks that an added audio track spans the film (see verify); it says why not.
func (e expectation) covers(t track, videoMs int64) string {
	dur, ok := clockMs(t.Properties.TagDuration)
	if !ok || dur <= 0 {
		return "has no duration in mkvmerge's statistics, so its length cannot be checked"
	}
	start := t.Properties.MinimumTimestamp / 1e6
	end := start + dur
	if latest := max(e.offsetMs, 0) + e.coverageMs; start > latest {
		return fmt.Sprintf("starts at %d ms, later than %d ms (the offset and match.runtimeTolerance)", start, latest)
	}
	if videoMs > 0 {
		if earliest := videoMs + min(e.offsetMs, 0) - e.coverageMs; end < earliest {
			return fmt.Sprintf("ends at %d ms, before %d ms: the video lasts %d ms (is the upload cut short?)", end, earliest, videoMs)
		}
	}
	return ""
}

// lang is a track's legacy language, "und" when none is set: Matroska writes "und" for
// tracks that MP4, AVI or MPEG-TS leave untagged.
func lang(l string) string {
	if l == "" {
		return "und"
	}
	return l
}

// sameTrackKind reports whether an output track is of the type of its source and, for
// video and audio, which are copied as they are, of its codec. Subtitles may change codec:
// mkvmerge writes the text subtitles of other containers (MP4's Timed Text) as SubRip.
func sameTrackKind(out, src track) bool {
	return out.Type == src.Type && (out.Type == typeSubtitle || out.Codec == src.Codec)
}

// frameMs is the length of one frame in whole ms (rounded up) at the rate "num/den"; 42
// (24 fps) when the rate is unknown.
func frameMs(rate string) int64 {
	num, den, ok := strings.Cut(rate, "/")
	n, nerr := strconv.ParseFloat(num, 64)
	d, derr := strconv.ParseFloat(den, 64)
	if !ok || nerr != nil || derr != nil || n <= 0 || d <= 0 {
		return 42
	}
	ms := math.Ceil(1000 * d / n)
	if math.IsNaN(ms) || ms < 1 || ms > 1000 {
		return 42
	}
	return int64(ms)
}

func tracks(p media.Probe) int { return len(p.Video) + len(p.Audio) + len(p.Subtitles) }

func abs(v int64) int64 {
	if v < 0 {
		return -v
	}
	return v
}
