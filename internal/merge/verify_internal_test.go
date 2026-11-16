package merge

import (
	"strings"
	"testing"

	"github.com/segun-io/fukku/internal/media"
)

func mkTrack(id int, typ, codec, lang, ietf, name string, def bool) track {
	t := track{ID: id, Type: typ, Codec: codec}
	t.Properties.Language, t.Properties.LanguageIETF = lang, ietf
	t.Properties.TrackName, t.Properties.DefaultTrack = name, def
	return t
}

// baseline is a merge that passes: a 24000/1001 film of 6 s with English audio
// (default) and subtitles, and a Spanish audio track added.
func baseline() (expectation, media.Probe, identification) {
	lib := media.Probe{DurationMs: 6000,
		Video:     []media.VideoTrack{{Codec: "h264", Width: 640, Height: 360, FrameRate: "24000/1001", DurationMs: 6006}},
		Audio:     []media.AudioTrack{{Codec: "aac", Language: "eng", Default: true}},
		Subtitles: []media.SubtitleTrack{{Codec: "subrip", Language: "eng"}}}
	libID := identification{Tracks: []track{
		mkTrack(0, typeVideo, "AVC/H.264/MPEG-4p10", "und", "und", "", false),
		mkTrack(1, typeAudio, "AAC", "eng", "en", "", true),
		mkTrack(2, typeSubtitle, "SubRip/SRT", "eng", "en", "", false),
	}}
	libID.Tracks[0].Properties.TagNumberOfFrames = "144"
	up := media.Probe{DurationMs: 6010}
	added := []track{mkTrack(1, typeAudio, "AAC", "spa", "es", "", false)}
	e := expectation{lib: lib, libID: libID, libDefaults: []bool{false, true, false}, up: up, added: added,
		tag: "es-419", name: "Español (Latino)", keepDefault: true, coverageMs: 500}

	out := lib
	out.Video = []media.VideoTrack{lib.Video[0]}
	out.Video[0].DurationMs = 6020 // within a frame (42 ms)
	out.Audio = append([]media.AudioTrack{}, lib.Audio[0], media.AudioTrack{Codec: "aac", Language: "spa"})
	out.DurationMs = 6010
	outID := identification{Tracks: append(append([]track{}, libID.Tracks...),
		mkTrack(3, typeAudio, "AAC", "spa", "es-419", "Español (Latino)", false))}
	outID.Tracks[3].Properties.TagDuration = "00:00:06.000000000"
	outID.Tracks[0].Properties.TagNumberOfFrames = "144"
	return e, out, outID
}

