package flows

import (
	"encoding/json"
	"path"
	"slices"

	"github.com/segun-io/fukku/internal/arr"
	"github.com/segun-io/fukku/internal/config"
	"github.com/segun-io/fukku/internal/media"
	"github.com/segun-io/fukku/internal/pathmap"
	"github.com/segun-io/fukku/internal/store"
)

// Flow 2: another language of a film that is already in the library (plan §8). This file
// is #52's contract, used by #53 (preview), #55 (merge), #56 (second versions), #57
// (season dubs) and #59 (UI).
//
// The match. A film that already has a file in Radarr is no longer refused
// (already_in_library): the match succeeds, and the job's target is marked as flow 2 and
// carries the library file, the tracked file of GET /movie/<id> (movie.movieFile, plan §6
// as corrected in M0). It is kept in Target.Options, next to what the match stores there:
//
//	"flow2": {"library": {"fileId": 12,
//	                      "appPath": "/movies/Film (2001)/Film (2001) WEBDL-1080p.mkv",
//	                      "path": "/data/media/movies/Film (2001)/Film (2001) WEBDL-1080p.mkv",
//	                      "size": 123456789, "quality": {"id": 3, "name": "WEBDL-1080p"},
//	                      "languages": [{"id": 1, "name": "English"}], "runtimeMs": 540000,
//	                      "probe": {…media.Probe…}}}
//
// appPath is the file as Radarr sees it, path as Fukku and the worker see it
// (paths.mappings.radarr). runtimeMs is Radarr's mediaInfo.runTime, rounded and only
// informative. probe is absent until the worker has probed the file.
//
// The matched step (flow2_step.go). For a flow 2 job it does not ask Radarr for a Manual
// Import proposal. It submits the library probe (task kind libraryProbe, id
// runner.LibraryProbeID) for library.path, stores the probe in the record, and moves the
// job to awaiting-confirmation, where the user chooses what to do (#55, #56). Nothing is
// imported from there by the flow 1 confirmation: it refuses a flow 2 job (CodeFlow2).
// A library file that is not under paths.libraryRoots, or that the worker cannot read,
// fails the job with library_unreadable; a retry probes it again.
//
// The comparison (compare.go) is never stored: it is computed from the two probes with
// the match.* settings each time it is needed (CompareProbes), and what can be offered
// from it and merge.enabled / versions.enabled (Offers), so it always follows the
// settings. The upload is the job's longest accepted video (Flow2Upload).
//
// JobDetail (GET /api/jobs/{id}) shows it as "flow2" (Flow2View), null for other jobs:
//
//	"flow2": {"state": "ready",
//	          "library": {"fileId": 12, "name": "Film (2001) WEBDL-1080p.mkv", "size": 123456789,
//	                      "quality": {"id": 3, "name": "WEBDL-1080p"}, "languages": [{"id": 1, "name": "English"}],
//	                      "runtimeMs": 540000, "durationMs": 5400020, "audio": […], "subtitles": […]},
//	          "upload":  {"fileId": 5, "name": "Film.es.mkv", "size": 98765432, "quality": null, "languages": [],
//	                      "durationMs": 5400400, "audio": […], "subtitles": […]},
//	          "finding": "match", "deltaMs": 380, "ratio": 1.00007,
//	          "offers": ["merge", "version"], "notOffered": []}
//
// state is pending until the library file is probed; finding, deltaMs and ratio are then
// null and offers and notOffered empty. finding is match, pal or different; deltaMs is
// upload − library and ratio the longer duration over the shorter (durations as
// CutDuration measures them). offers lists what can be done, in order of preference
// (merge, version); notOffered lists the rest, each with its reason: merge_disabled,
// versions_disabled, versions_unsupported (no media server that can group versions, #56),
// or runtime_differs (a merge needs the same cut; the finding says whether it is a PAL
// speed-up or a different cut). With merge.enabled and versions.enabled both off, offers
// is empty and notOffered says why for both. The audio and subtitle tracks are the probes'
// (media.AudioTrack, media.SubtitleTrack). No path is shown. While "version" is offered,
// "version" says what the second version would be (VersionOffer, version.go):
//
//	"version": {"label": "Español", "language": "es", "name": "Film (2001) - Español.mkv",
//	            "problem": null, "notices": ["outside_radarr", "regrouped_after_changes", "kept_on_delete"]}

