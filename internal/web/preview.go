package web

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"strconv"
	"syscall"
	"time"

	"github.com/segun-io/fukku/internal/auth"
	"github.com/segun-io/fukku/internal/flows"
	"github.com/segun-io/fukku/internal/jobdir"
	"github.com/segun-io/fukku/internal/preview"
	"github.com/segun-io/fukku/internal/store"
)

// The sync-check clips of flow 2a (#53). Jobs are addressed by id and clips by number;
// no request carries a path. The clips are files the worker wrote; this process streams
// them as they are and never parses them.
//
//	POST /api/jobs/{id}/preview     {"offsetMs": -800, "audioTrack": 1}  (both optional; an
//	                                empty body is {})
//	                                → 202 Preview (state pending); 200 when the same offset
//	                                  and track are already pending or ready
//	GET  /api/jobs/{id}/preview     → 200 Preview
//	GET  /api/jobs/{id}/preview/{n} → 200 or 206: clip n (1-based), video/mp4, with range
//	                                  requests; 404 for a number not in the list
//
// offsetMs (default 0, from −60000 to 60000) delays the upload's audio against the
// library file's video, as mkvmerge's --sync does: an audio track that starts 800 ms late
// is brought into sync with −800. audioTrack is the index of one of flow2.upload.audio in
// GET /api/jobs/{id} (default: the upload's default audio track, else its first). A new
// request replaces the earlier clips, and its offset and track are kept with the job for
// the merge.
//
//	Preview: {"state": "ready",                 none, pending, ready or failed
//	          "offsetMs": -800, "audioTrack": 1, null while state is none
//	          "requestedAt": "…", "requestedBy": "user",
//	          "error": null,                     when failed: {"code": "timeout", "message": "…"}
//	          "clips": [{"n": 1, "startMs": 0, "lengthMs": 20000, "size": 812345,
//	                     "url": "/api/jobs/<id>/preview/1"}, …]}   empty unless ready
//
// Error codes besides the common ones: 409 not_flow2 (the job is not flow 2),
// 409 wrong_state (POST: the job is not awaiting confirmation), 409 merge_not_offered
// (the upload is not the same cut, or merge.enabled is off), 409 no_audio, 400 bad_request
// (an offset out of range, an audioTrack that is not one of the upload's).
// Audit: preview.requested, with the offset and the track.

func (s *Server) previewRoutes(mux *http.ServeMux) {
	if s.deps.Store == nil || s.deps.Proposals == nil {
		return
	}
	mux.HandleFunc("POST /api/jobs/{id}/preview", s.requestPreview)
	mux.HandleFunc("GET /api/jobs/{id}/preview", s.getPreview)
	mux.HandleFunc("GET /api/jobs/{id}/preview/{n}", s.previewClip)
}

type previewJSON struct {
	State       string            `json:"state"`
	OffsetMs    *int64            `json:"offsetMs"`
	AudioTrack  *int              `json:"audioTrack"`
	RequestedAt *time.Time        `json:"requestedAt"`
	RequestedBy *string           `json:"requestedBy"`
	Error       *jobError         `json:"error"`
	Clips       []previewClipJSON `json:"clips"`
}

type previewClipJSON struct {
	N        int    `json:"n"`
	StartMs  int64  `json:"startMs"`
	LengthMs int64  `json:"lengthMs"`
	Size     int64  `json:"size"`
	URL      string `json:"url"`
}

// previewMessages are the English texts of a failed preview; admins see the detail.
var previewMessages = map[string]string{
	preview.RefusalOutsideLibrary:    "The library file is outside the folders Fukku may read.",
	preview.RefusalLibraryUnreadable: "The library file could not be read.",
	preview.RefusalUploadUnreadable:  "The uploaded file could not be read.",
	preview.RefusalRenderFailed:      "The clips could not be made from these files.",
	preview.RefusalTimeout:           "Making a clip took too long.",
	preview.RefusalTooLarge:          "A clip came out too large.",
}

func previewView(u auth.User, jobID string, pv flows.Preview, ok bool) previewJSON {
	v := previewJSON{State: "none", Clips: []previewClipJSON{}}
	if !ok {
		return v
	}
	at := pv.RequestedAt.UTC()
	v.State, v.OffsetMs, v.AudioTrack, v.RequestedAt, v.RequestedBy = pv.State, &pv.OffsetMs, &pv.AudioTrack, &at, &pv.RequestedBy
	if pv.State == flows.PreviewFailed {
		msg, known := previewMessages[pv.ErrorCode]
		if !known {
			msg = "The clips could not be made."
		}
		if u.Admin && pv.ErrorText != "" {
			msg = pv.ErrorText
		}
		v.Error = &jobError{Code: pv.ErrorCode, Message: msg}
	}
	if pv.State == flows.PreviewReady {
		for _, c := range pv.Clips {
			v.Clips = append(v.Clips, previewClipJSON{
				N: c.N, StartMs: c.StartMs, LengthMs: c.LengthMs, Size: c.Size,
				URL: "/api/jobs/" + jobID + "/preview/" + strconv.Itoa(c.N),
			})
		}
	}
	return v
}

