package web

import (
	"net/http"

	"github.com/segun-io/fukku/internal/auth"
	"github.com/segun-io/fukku/internal/version"
)

// The answer of GET /api/session (the contract of issue #5): who is signed in and the
// settings the UI needs. It never carries secrets: no API keys, no URLs of the apps.
type sessionInfo struct {
	User        sessionUser   `json:"user"`
	Version     string        `json:"version"`
	AuthMode    string        `json:"authMode"`
	InsecureDev bool          `json:"insecureDev"`
	UILanguages []string      `json:"uiLanguages"`
	Apps        sessionApps   `json:"apps"`
	Upload      sessionUpload `json:"upload"`
}

type sessionUser struct {
	Name  string `json:"name"`
	Email string `json:"email"`
	Admin bool   `json:"admin"`
}

type sessionApps struct {
	Radarr bool `json:"radarr"`
	Sonarr bool `json:"sonarr"`
}

type sessionUpload struct {
	Endpoint               string   `json:"endpoint"`
	ChunkSize              int64    `json:"chunkSize"`
	MaxFileSize            int64    `json:"maxFileSize"`
	MaxParallelPerUser     int      `json:"maxParallelPerUser"`
	VideoExtensions        []string `json:"videoExtensions"`
	SubtitleExtensions     []string `json:"subtitleExtensions"`
	ArchiveExtensions      []string `json:"archiveExtensions"`
	LargeFileHintThreshold int64    `json:"largeFileHintThreshold"`
}

// session answers GET /api/session, the first request the UI makes.
func (s *Server) session(w http.ResponseWriter, r *http.Request) {
	u, ok := auth.FromContext(r.Context())
	if !ok {
		s.log.Error("session_without_user", "path", r.URL.Path)
		writeError(w, http.StatusInternalServerError, "internal", "The request has no user.")
		return
	}
	c := s.cfg
	writeJSON(w, http.StatusOK, sessionInfo{
		User:        sessionUser{Name: u.Name, Email: u.Email, Admin: u.Admin},
		Version:     version.Version,
		AuthMode:    c.Auth.Mode,
		InsecureDev: c.Auth.Mode == "none",
		UILanguages: list(c.Server.UILanguages),
		Apps:        sessionApps{Radarr: c.Radarr.URL != "", Sonarr: c.Sonarr.URL != ""},
		Upload: sessionUpload{
			Endpoint:               uploadsPath,
			ChunkSize:              int64(c.Upload.ChunkSize),
			MaxFileSize:            int64(c.Upload.MaxFileSize),
			MaxParallelPerUser:     c.Upload.MaxParallelPerUser,
			VideoExtensions:        list(c.Upload.VideoExtensions),
			SubtitleExtensions:     list(c.Upload.SubtitleExtensions),
			ArchiveExtensions:      list(c.Upload.ArchiveExtensions),
			LargeFileHintThreshold: int64(c.Upload.LargeFileHintThreshold),
		},
	})
}

// list makes an empty list [] in JSON rather than null.
func list(l []string) []string {
	if l == nil {
		return []string{}
	}
	return l
}