// Job error and refusal codes of flow 2.
const (
	// CodeLibraryUnreadable fails a flow 2 job whose library file is not under
	// paths.libraryRoots (as paths.mappings.radarr maps Radarr's path), or that the worker
	// could not read or probe. The job's error text names the path and the refusal.
	CodeLibraryUnreadable = "library_unreadable"
	// CodeFlow2 refuses the flow 1 confirmation of a flow 2 job: the film has a file, which
	// an import of the upload would replace.
	CodeFlow2 = "flow2"
)

// Flow2 is the flow 2 record of a job's target (Target.Options["flow2"]).
type Flow2 struct {
	Library LibraryFile `json:"library"`
}

// LibraryFile is the file the film has in the library, as Radarr tracked it at the match.
type LibraryFile struct {
	FileID    int          `json:"fileId"`  // Radarr's movie-file id
	AppPath   string       `json:"appPath"` // as Radarr sees it
	Path      string       `json:"path"`    // as Fukku and the worker see it
	Size      int64        `json:"size"`
	Quality   Ref          `json:"quality"`
	Languages []Ref        `json:"languages"`
	RuntimeMs int64        `json:"runtimeMs"`       // Radarr's mediaInfo.runTime; 0: unknown
	Probe     *media.Probe `json:"probe,omitempty"` // the worker's probe; nil until it is in
}

// Flow2Of returns the target's flow 2 record; ok is false for a job that is not flow 2.
func Flow2Of(t store.Target) (Flow2, bool) {
	var opts struct {
		Flow2 *Flow2 `json:"flow2"`
	}
	if len(t.Options) > 0 {
		_ = json.Unmarshal(t.Options, &opts)
	}
	if opts.Flow2 == nil {
		return Flow2{}, false
	}
	return *opts.Flow2, true
}

// newFlow2 is the record the match stores for a film's tracked file.
func newFlow2(f *arr.MovieFile, cfg config.Config) *Flow2 {
	lf := LibraryFile{
		FileID: f.ID, AppPath: f.Path, Size: f.Size, RuntimeMs: f.RuntimeMs,
		Quality: Ref{ID: f.Quality.ID, Name: f.Quality.Name}, Languages: []Ref{},
	}
	if f.Path != "" {
		lf.Path = pathmap.From(cfg.Paths.Mappings.Radarr, f.Path)
	}
	for _, l := range f.Languages {
		lf.Languages = append(lf.Languages, Ref{ID: l.ID, Name: l.Name})
	}
	return &Flow2{Library: lf}
}

// sameLibraryFile reports whether the old target's flow 2 record names the same library
// file as rec (nil: the new match is not flow 2), so that a match of the same title again
// may keep the old options, and with them the library file's probe.
func sameLibraryFile(old store.Target, rec *Flow2) bool {
	prev, ok := Flow2Of(old)
	if rec == nil || !ok {
		return rec == nil && !ok
	}
	a, b := prev.Library, rec.Library
	return a.FileID == b.FileID && a.AppPath == b.AppPath && a.Path == b.Path && a.Size == b.Size
}

// Flow2Upload is the job file flow 2 compares with the library file: the longest accepted
// video under files/ (by CutDuration; the first by path on a tie), with its probe. ok is
// false when the job has none.
func Flow2Upload(files []store.File) (store.File, media.Probe, bool) {
	var (
		best  store.File
		bestP media.Probe
		found bool
	)
	for _, f := range offerable(files) {
		pr := probeOf(f)
		if CutDuration(pr) <= 0 {
			continue
		}
		if !found || CutDuration(pr) > CutDuration(bestP) {
			best, bestP, found = f, pr, true
		}
	}
	return best, bestP, found
}

// Flow2 states in the API.
const (
	Flow2Pending = "pending" // the library file is not probed yet
	Flow2Ready   = "ready"
)

