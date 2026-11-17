package web

import (
	"context"
	"errors"
	"net/http"
	"time"

	"github.com/segun-io/fukku/internal/flows"
)

// The confirmation (#40): the user confirms the proposal and the import starts. Rows are
// named by id; no request carries a path or a title.
//
//	POST /api/jobs/{id}/confirm  {"overrides": ["r3"], "seasonDub": "replace",
//	                              "subtitles": [{"fileId": 12, "language": "es", "forced": false, "hearingImpaired": true}]}
//	                              → 202 Job (state importing)
//
// overrides lists the rows to import despite their rejections or warnings (the language
// warning of #39 included). subtitles (optional) sets the language (ISO 639-1) and flags of
// subtitle files of the job for the hand-over; a file not listed keeps what its name says.
// seasonDub (optional) answers the proposal's seasonDub question (#57) for rows whose
// episodes already have a file: replace imports over them (the choice answers the app's
// "not an upgrade" refusal and the existing_file warning of those rows; the season is
// unmonitored afterwards when only a dub was imported), subtitlesOnly imports nothing and
// hands the subtitles over. With a fixed seasonDub.policy it must be empty or equal to it.
// Audit: season.replaced or season.subtitles_only per season, import.confirmed,
// rejection.overridden per overridden row, and language.overridden with the custom formats
// and scores when the row's language warning was overridden.
// Errors besides the common ones:
//
//	409 needs_override     rows with rejections or warnings that overrides does not list:
//	                       {"error": {"code": "needs_override", "message": "…", "rows": ["r3"]}}
//	409 incomplete_rows    rows without a title or an episode (edit or skip them); "rows" as above
//	409 season_dub_choice  rows whose episodes already have a file and seasonDub.policy is
//	                       ask: "rows" lists them; send "seasonDub" with one of the proposal's
//	                       seasonDub.offers ("replace", "subtitlesOnly"), or skip the rows
//	409 shared_file        with replace: the library file of these rows also holds episodes
//	                       outside the job (a double); "rows" as above; list one in overrides
//	                       to accept losing those episodes' file
//	409 nothing_to_import  every row is skipped
//	409 wrong_state        the job is not awaiting confirmation (or the rows changed meanwhile)
//	409 flow2              the film already has a file (flow 2, #52): the job's "flow2" says
//	                       what can be done instead
//
// A flow 2 job is confirmed as a second version of the film (#56, decision #20) with
//
//	POST /api/jobs/{id}/confirm  {"flow2": "version", "version": {"label": "Español"}}
//	                              → 202 Job (state importing)
//
// label is optional (empty: flow2.version.label, the proposed one); overrides, subtitles
// and seasonDub must be absent. The version is placed outside Radarr's root folders, in
// versions.root, and grouped with the film in the media server (flows/version.go). Audit:
// version.confirmed, then version.linked once the file is in place. Errors besides the
// common ones:
//
//	409 version_unavailable  "version" is not among flow2.offers, or versions.root cannot be
//	                       used (inside a Radarr root folder, say)
//	409 version_label      the label is refused: a forbidden token, a word Jellyfin takes for
//	                       an extra, a character that cannot be in a file name, too long, or
//	                       Radarr reads a quality in the name (versions.requireNotUpgradeVerdict)
//	409 version_exists     a file of the version's name is already there; nothing is replaced
//	409 library_changed    Radarr's file for the film changed since the match: the job went
//	                       back to probed; match the film again
//	503 storage_unavailable  versions.root is missing, not writable, or not on the same mount
//	                       as the intake root (checked by a trial hard link)
//	400 bad_request        flow2 or version on a job that is not flow 2, or with other fields
//
// A show's rows whose episodes already have a file are kept as second versions of those
// episodes (#58, decision #20) with
//
//	POST /api/jobs/{id}/confirm  {"seasonDub": "version", "version": {"label": "Español"}}
//	                              → 202 Job (state importing)
//
// when "version" is among the proposal's seasonDub.offers (versions.enabled, Jellyfin 12.4 or
// later, Sonarr's series folder usable in a file name, every season folder taking a hard link
// from the intake root; else seasonDub.notOffered says why). label is optional (empty: seasonDub.version.label). Each version goes next to the
// episode's file, as "<series folder> - S01E02 - <label><ext>"; the other rows import as
// usual. Audit: season.versions per season, version.confirmed per episode, then
// version.linked once each file is in place. A version the placement refuses (Sonarr's
// verdict changed, the name was taken, the episode lost its file) fails its row only
// (import.rows[].result failed, result.episodeVersions[].status refused, audit
// version.refused); the job fails only when every version is refused and nothing else is
// imported. Errors besides the common ones:
//
//	409 version_unavailable  rows that cannot be versions (a double episode, a file holding
//	                       other episodes, a folder outside paths.libraryRoots): "rows" lists them
//	409 version_exists     a file or a record of a version's name is there: "rows" lists them
//	409 version_label      the label is refused (as for films, or it holds an SxxEyy)
//	409 version_upgrade    Sonarr does not reject these files, under their version names, as
//	                       "not an upgrade": it would take them for the episodes' files on its
//	                       next rescan (versions.requireNotUpgradeVerdict); "rows" lists them
//	409 version_unchecked  Sonarr gives no verdict on these files (it does not offer them, or
//	                       reads them as another episode); "rows" lists them, the message why
//	503 storage_unavailable  a season folder is missing, not writable by Fukku, or not on the
//	                       same mount as the intake root (a trial hard link)
//
// A flow 2 job whose upload is the same cut is confirmed as a merge (#55, flows/merge.go):
//
//	POST /api/jobs/{id}/confirm  {"flow2": "merge", "merge": {"audioTracks": [2], "subtitleTracks": [],
//	                              "offsetMs": -800, "languageTag": "es-419", "trackName": "Español (Latino)",
//	                              "keepOriginalDefault": true}}
//	                              → 202 Job (state importing)
//
// Every field of merge is optional: the preview's audio track and offset, no subtitle
// track, and merge.languageTag, merge.trackName and merge.keepOriginalDefault. Tracks are
// the upload's stream indexes (flow2.upload.audio, flow2.upload.subtitles). subtitles may
// come with it (the job's subtitle files, handed over after the import); overrides and
// seasonDub may not. The worker merges, Radarr replaces the film's file with the merged one
// through Manual Import, and the film is unmonitored (merge.unmonitorAfterMerge). Audit:
// merge.confirmed. The job's "import.merge" and "result.merge" show it. Errors besides the
// common ones:
//
//	409 merge_not_offered  the upload cannot be merged (flow2.notOffered says why), or the
//	                       job is not Radarr's
//	409 preview_required   no ready preview of the upload with this offset and audio track
//	                       (or a new one was requested meanwhile): render and watch the clips
//	409 library_changed    Radarr's file for the film changed since the match; the job went
//	                       back to probed, to be matched again
//	400 bad_request        a track that is not the upload's, no audio track, a language tag
//	                       not in canonical BCP 47 form, an invalid track name; flow2 or merge
//	                       for a job that is not flow 2; overrides or seasonDub with a merge
//
// The import's own refusals:
//
//	400 bad_request        an override names no row of the job; a subtitle choice names no
//	                       subtitle file of the job, a file twice, or an unknown language
//	503 storage_unavailable  a sentinel is missing, or the intake and library roots are on
//	                       different filesystems (importMode move)
//	502 arr_unavailable    the app did not answer (a show's episodes are read to judge the rows)
//
// The import and its result then show in GET /api/jobs/{id} ("import", "result").

