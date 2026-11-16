package flows

import (
	"encoding/json"
	"math"
	"slices"
	"testing"
	"time"

	"github.com/segun-io/fukku/internal/config"
	"github.com/segun-io/fukku/internal/media"
	"github.com/segun-io/fukku/internal/store"
)

// The three findings at their boundaries, with the default settings: ±500 ms, and the PAL
// ratio 1.0427 ± 0.002.
func TestCompareBoundaries(t *testing.T) {
	m := config.Defaults().Match
	const film = int64(6_000_000) // 100 minutes
	pal := func(ratio float64) int64 { return int64(math.Round(float64(film) / ratio)) }
	for _, tc := range []struct {
		name            string
		library, upload int64
		want            string
	}{
		{"identical", film, film, FindingMatch},
		{"500 ms longer", film, film + 500, FindingMatch},
		{"500 ms shorter", film, film - 500, FindingMatch},
		{"501 ms longer", film, film + 501, FindingDifferent},
		{"501 ms shorter", film, film - 501, FindingDifferent},
		{"PAL upload", film, pal(25.0 / (24000.0 / 1001.0)), FindingPAL},
		{"PAL library", pal(25.0 / (24000.0 / 1001.0)), film, FindingPAL},
		{"ratio at the window's top", film, pal(1.0447), FindingPAL},
		{"ratio at the window's bottom", film, pal(1.0407), FindingPAL},
		{"ratio just above the window", film, pal(1.0448), FindingDifferent},
		{"ratio just below the window", film, pal(1.0406), FindingDifferent},
		{"a trimmed cut", film, film - 120_000, FindingDifferent},
		{"a longer cut", film, film + 600_000, FindingDifferent},
		{"library unknown", 0, film, FindingDifferent},
		{"upload unknown", film, 0, FindingDifferent},
	} {
		c := Compare(tc.library, tc.upload, m)
		if c.Finding != tc.want {
			t.Errorf("%s: %+v, want %s", tc.name, c, tc.want)
		}
		if c.DeltaMs != tc.upload-tc.library {
			t.Errorf("%s: delta %d", tc.name, c.DeltaMs)
		}
		if tc.library > 0 && tc.upload > 0 && c.Ratio < 1 {
			t.Errorf("%s: ratio %f < 1", tc.name, c.Ratio)
		}
	}

	// The settings move the boundaries.
	m.RuntimeTolerance = config.Duration(2 * time.Second)
	if c := Compare(film, film+2000, m); c.Finding != FindingMatch {
		t.Errorf("2 s tolerance: %+v", c)
	}
	m.PALRatio, m.PALTolerance = 1.05, 0.001
	if c := Compare(film, pal(1.0427), m); c.Finding != FindingDifferent {
		t.Errorf("another PAL ratio: %+v", c)
	}
	if c := Compare(film, pal(1.0505), m); c.Finding != FindingPAL {
		t.Errorf("another PAL ratio: %+v", c)
	}
}

// A dub whose audio starts late makes the container longer, not the video: the cut is
// compared on the video stream. A file without a stream duration is compared, like the
// other, on the container's.
func TestCompareProbesUsesTheVideoStream(t *testing.T) {
	m := config.Defaults().Match
	probe := func(container, video int64) media.Probe {
		return media.Probe{DurationMs: container, Video: []media.VideoTrack{{DurationMs: video}}}
	}
	lib := probe(6_000_021, 6_000_020)
	if c := CompareProbes(lib, probe(6_000_852, 6_000_040), m); c.Finding != FindingMatch || c.DeltaMs != 20 {
		t.Errorf("shifted dub: %+v", c)
	}
	if c := CompareProbes(lib, probe(6_000_852, 0), m); c.Finding != FindingDifferent || c.DeltaMs != 831 {
		t.Errorf("no stream duration on one side: %+v", c)
	}
	if got := CutDuration(media.Probe{DurationMs: 7}); got != 7 {
		t.Errorf("no video: %d", got)
	}
}

