package flows

import (
	"math"

	"github.com/segun-io/fukku/internal/config"
	"github.com/segun-io/fukku/internal/media"
)

// The runtime comparison of flow 2 (plan §8, #52): is the upload the same cut as the file
// in the library? Both durations come from the worker's ffprobe, never from the app's
// rounded runtime. The same comparison per episode is the basis of the season-dub
// policies (#57).
//
//	| Finding   | When                                              | Offered                               |
//	| match     | |upload − library| ≤ match.runtimeTolerance       | merge (after the sync check), version |
//	| pal       | longer / shorter within match.palTolerance of     | version (a PAL speed-up)              |
//	|           | match.palRatio                                    |                                       |
//	| different | anything else                                     | version (a different cut)             |

// Findings.
const (
	FindingMatch     = "match"     // the same cut: a merge is possible, after the sync check
	FindingPAL       = "pal"       // a PAL speed-up of the same cut (25 / 23.976): a second version
	FindingDifferent = "different" // a different cut: a second version
)

// Offers: what flow 2 can do with the upload.
const (
	OfferMerge   = "merge"   // 2a: add the upload's tracks to the library file (#55)
	OfferVersion = "version" // 2b: keep the upload as a second version of the film (#56)
)

// Reasons an offer is not made (NotOffered.Reason).
const (
	ReasonMergeDisabled    = "merge_disabled"    // merge.enabled is off
	ReasonVersionsDisabled = "versions_disabled" // versions.enabled is off
	// ReasonRuntimeDiffers: a merge needs the same cut; the finding (pal or different) says
	// how the two differ.
	ReasonRuntimeDiffers = "runtime_differs"
)

// palEpsilon absorbs the rounding of the ratio, so that a ratio exactly at the edge of the
// PAL window counts as inside.
const palEpsilon = 1e-9

// Comparison is the outcome of Compare. DeltaMs is upload − library (negative: the upload
// is shorter); Ratio is the longer duration over the shorter one (≥ 1).
type Comparison struct {
	Finding string  `json:"finding"`
	DeltaMs int64   `json:"deltaMs"`
	Ratio   float64 `json:"ratio"`
}

// Compare classifies two durations in ms with the match.* settings. The tolerances are
// inclusive: a difference of exactly match.runtimeTolerance is a match, a ratio exactly
// match.palTolerance away from match.palRatio is a PAL speed-up. Either duration ≤ 0 (not
// known) is a different cut: nothing can be said to match it.
func Compare(libraryMs, uploadMs int64, m config.Match) Comparison {
	c := Comparison{Finding: FindingDifferent, DeltaMs: uploadMs - libraryMs}
	if libraryMs <= 0 || uploadMs <= 0 {
		return c
	}
	long, short := max(libraryMs, uploadMs), min(libraryMs, uploadMs)
	c.Ratio = float64(long) / float64(short)
	switch {
	case abs(c.DeltaMs) <= m.RuntimeTolerance.D().Milliseconds():
		c.Finding = FindingMatch
	case math.Abs(c.Ratio-m.PALRatio) <= m.PALTolerance+palEpsilon:
		c.Finding = FindingPAL
	}
	return c
}

func abs(v int64) int64 {
	if v < 0 {
		return -v
	}
	return v
}

// CutDuration is the duration of a probed file that flow 2 compares: the first video
// stream's own duration when the file has one, else the container's. The container's
// duration grows when an audio track starts late (a dub shifted by 800 ms is 800 ms
// "longer"), and such a dub is the same cut: the sync check (#53) is what catches the
// shift.
func CutDuration(p media.Probe) int64 {
	if hasStreamDuration(p) {
		return p.Video[0].DurationMs
	}
	return p.DurationMs
}

func hasStreamDuration(p media.Probe) bool { return len(p.Video) > 0 && p.Video[0].DurationMs > 0 }

// CompareProbes is Compare on the cut durations of two probes. When only one of them has a
// video stream duration, both use the container's, so the two are measured alike.
func CompareProbes(library, upload media.Probe, m config.Match) Comparison {
	if hasStreamDuration(library) != hasStreamDuration(upload) {
		return Compare(library.DurationMs, upload.DurationMs, m)
	}
	return Compare(CutDuration(library), CutDuration(upload), m)
}

// NotOffered is an offer flow 2 does not make, and why.
type NotOffered struct {
	Offer  string `json:"offer"`
	Reason string `json:"reason"`
}

// Offers returns what can be done with an upload of the finding, in order of preference,
// and what cannot, with the reason; merge first, then version. offers is empty, never nil,
// when nothing can be done; notOffered then says why for each.
func Offers(finding string, cfg config.Config) (offers []string, notOffered []NotOffered) {
	offers, notOffered = []string{}, []NotOffered{}
	switch {
	case finding != FindingMatch:
		notOffered = append(notOffered, NotOffered{OfferMerge, ReasonRuntimeDiffers})
	case !cfg.Merge.Enabled:
		notOffered = append(notOffered, NotOffered{OfferMerge, ReasonMergeDisabled})
	default:
		offers = append(offers, OfferMerge)
	}
	if cfg.Versions.Enabled {
		offers = append(offers, OfferVersion)
	} else {
		notOffered = append(notOffered, NotOffered{OfferVersion, ReasonVersionsDisabled})
	}
	return offers, notOffered
}