func TestVerifyRules(t *testing.T) {
	e, out, outID := baseline()
	if bad := verify(e, out, outID); len(bad) != 0 {
		t.Fatalf("the baseline fails: %v", bad)
	}
	for name, tc := range map[string]struct {
		mutate func(*expectation, *media.Probe, *identification)
		want   string
	}{
		"no video": {func(_ *expectation, o *media.Probe, _ *identification) { o.Video = nil }, "0 video streams"},
		"other codec": {func(_ *expectation, o *media.Probe, _ *identification) {
			o.Video = []media.VideoTrack{{Codec: "hevc", Width: 640, Height: 360, DurationMs: 6006}}
		}, "is hevc 640x360"},
		"other size": {func(_ *expectation, o *media.Probe, _ *identification) {
			o.Video = []media.VideoTrack{{Codec: "h264", Width: 320, Height: 180, DurationMs: 6006}}
		}, "320x180"},
		"video a frame short": {func(_ *expectation, o *media.Probe, _ *identification) {
			o.Video = []media.VideoTrack{{Codec: "h264", Width: 640, Height: 360, DurationMs: 6006 - 44}}
		}, "more than a frame apart"},
		"frame count": {func(_ *expectation, _ *media.Probe, id *identification) {
			id.Tracks[0].Properties.TagNumberOfFrames = "143"
		}, "143 frames"},
		"shorter": {func(_ *expectation, o *media.Probe, _ *identification) { o.DurationMs = 5000 }, "shorter"},
		"longer":  {func(_ *expectation, o *media.Probe, _ *identification) { o.DurationMs = 9000 }, "longer"},
		"ffprobe count": {func(_ *expectation, o *media.Probe, _ *identification) {
			o.Audio = o.Audio[:1]
		}, "tracks (ffprobe)"},
		"mkvmerge count": {func(_ *expectation, _ *media.Probe, id *identification) {
			id.Tracks = id.Tracks[:3]
		}, "tracks (mkvmerge)"},
		"original moved": {func(_ *expectation, _ *media.Probe, id *identification) {
			id.Tracks[1], id.Tracks[2] = id.Tracks[2], id.Tracks[1]
		}, "the library file's audio AAC"},
		"original renamed": {func(_ *expectation, _ *media.Probe, id *identification) {
			id.Tracks[1].Properties.TrackName = "Commentary"
		}, `"Commentary"`},
		"original lost default": {func(_ *expectation, _ *media.Probe, id *identification) {
			id.Tracks[1].Properties.DefaultTrack = false
		}, "track 1 has the default flag false, want true"},
		"added wrong tag": {func(_ *expectation, _ *media.Probe, id *identification) {
			id.Tracks[3].Properties.LanguageIETF = "es"
		}, `language "es"`},
		"added wrong name": {func(_ *expectation, _ *media.Probe, id *identification) {
			id.Tracks[3].Properties.TrackName = "Espa"
		}, `named "Espa"`},
		"added default": {func(_ *expectation, _ *media.Probe, id *identification) {
			id.Tracks[3].Properties.DefaultTrack = true
		}, "track 3 has the default flag true, want false"},
		"added other codec": {func(_ *expectation, _ *media.Probe, id *identification) {
			id.Tracks[3].Codec = "AC-3"
		}, "the upload's audio AAC"},
		"dub to be default": {func(e *expectation, _ *media.Probe, _ *identification) {
			e.keepDefault, e.libDefaults[1] = false, false
		}, "track 1 has the default flag true, want false"},
		"library default not as given": {func(e *expectation, _ *media.Probe, _ *identification) {
			e.libDefaults[0] = true
		}, "track 0 has the default flag false, want true"},
		"truncated dub": {func(_ *expectation, _ *media.Probe, id *identification) {
			id.Tracks[3].Properties.TagDuration = "00:00:04.200000000"
		}, "ends at 4200 ms, before 5520 ms"},
		"late dub": {func(_ *expectation, _ *media.Probe, id *identification) {
			id.Tracks[3].Properties.MinimumTimestamp = 2e9
		}, "starts at 2000 ms"},
		"dub without statistics": {func(_ *expectation, _ *media.Probe, id *identification) {
			id.Tracks[3].Properties.TagDuration = ""
		}, "has no duration"},
		"unmeasured video, output longer": {func(e *expectation, o *media.Probe, _ *identification) {
			e.lib.Video[0].DurationMs = 0
			o.Video[0].DurationMs = 6100
		}, "longer than the library file"},
	} {
		e, out, outID := baseline()
		tc.mutate(&e, &out, &outID)
		bad := strings.Join(verify(e, out, outID), "; ")
		if !strings.Contains(bad, tc.want) {
			t.Errorf("%s: %q, want it to say %q", name, bad, tc.want)
		}
	}
}

