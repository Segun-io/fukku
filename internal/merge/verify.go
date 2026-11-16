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
	coverageMs  int64 // merge.coverageTolerance
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
//   - each added audio track covers the film, by the output's own timestamps and mkvmerge's
//     statistics, within merge.coverageTolerance (see covers), so a truncated dub is caught.
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
	film, filmOK := e.filmEnd(out, outID)
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
			if why := e.covers(ot, at, film, filmOK); why != "" {
				add("added track %d %s", i, why)
			}
		}
	}
	return bad
}

// span is when a track starts and ends in ms, by its first timestamp and mkvmerge's
// statistics (DURATION). ok is false without statistics.
func span(t track) (start, end int64, ok bool) {
	d, ok := clockMs(t.Properties.TagDuration)
	if !ok || d <= 0 {
		return 0, 0, false
	}
	start = t.Properties.MinimumTimestamp / 1e6
	return start, start + d, true
}

// claimedEnd is when a track of the upload ends by its DURATION tag: mkvmerge's statistics
// give its length from its first timestamp, ffmpeg's tag the time it ends.
func claimedEnd(t track) (int64, bool) {
	d, ok := clockMs(t.Properties.TagDuration)
	if !ok || d <= 0 {
		return 0, false
	}
	if t.Properties.StatisticsApp != "" {
		return t.Properties.MinimumTimestamp/1e6 + d, true
	}
	return d, true
}

// filmEnd is when the film ends in the output: the end of its first video stream (its
// start time plus its length, so a stream that starts late is not taken to end early), or,
// when the library file's audio ends sooner, the end of that audio: a dub cannot be asked
// to cover more than the original does. Without statistics for the video, its probed
// length is used, and then the library file's. ok is false when none is known.
func (e expectation) filmEnd(out media.Probe, outID identification) (int64, bool) {
	var end int64
	if vs := outID.ofType(typeVideo); len(vs) > 0 {
		if _, ve, ok := span(vs[0]); ok {
			end = ve
		} else if len(out.Video) > 0 && out.Video[0].DurationMs > 0 {
			end = vs[0].Properties.MinimumTimestamp/1e6 + out.Video[0].DurationMs
		}
	}
	if end == 0 && len(e.lib.Video) > 0 {
		end = e.lib.Video[0].DurationMs
	}
	if end <= 0 {
		return 0, false
	}
	var audioEnd int64
	for i, lt := range e.libID.Tracks {
		if lt.Type != typeAudio || i >= len(outID.Tracks) {
			continue
		}
		if _, ae, ok := span(outID.Tracks[i]); ok {
			audioEnd = max(audioEnd, ae)
		}
	}
	if audioEnd > 0 {
		end = min(end, audioEnd)
	}
	return end, true
}

// covers checks that an added audio track t, from the upload's track src, covers the film
// (see verify) and says why not, within the coverage tolerance:
//
//   - it starts no later than it did in the upload, shifted by the offset (a dub whose
//     audio starts 0.8 s into the upload is fine, at 0.8 s);
//   - it ends no earlier than the film (filmEnd), less a negative offset, which moves the
//     whole dub earlier;
//   - and, when the upload's track has a DURATION tag, no earlier than it says the track
//     ends (claimedEnd), shifted by the offset: a file cut short whose header still states the full
//     length is caught whatever the offset.
func (e expectation) covers(t, src track, film int64, filmOK bool) string {
	start, end, ok := span(t)
	if !ok {
		return "has no duration in mkvmerge's statistics, so its length cannot be checked"
	}
	tol := e.coverageMs
	if latest := max(src.Properties.MinimumTimestamp/1e6+e.offsetMs, 0) + tol; start > latest {
		return fmt.Sprintf("starts at %d ms, later than %d ms (its start in the upload, the offset and merge.coverageTolerance)", start, latest)
	}
	if !filmOK {
		return "cannot be checked against the film, whose length is unknown"
	}
	if want := film + min(e.offsetMs, 0); end < want-tol {
		return fmt.Sprintf("ends at %d ms, before %d ms: the film ends at %d ms, offset %d ms (is the upload cut short?)",
			end, want, film, e.offsetMs)
	}
	if claimed, ok := claimedEnd(src); ok {
		if want := claimed + e.offsetMs; end < want-tol {
			return fmt.Sprintf("ends at %d ms, before %d ms: the upload's statistics say it ends at %d ms, offset %d ms (is the upload cut short?)",
				end, want, claimed, e.offsetMs)
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