// Flow2View is JobDetail's "flow2".
type Flow2View struct {
	State      string         `json:"state"`
	Library    Flow2FileView  `json:"library"`
	Upload     *Flow2FileView `json:"upload"` // null: the job has no accepted video
	Finding    *string        `json:"finding"`
	DeltaMs    *int64         `json:"deltaMs"`
	Ratio      *float64       `json:"ratio"`
	Offers     []string       `json:"offers"`
	NotOffered []NotOffered   `json:"notOffered"`
	// Version is what a second version would be while "version" is offered (#56,
	// version.go); null otherwise.
	Version *VersionOffer `json:"version"`
	// MergeRefusal is the refusal that sent a merge back to this confirmation (#95), with
	// the choice it refused; null when there is none.
	MergeRefusal *MergeRefusalView `json:"mergeRefusal"`
}

// Flow2FileView is one side of the comparison. For the library file FileID is Radarr's
// movie-file id, and quality, languages and runtimeMs are Radarr's; for the upload FileID
// is the job file's id, and quality is null and languages empty (Radarr has not judged it).
// durationMs is the duration compared (CutDuration), null until the file is probed.
type Flow2FileView struct {
	FileID     int64                 `json:"fileId"`
	Name       string                `json:"name"`
	Size       int64                 `json:"size"`
	Quality    *Ref                  `json:"quality"`
	Languages  []Ref                 `json:"languages"`
	RuntimeMs  int64                 `json:"runtimeMs,omitempty"`
	DurationMs *int64                `json:"durationMs"`
	Audio      []media.AudioTrack    `json:"audio"`
	Subtitles  []media.SubtitleTrack `json:"subtitles"`
}

// ViewFlow2 returns JobDetail's flow2 for a job with target t and files, with the
// comparison and the offers as the settings in cfg make them now; nil when the job is not
// flow 2.
func ViewFlow2(cfg config.Config, t store.Target, files []store.File) *Flow2View {
	rec, ok := Flow2Of(t)
	if !ok {
		return nil
	}
	lib := rec.Library
	q := lib.Quality
	v := &Flow2View{
		State: Flow2Pending, Offers: []string{}, NotOffered: []NotOffered{},
		Library: Flow2FileView{
			FileID: int64(lib.FileID), Name: path.Base(lib.AppPath), Size: lib.Size, Quality: &q,
			Languages: lib.Languages, RuntimeMs: lib.RuntimeMs,
			Audio: []media.AudioTrack{}, Subtitles: []media.SubtitleTrack{},
		},
	}
	if lib.AppPath == "" {
		v.Library.Name = ""
	}
	v.MergeRefusal = viewMergeRefusal(t)
	if v.Library.Languages == nil {
		v.Library.Languages = []Ref{}
	}
	if lib.Probe != nil {
		v.Library.DurationMs = ptr(CutDuration(*lib.Probe))
		v.Library.Audio, v.Library.Subtitles = tracks(*lib.Probe)
	}
	f, up, found := Flow2Upload(files)
	if found {
		audio, subs := tracks(up)
		v.Upload = &Flow2FileView{
			FileID: f.ID, Name: path.Base(f.Path), Size: f.Size, Languages: []Ref{},
			DurationMs: ptr(CutDuration(up)), Audio: audio, Subtitles: subs,
		}
	}
	if lib.Probe == nil || !found {
		return v
	}
	c := CompareProbes(*lib.Probe, up, cfg.Match)
	v.State = Flow2Ready
	v.Finding, v.DeltaMs, v.Ratio = &c.Finding, &c.DeltaMs, &c.Ratio
	// The view shows the durations that were compared.
	if hasStreamDuration(*lib.Probe) != hasStreamDuration(up) {
		v.Library.DurationMs, v.Upload.DurationMs = &lib.Probe.DurationMs, &up.DurationMs
	}
	v.Offers, v.NotOffered = Offers(c.Finding, cfg)
	if slices.Contains(v.Offers, OfferVersion) {
		v.Version = viewVersion(cfg, lib, f, up, files)
	}
	return v
}

func tracks(p media.Probe) ([]media.AudioTrack, []media.SubtitleTrack) {
	audio, subs := p.Audio, p.Subtitles
	if audio == nil {
		audio = []media.AudioTrack{}
	}
	if subs == nil {
		subs = []media.SubtitleTrack{}
	}
	return audio, subs
}

func ptr[T any](v T) *T { return &v }