// What may differ without failing: an offset makes the output longer by up to the offset;
// text subtitles change codec; the dub as the default, when asked for; the tag's case.
func TestVerifyAllowed(t *testing.T) {
	e, out, outID := baseline()
	e.offsetMs = 800
	out.DurationMs = 6810
	if bad := verify(e, out, outID); len(bad) != 0 {
		t.Errorf("offset: %v", bad)
	}

	e, out, outID = baseline()
	e.added = append(e.added, mkTrack(2, typeSubtitle, "Timed Text", "spa", "es", "", false))
	out.Subtitles = append(out.Subtitles, media.SubtitleTrack{Codec: "subrip"})
	outID.Tracks = append(outID.Tracks, mkTrack(4, typeSubtitle, "SubRip/SRT", "spa", "ES-419", "Español (Latino)", false))
	if bad := verify(e, out, outID); len(bad) != 0 {
		t.Errorf("converted subtitles: %v", bad)
	}

	e, out, outID = baseline()
	e.keepDefault, e.libDefaults[1] = false, false
	outID.Tracks[1].Properties.DefaultTrack = false
	outID.Tracks[3].Properties.DefaultTrack = true
	if bad := verify(e, out, outID); len(bad) != 0 {
		t.Errorf("dub as default: %v", bad)
	}

	// An MP4, AVI or MPEG-TS library leaves tracks untagged; Matroska says "und".
	e, out, outID = baseline()
	e.libID.Tracks[1].Properties.Language = ""
	outID.Tracks[1].Properties.Language = "und"
	if bad := verify(e, out, outID); len(bad) != 0 {
		t.Errorf("untagged library track: %v", bad)
	}

	// A dub 800 ms early is cut at the start and ends 800 ms before the picture.
	e, out, outID = baseline()
	e.offsetMs = -800
	outID.Tracks[3].Properties.TagDuration = "00:00:05.200000000"
	if bad := verify(e, out, outID); len(bad) != 0 {
		t.Errorf("negative offset: %v", bad)
	}

	// A library video stream without a duration of its own (not measured): only the
	// weaker check, which a correct output passes.
	e, out, outID = baseline()
	e.lib.Video[0].DurationMs = 0
	if bad := verify(e, out, outID); len(bad) != 0 {
		t.Errorf("unmeasured library video: %v", bad)
	}
}

func TestFrameMs(t *testing.T) {
	for rate, want := range map[string]int64{"24000/1001": 42, "25/1": 40, "60000/1001": 17, "": 42, "0/0": 42, "x/y": 42, "1/0": 42, "1000000/1": 1} {
		if got := frameMs(rate); got != want {
			t.Errorf("frameMs(%q) = %d, want %d", rate, got, want)
		}
	}
}

func TestLimitedBufferTail(t *testing.T) {
	b := &limitedBuffer{max: 8, tail: true}
	for _, s := range []string{"abc", "defgh", "ijklmnop", "q"} {
		if n, err := b.Write([]byte(s)); err != nil || n != len(s) {
			t.Fatalf("write %q: %d %v", s, n, err)
		}
	}
	if got := b.buf.String(); got != "jklmnopq" {
		t.Errorf("tail %q", got)
	}
	strict := &limitedBuffer{max: 4}
	if _, err := strict.Write([]byte("12345")); err == nil {
		t.Error("strict buffer took too much")
	}
	if got := muxMessages("a\nProgress: 10%\rProgress: 100%\nb\n\nc\n", 2); got != "b; c" {
		t.Errorf("muxMessages %q", got)
	}
	log := "mkvmerge v92.0\nWarning: odd timestamps\nProgress: 100%\nThe cue entries are being written...\nMultiplexing took 0 seconds.\n"
	if got := muxMessages(log, 5); got != "Warning: odd timestamps" {
		t.Errorf("muxMessages keeps the warning: %q", got)
	}
	for in, want := range map[string]int64{"00:00:05.034000000": 5034, "01:02:03.5": 3723500} {
		if got, ok := clockMs(in); !ok || got != want {
			t.Errorf("clockMs(%q) = %d %t", in, got, ok)
		}
	}
	for _, in := range []string{"", "5.0", "00:61:00.0", "00:00:60.0", "a:b:c"} {
		if _, ok := clockMs(in); ok {
			t.Errorf("clockMs(%q) accepted", in)
		}
	}
}