func (s *Server) requestPreview(w http.ResponseWriter, r *http.Request) {
	u, j, ok := s.ownJob(w, r)
	if !ok {
		return
	}
	// Both fields are optional, so an empty body is {}.
	body, err := io.ReadAll(r.Body)
	if err != nil {
		var tooLarge *http.MaxBytesError
		if errors.As(err, &tooLarge) {
			writeError(w, http.StatusRequestEntityTooLarge, "too_large", fmt.Sprintf("The request body is larger than %d bytes.", tooLarge.Limit))
			return
		}
		writeError(w, http.StatusBadRequest, "bad_request", "The request body could not be read.")
		return
	}
	var req flows.PreviewRequest
	if len(bytes.TrimSpace(body)) > 0 {
		r.Body = io.NopCloser(bytes.NewReader(body))
		if !decodeJSON(w, r, &req) {
			return
		}
	}
	pv, created, err := s.deps.Proposals.RequestPreview(r.Context(), u.Name, j, req)
	if err != nil && !created {
		s.previewError(w, r, err)
		return
	}
	status := http.StatusOK
	if created {
		s.audit(r.Context(), u, "preview.requested", j.ID, map[string]string{
			"offsetMs": strconv.FormatInt(pv.OffsetMs, 10), "audioTrack": strconv.Itoa(pv.AudioTrack),
		})
		status = http.StatusAccepted
		if err != nil {
			// The request is stored; the runner's step submits it on its next pass.
			s.log.Warn("preview_api", "job", j.ID, "error", err, "note", "submitted by the next pass")
		}
	}
	writeJSON(w, status, previewView(u, j.ID, pv, true))
}

// flow2Target loads the target of a flow 2 job; it answers and returns false otherwise.
func (s *Server) flow2Target(w http.ResponseWriter, r *http.Request, j store.Job) (store.Target, bool) {
	t, err := s.deps.Store.Target(r.Context(), j.ID)
	if err != nil && !errors.Is(err, store.ErrNotFound) {
		s.internalError(w, r, err)
		return store.Target{}, false
	}
	if _, isFlow2 := flows.Flow2Of(t); err != nil || !isFlow2 {
		s.previewError(w, r, &flows.Error{Code: flows.CodeNotFlow2, Message: "The job is not another language of a film already in the library, so it has no preview."})
		return store.Target{}, false
	}
	return t, true
}

func (s *Server) getPreview(w http.ResponseWriter, r *http.Request) {
	u, j, ok := s.ownJob(w, r)
	if !ok {
		return
	}
	t, ok := s.flow2Target(w, r, j)
	if !ok {
		return
	}
	pv, has := flows.PreviewOf(t)
	writeJSON(w, http.StatusOK, previewView(u, j.ID, pv, has))
}

// previewClip streams one clip of the job's ready preview. Anything else (no such
// number, a preview that is not ready, a file that is missing or not the one the worker
// reported) is 404.
func (s *Server) previewClip(w http.ResponseWriter, r *http.Request) {
	_, j, ok := s.ownJob(w, r)
	if !ok {
		return
	}
	clipNotFound := func() { writeError(w, http.StatusNotFound, "not_found", "No such clip.") }
	t, err := s.deps.Store.Target(r.Context(), j.ID)
	if err != nil && !errors.Is(err, store.ErrNotFound) {
		s.internalError(w, r, err)
		return
	}
	pv, has := flows.PreviewOf(t)
	_, isFlow2 := flows.Flow2Of(t)
	n, nerr := strconv.Atoi(r.PathValue("n"))
	if err != nil || !isFlow2 || !has || pv.State != flows.PreviewReady || !preview.ValidRequest(pv.Request) ||
		nerr != nil || n < 1 || n > len(pv.Clips) || strconv.Itoa(n) != r.PathValue("n") {
		clipNotFound()
		return
	}
	clip := pv.Clips[n-1]
	root, err := os.OpenRoot(jobdir.Dir(s.cfg.Paths.IntakeRoot, j.ID))
	if err != nil {
		clipNotFound()
		return
	}
	defer func() { _ = root.Close() }()
	// O_NONBLOCK: a FIFO put there must not hang the request.
	f, err := root.OpenFile(preview.Dir+"/"+preview.ClipName(pv.Request, n), os.O_RDONLY|syscall.O_NONBLOCK, 0)
	if err != nil {
		clipNotFound()
		return
	}
	defer func() { _ = f.Close() }()
	info, err := f.Stat()
	if err != nil || !info.Mode().IsRegular() || info.Size() != clip.Size {
		clipNotFound()
		return
	}
	h := w.Header()
	h.Set("Content-Type", preview.ContentType)
	h.Set("Cache-Control", "private, no-cache")
	h.Set("ETag", `"`+pv.Request+"-"+strconv.Itoa(n)+`"`)
	http.ServeContent(w, r, "", info.ModTime(), f)
}

func (s *Server) previewError(w http.ResponseWriter, r *http.Request, err error) {
	switch flows.Code(err) {
	case flows.CodeNotFlow2, flows.CodeMergeNotOffered, flows.CodeNoAudio:
		var e *flows.Error
		errors.As(err, &e)
		writeError(w, http.StatusConflict, e.Code, e.Message)
	default:
		s.matchError(w, r, err)
	}
}