func TestOffers(t *testing.T) {
	for _, tc := range []struct {
		finding         string
		merge, versions bool
		offers          []string
		notOffered      []NotOffered
	}{
		{FindingMatch, true, true, []string{OfferMerge, OfferVersion}, []NotOffered{}},
		{FindingMatch, true, false, []string{OfferMerge}, []NotOffered{{OfferVersion, ReasonVersionsDisabled}}},
		{FindingMatch, false, true, []string{OfferVersion}, []NotOffered{{OfferMerge, ReasonMergeDisabled}}},
		{FindingMatch, false, false, []string{}, []NotOffered{{OfferMerge, ReasonMergeDisabled}, {OfferVersion, ReasonVersionsDisabled}}},
		{FindingPAL, true, true, []string{OfferVersion}, []NotOffered{{OfferMerge, ReasonRuntimeDiffers}}},
		{FindingPAL, true, false, []string{}, []NotOffered{{OfferMerge, ReasonRuntimeDiffers}, {OfferVersion, ReasonVersionsDisabled}}},
		{FindingDifferent, true, true, []string{OfferVersion}, []NotOffered{{OfferMerge, ReasonRuntimeDiffers}}},
		{FindingDifferent, false, false, []string{}, []NotOffered{{OfferMerge, ReasonRuntimeDiffers}, {OfferVersion, ReasonVersionsDisabled}}},
	} {
		cfg := config.Defaults()
		cfg.Merge.Enabled, cfg.Versions.Enabled = tc.merge, tc.versions
		offers, not := Offers(tc.finding, cfg)
		if !slices.Equal(offers, tc.offers) || !slices.Equal(not, tc.notOffered) {
			t.Errorf("%s merge=%t versions=%t: %v %v, want %v %v", tc.finding, tc.merge, tc.versions, offers, not, tc.offers, tc.notOffered)
		}
	}
}

func TestViewFlow2(t *testing.T) {
	cfg := config.Defaults()
	rec := newFlow2(libFile(), cfg)
	upload := func(id int64, name string, ms int64) store.File {
		pr, _ := json.Marshal(media.Probe{DurationMs: ms, Video: []media.VideoTrack{{DurationMs: ms}},
			Audio: []media.AudioTrack{{Index: 1, Codec: "aac", Language: "spa"}}})
		return store.File{ID: id, Path: "files/" + name, Kind: store.KindVideo, Size: 99, Status: store.FileOK, Report: pr}
	}
	files := []store.File{upload(1, "sample.mkv", 60_000), upload(2, "Some Film.es.mkv", filmMinutes+300)}
	target := func(r *Flow2) store.Target {
		raw, _ := withOption(nil, "flow2", r)
		return store.Target{App: AppRadarr, TitleID: filmID, Options: raw}
	}

	if v := ViewFlow2(cfg, store.Target{App: AppRadarr, TitleID: filmID}, files); v != nil {
		t.Errorf("flow 1 job: %+v", v)
	}

	v := ViewFlow2(cfg, target(rec), files)
	if v == nil || v.State != Flow2Pending || v.Finding != nil || v.DeltaMs != nil || len(v.Offers) != 0 || len(v.NotOffered) != 0 {
		t.Fatalf("pending: %+v", v)
	}
	if v.Library.Name != "Some Film (2001) WEBDL-1080p.mkv" || v.Library.DurationMs != nil || v.Library.RuntimeMs != filmMinutes ||
		v.Library.Quality == nil || v.Library.Quality.Name != "WEBDL-1080p" {
		t.Errorf("library: %+v", v.Library)
	}
	if v.Upload == nil || v.Upload.FileID != 2 || *v.Upload.DurationMs != filmMinutes+300 || len(v.Upload.Audio) != 1 || v.Upload.Quality != nil {
		t.Errorf("upload (the longest video): %+v", v.Upload)
	}

	rec.Library.Probe = libProbe(filmMinutes)
	v = ViewFlow2(cfg, target(rec), files)
	if v.State != Flow2Ready || *v.Finding != FindingMatch || *v.DeltaMs != 300 || *v.Library.DurationMs != filmMinutes {
		t.Fatalf("ready: %+v", v)
	}
	if !slices.Equal(v.Offers, []string{OfferMerge}) || !slices.Equal(v.NotOffered, []NotOffered{{OfferVersion, ReasonVersionsDisabled}}) {
		t.Errorf("defaults: %v %v", v.Offers, v.NotOffered)
	}
	cfg.Merge.Enabled = false
	v = ViewFlow2(cfg, target(rec), files)
	if len(v.Offers) != 0 || !slices.Equal(v.NotOffered, []NotOffered{{OfferMerge, ReasonMergeDisabled}, {OfferVersion, ReasonVersionsDisabled}}) {
		t.Errorf("nothing enabled: %v %v", v.Offers, v.NotOffered)
	}

	// A job without a usable video (cannot happen after probing) shows no upload.
	if v := ViewFlow2(cfg, target(rec), nil); v.Upload != nil || v.State != Flow2Pending {
		t.Errorf("no upload: %+v", v)
	}
}