func (s *Server) confirmRoutes(mux *http.ServeMux) {
	if s.deps.Store == nil || s.deps.Imports == nil {
		return
	}
	mux.HandleFunc("POST /api/jobs/{id}/confirm", s.confirmJob)
}

// rowsErrorBody is the error shape of a refusal about rows.
type rowsErrorBody struct {
	Error rowsError `json:"error"`
}

type rowsError struct {
	Code    string   `json:"code"`
	Message string   `json:"message"`
	Rows    []string `json:"rows"`
}

func (s *Server) confirmJob(w http.ResponseWriter, r *http.Request) {
	u, j, ok := s.ownJob(w, r)
	if !ok {
		return
	}
	var req flows.ConfirmRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	c, err := s.deps.Imports.Confirm(r.Context(), u.Name, u.Email, j, req)
	if err != nil {
		s.confirmError(w, r, err)
		return
	}
	// The job is importing, with its audit entries (stored in the same transaction),
	// whether or not the client is still there.
	dctx, cancel := context.WithTimeout(context.WithoutCancel(r.Context()), 30*time.Second)
	defer cancel()
	files, uploads, err := s.parts(dctx, c.Job)
	if err != nil {
		s.internalError(w, r, err)
		return
	}
	writeJSON(w, http.StatusAccepted, s.view(r.Context(), u, c.Job, files, uploads))
}

func (s *Server) confirmError(w http.ResponseWriter, r *http.Request, err error) {
	var e *flows.Error
	if !errors.As(err, &e) {
		s.matchError(w, r, err)
		return
	}
	switch e.Code {
	case flows.CodeNeedsOverride, flows.CodeIncompleteRows, flows.CodeSeasonDubChoice, flows.CodeSharedFile, flows.CodeVersionUpgrade, flows.CodeVersionUnchecked:
		writeJSON(w, http.StatusConflict, rowsErrorBody{rowsError{Code: e.Code, Message: e.Message, Rows: e.Rows}})
	case flows.CodeVersionUnavailable, flows.CodeVersionExists:
		if len(e.Rows) > 0 { // episodes' versions (#58): the rows that cannot be one
			writeJSON(w, http.StatusConflict, rowsErrorBody{rowsError{Code: e.Code, Message: e.Message, Rows: e.Rows}})
			return
		}
		writeError(w, http.StatusConflict, e.Code, e.Message)
	case flows.CodeNothingToImport, flows.CodeVersionLabel, flows.CodeLibraryChanged, flows.CodeMergeNotOffered, flows.CodePreviewRequired:
		writeError(w, http.StatusConflict, e.Code, e.Message)
	case flows.CodeStorageUnavailable:
		writeError(w, http.StatusServiceUnavailable, e.Code, e.Message)
	default:
		s.matchError(w, r, err)
	}
}
