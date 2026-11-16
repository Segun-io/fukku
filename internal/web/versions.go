package web

import (
	"net/http"
	"path"
	"time"
)

// The second versions of films (#56): what Fukku placed outside Radarr's root folders and
// how the media server groups each one now. Admins only (404 for others, like the rest of
// the admin API); the records outlive their jobs.
//
//	GET /api/admin/versions → 200 {"versions": [{"id": 1, "jobId": "…", "app": "radarr",
//	    "titleId": 603, "title": "Film", "year": 2001, "name": "Film (2001) - Español.mkv",
//	    "path": "/data/versions/Film (2001)/Film (2001) - Español.mkv", "label": "Español",
//	    "status": "grouped", "detail": "", "primaryFile": "Film (2001) WEBDL-1080p.mkv",
//	    "createdAt": "…", "checkedAt": "…", "groupedAt": "…", "primaryChangedAt": null}]}
//
// status is placing, linked, grouped, waiting (detail says why: the media server does not
// list a file yet, Radarr has no file for the film), film_deleted (Radarr no longer has the
// film; the file is left on disk) or missing (the file is gone). primaryChangedAt is when
// the version was last grouped with another film file than before: Radarr renamed or
// upgraded it (versions.regroupInterval).
func (s *Server) versionRoutes(mux *http.ServeMux) {
	if s.deps.Store == nil {
		return
	}
	mux.HandleFunc("GET /api/admin/versions", s.adminOnly(s.adminVersions))
}

type versionJSON struct {
	ID               int64      `json:"id"`
	JobID            string     `json:"jobId"`
	App              string     `json:"app"`
	TitleID          int        `json:"titleId"`
	Title            string     `json:"title"`
	Year             int        `json:"year"`
	Name             string     `json:"name"`
	Path             string     `json:"path"`
	Label            string     `json:"label"`
	Status           string     `json:"status"`
	Detail           string     `json:"detail"`
	PrimaryFile      string     `json:"primaryFile"`
	CreatedAt        time.Time  `json:"createdAt"`
	CheckedAt        *time.Time `json:"checkedAt"`
	GroupedAt        *time.Time `json:"groupedAt"`
	PrimaryChangedAt *time.Time `json:"primaryChangedAt"`
}

func (s *Server) adminVersions(w http.ResponseWriter, r *http.Request) {
	recs, err := s.deps.Store.AllVersions(r.Context())
	if err != nil {
		s.internalError(w, r, err)
		return
	}
	at := func(t time.Time) *time.Time {
		if t.IsZero() {
			return nil
		}
		u := t.UTC()
		return &u
	}
	out := make([]versionJSON, 0, len(recs))
	for _, v := range recs {
		j := versionJSON{
			ID: v.ID, JobID: v.JobID, App: v.App, TitleID: v.TitleID, Title: v.Title, Year: v.Year,
			Name: path.Base(v.Path), Path: v.Path, Label: v.Label, Status: v.Status, Detail: v.Detail,
			CreatedAt: v.CreatedAt.UTC(), CheckedAt: at(v.CheckedAt), GroupedAt: at(v.GroupedAt), PrimaryChangedAt: at(v.PrimaryChangedAt),
		}
		if v.PrimaryPath != "" {
			j.PrimaryFile = path.Base(v.PrimaryPath)
		}
		out = append(out, j)
	}
	writeJSON(w, http.StatusOK, map[string][]versionJSON{"versions": out})
}
