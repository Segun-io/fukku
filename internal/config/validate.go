package config

import (
	"errors"
	"fmt"
	"net/netip"
	"net/url"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"time"
)

// Role is the process a configuration is validated for.
type Role string

const (
	RoleWeb      Role = "web"
	RoleWorker   Role = "worker"
	RoleCombined Role = "combined"
)

// ValidateOptions carry command-line facts that settings alone do not express.
type ValidateOptions struct {
	// InsecureDev is the --insecure-dev flag; auth.mode=none refuses to start without it.
	InsecureDev bool
}

type problems []error

func (p *problems) add(key, format string, args ...any) {
	*p = append(*p, fmt.Errorf("%s: %s", key, fmt.Sprintf(format, args...)))
}

// Validate checks cfg for the given role and returns every problem found, joined.
func Validate(cfg Config, role Role, opts ValidateOptions) error {
	var p problems

	oneOf(&p, "mode", cfg.Mode, "split", "combined")
	switch {
	case role == RoleCombined && cfg.Mode != "combined":
		p.add("mode", "the combined command needs mode=combined")
	case role != RoleCombined && cfg.Mode == "combined":
		p.add("mode", "mode=combined runs as one process; start it with `fukku combined`")
	}

	if role == RoleWorker {
		// The worker parses untrusted files: it must not hold any credentials.
		for _, leaf := range Leaves(&cfg) {
			if leaf.Secret && leaf.value.String() != "" {
				p.add(leaf.Key, "the worker must not be given API keys; pass secrets to the web process only")
			}
		}
	} else {
		validateWeb(&p, cfg, opts)
	}
	validateShared(&p, cfg)

	return errors.Join(p...)
}

func validateWeb(p *problems, cfg Config, opts ValidateOptions) {
	s := cfg.Server
	if s.ListenAddress == "" {
		p.add("server.listenAddress", "must be set, e.g. :8080")
	}
	if s.PublicBaseURL != "" {
		httpURL(p, "server.publicBaseUrl", s.PublicBaseURL)
	}
	if len(s.UILanguages) == 0 {
		p.add("server.uiLanguages", "needs at least one language")
	}
	for _, l := range s.UILanguages {
		oneOf(p, "server.uiLanguages", l, "en", "es")
	}
	oneOf(p, "server.logFormat", s.LogFormat, "logfmt", "json")
	absPath(p, "server.dataDir", s.DataDir)
	positive(p, "server.jobStuckAfter", int64(s.JobStuckAfter))
	nonNegative(p, "server.readinessCacheTtl", int64(s.ReadinessCacheTTL))
	positive(p, "server.arrStallBackoff", int64(s.ArrStallBackoff))
	positive(p, "server.arrStallLimit", int64(s.ArrStallLimit))
	positive(p, "server.importPollInterval", int64(s.ImportPollInterval))
	positive(p, "server.postStepAttempts", int64(s.PostStepAttempts))
	positive(p, "server.postStepBackoff", int64(s.PostStepBackoff))

	a := cfg.Auth
	oneOf(p, "auth.mode", a.Mode, "proxyHeaders", "oidc", "none")
	switch a.Mode {
	case "proxyHeaders":
		if len(a.TrustedProxies) == 0 {
			p.add("auth.trustedProxies", "required when auth.mode=proxyHeaders: the addresses or CIDRs the reverse proxy connects from")
		}
		for _, tp := range a.TrustedProxies {
			if _, err := ParsePrefix(tp); err != nil {
				p.add("auth.trustedProxies", "%q is not an address or CIDR", tp)
			}
		}
		if a.UserHeader == "" {
			p.add("auth.userHeader", "required when auth.mode=proxyHeaders")
		}
	case "oidc":
		p.add("auth.mode", "oidc is on the backlog and not available yet; use proxyHeaders")
	case "none":
		if !opts.InsecureDev {
			p.add("auth.mode", "none is for development only and needs the --insecure-dev flag")
		}
	}
	if a.GroupsHeader != "" && a.GroupsSeparator == "" {
		p.add("auth.groupsSeparator", "must be set when auth.groupsHeader is set")
	}

	if cfg.Radarr.URL == "" && cfg.Sonarr.URL == "" {
		p.add("radarr.url", "set radarr.url, sonarr.url or both")
	}
	validateArr(p, "radarr", cfg.Radarr)
	validateArr(p, "sonarr", cfg.Sonarr)

	st := cfg.Subtitles
	oneOf(p, "subtitles.provider", st.Provider, "bazarr", "none")
	if st.Provider == "bazarr" {
		service(p, "subtitles", st.URL, st.APIKey)
	}
	positive(p, "subtitles.requestTimeout", int64(st.RequestTimeout))
	positive(p, "subtitles.attempts", int64(st.Attempts))
	positive(p, "subtitles.confirmTimeout", int64(st.ConfirmTimeout))
	positive(p, "subtitles.pollInterval", int64(st.PollInterval))
	positive(p, "subtitles.retryBackoff", int64(st.RetryBackoff))
	positive(p, "subtitles.maxFileSize", int64(st.MaxFileSize))

	ms := cfg.MediaServer
	oneOf(p, "mediaServer.provider", ms.Provider, "jellyfin", "none")
	oneOf(p, "mediaServer.refreshMethod", ms.RefreshMethod, "libraryRefresh", "pathUpdate", "libraryScan", "none")
	if ms.Provider == "jellyfin" {
		service(p, "mediaServer", ms.URL, ms.APIKey)
	}
	positive(p, "mediaServer.requestTimeout", int64(ms.RequestTimeout))
	positive(p, "mediaServer.itemWait", int64(ms.ItemWait))
	positive(p, "mediaServer.itemPollInterval", int64(ms.ItemPollInterval))
	positive(p, "worker.heartbeatMaxAge", int64(cfg.Worker.HeartbeatMaxAge))
}

func validateArr(p *problems, name string, a Arr) {
	if a.URL != "" {
		service(p, name, a.URL, a.APIKey)
	}
	if a.PublicURL != "" {
		httpURL(p, name+".publicUrl", a.PublicURL)
	}
	oneOf(p, name+".importMode", a.ImportMode, "move", "copy")
	oneOf(p, name+".minimumAvailability", a.MinimumAvailability, "announced", "inCinemas", "released")
	positive(p, name+".requestTimeout", int64(a.RequestTimeout))
}

func validateShared(p *problems, cfg Config) {
	pa := cfg.Paths
	absPath(p, "paths.intakeRoot", pa.IntakeRoot)
	for _, r := range pa.LibraryRoots {
		absPath(p, "paths.libraryRoots", r)
	}
	for _, s := range pa.Sentinels {
		absPath(p, "paths.sentinels", s)
	}
	for key, list := range map[string][]PathMapping{
		"paths.mappings.radarr": pa.Mappings.Radarr, "paths.mappings.sonarr": pa.Mappings.Sonarr,
		"paths.mappings.subtitles": pa.Mappings.Subtitles, "paths.mappings.mediaServer": pa.Mappings.MediaServer,
	} {
		for _, m := range list {
			if !filepath.IsAbs(m.From) || !strings.HasPrefix(m.To, "/") && !isWindowsAbs(m.To) {
				p.add(key, "from and to must both be absolute paths (got %q → %q)", m.From, m.To)
			}
		}
	}
	oneOf(p, "paths.requireSameFilesystem", pa.RequireSameFilesystem, "auto", "always", "never")

	u := cfg.Upload
	positive(p, "upload.chunkSize", int64(u.ChunkSize))
	positive(p, "upload.maxFileSize", int64(u.MaxFileSize))
	if u.ChunkSize > u.MaxFileSize {
		p.add("upload.chunkSize", "is larger than upload.maxFileSize")
	}
	if u.MaxInFlight < u.MaxFileSize {
		p.add("upload.maxInFlight", "is smaller than upload.maxFileSize, so no file could ever be uploaded")
	}
	positive(p, "upload.maxParallelPerUser", int64(u.MaxParallelPerUser))
	nonNegative(p, "upload.freeSpaceMargin", int64(u.FreeSpaceMargin))
	extensions(p, "upload.videoExtensions", u.VideoExtensions, true)
	extensions(p, "upload.subtitleExtensions", u.SubtitleExtensions, false)
	extensions(p, "upload.archiveExtensions", u.ArchiveExtensions, false)
	positive(p, "upload.idleUploadExpiry", int64(u.IdleUploadExpiry))
	positive(p, "upload.requestIdleTimeout", int64(u.RequestIdleTimeout))
	positive(p, "upload.jobRetention", int64(u.JobRetention))
	positive(p, "upload.finishedJobRetention", int64(u.FinishedJobRetention))
	positive(p, "upload.sweepInterval", int64(u.SweepInterval))
	nonNegative(p, "upload.largeFileHintThreshold", int64(u.LargeFileHintThreshold))

	e := cfg.Extract
	positive(p, "extract.maxEntries", int64(e.MaxEntries))
	positive(p, "extract.maxWrittenBytes", int64(e.MaxWrittenBytes))
	if e.MaxRatio <= 1 {
		p.add("extract.maxRatio", "must be greater than 1")
	}
	nonNegative(p, "extract.ratioFloor", int64(e.RatioFloor))
	if e.MaxNameBytes < 16 || e.MaxNameBytes > 255 {
		p.add("extract.maxNameBytes", "must be between 16 and 255")
	}
	if e.MaxPathSegments < 1 || e.MaxPathSegments > 255 {
		p.add("extract.maxPathSegments", "must be between 1 and 255")
	}
	if e.MaxPathBytes < e.MaxNameBytes || e.MaxPathBytes > 3072 {
		p.add("extract.maxPathBytes", "must be between extract.maxNameBytes and 3072")
	}
	for _, t := range e.AllowedTypes {
		oneOf(p, "extract.allowedTypes", t, "zip")
	}

	m := cfg.Match
	positive(p, "match.runtimeTolerance", int64(m.RuntimeTolerance))
	nonNegative(p, "match.lookupCacheTtl", int64(m.LookupCacheTTL))
	if m.PALRatio <= 1 || m.PALTolerance <= 0 || m.PALTolerance >= m.PALRatio-1 {
		p.add("match.palRatio", "palRatio must be > 1 and palTolerance between 0 and palRatio-1")
	}
	oneOf(p, "match.defaultSource", m.DefaultSource, "WEBDL", "WEBRIP", "BLURAY", "HDTV", "DVD")
	if m.DoubleEpisodeRatio <= 1 || m.DoubleEpisodeTolerance <= 0 || m.DoubleEpisodeTolerance >= 1 {
		p.add("match.doubleEpisodeRatio", "doubleEpisodeRatio must be > 1 and doubleEpisodeTolerance between 0 and 1")
	}
	if m.RuntimeMismatchTolerance <= 0 || m.RuntimeMismatchTolerance >= 1 {
		p.add("match.runtimeMismatchTolerance", "must be between 0 and 1")
	}
	if m.DVDOrderShare <= 0 || m.DVDOrderShare >= 1 {
		p.add("match.dvdOrderShare", "must be between 0 and 1")
	}

	mg := cfg.Merge
	if mg.PreviewClips < 1 || mg.PreviewClips > 10 {
		p.add("merge.previewClips", "must be between 1 and 10")
	}
	if mg.PreviewClipLength <= 0 || mg.PreviewClipLength.D() > 2*time.Minute {
		p.add("merge.previewClipLength", "must be more than 0 and at most 2m")
	}
	if mg.PreviewHeight < 144 || mg.PreviewHeight > 2160 || mg.PreviewHeight%2 != 0 {
		p.add("merge.previewHeight", "must be an even number of pixels from 144 to 2160")
	}
	if mg.PreviewVideoBitrate < 100 || mg.PreviewVideoBitrate > 20000 {
		p.add("merge.previewVideoBitrate", "must be from 100 to 20000 (kbit/s)")
	}
	positive(p, "merge.previewTimeout", int64(mg.PreviewTimeout))
	if mg.LanguageTag == "" {
		p.add("merge.languageTag", "must be a BCP 47 tag such as es-419")
	}

	v := cfg.Versions
	oneOf(p, "versions.strategy", v.Strategy, "naming", "mergeApi")
	if strings.TrimSpace(v.LabelTemplate) == "" {
		p.add("versions.labelTemplate", "must not be empty")
	}

	sd := cfg.SeasonDub
	policies := []string{"merge", "version", "replace", "subtitlesOnly"}
	oneOf(p, "seasonDub.policy", sd.Policy, append(policies, "ask")...)
	for _, o := range sd.Order {
		oneOf(p, "seasonDub.order", o, policies...)
	}

	w := cfg.Worker
	if w.Concurrency < 1 || w.Concurrency > 16 {
		p.add("worker.concurrency", "must be between 1 and 16")
	}
	absPath(p, "worker.spoolDir", w.SpoolDir)
	positive(p, "worker.pollInterval", int64(w.PollInterval))
	positive(p, "worker.probeTimeout", int64(w.ProbeTimeout))
	if len(w.LibraryFormats) == 0 {
		p.add("worker.libraryFormats", "must name at least one demuxer")
	}
	for _, f := range w.LibraryFormats {
		if !demuxerName.MatchString(f) {
			p.add("worker.libraryFormats", "%q is not a demuxer name (lower-case letters, digits, _ and +)", f)
		} else if slices.Contains(networkDemuxers, f) {
			p.add("worker.libraryFormats", "%q reads playlists or network streams and is not allowed", f)
		}
	}
	positive(p, "worker.taskTimeout", int64(w.TaskTimeout))
	if w.Nice < 0 || w.Nice > 19 {
		p.add("worker.nice", "must be between 0 and 19")
	}
	positive(p, "worker.heartbeatInterval", int64(w.HeartbeatInterval))
	if w.HeartbeatMaxAge <= w.HeartbeatInterval {
		p.add("worker.heartbeatMaxAge", "must be longer than worker.heartbeatInterval")
	}
	validateMergeTrack(p, cfg.Merge)
}

func oneOf(p *problems, key, val string, allowed ...string) {
	if !slices.Contains(allowed, val) {
		p.add(key, "%q is not one of %s", val, strings.Join(allowed, ", "))
	}
}

func positive(p *problems, key string, v int64) {
	if v <= 0 {
		p.add(key, "must be greater than zero")
	}
}

func nonNegative(p *problems, key string, v int64) {
	if v < 0 {
		p.add(key, "must not be negative")
	}
}

func absPath(p *problems, key, path string) {
	if !filepath.IsAbs(path) {
		p.add(key, "must be an absolute path, got %q", path)
	}
}

func httpURL(p *problems, key, raw string) {
	u, err := url.Parse(raw)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		p.add(key, "must be an http:// or https:// URL, got %q", raw)
		return
	}
	if u.User != nil {
		p.add(key, "must not contain credentials")
	}
}

func service(p *problems, group, rawURL string, key Secret) {
	if rawURL == "" {
		p.add(group+".url", "required")
	} else {
		httpURL(p, group+".url", rawURL)
	}
	if key == "" {
		p.add(group+".apiKey", "required (set %s or %s_FILE)", envFor(group+".apiKey"), envFor(group+".apiKey"))
	}
}

func extensions(p *problems, key string, list []string, required bool) {
	if required && len(list) == 0 {
		p.add(key, "needs at least one extension")
	}
	for _, e := range list {
		if !strings.HasPrefix(e, ".") || strings.ContainsAny(e, "/\\ ") || e != strings.ToLower(e) {
			p.add(key, "%q must be lower case and start with a dot, e.g. .mkv", e)
		}
	}
}

// ParsePrefix parses an auth.trustedProxies entry: an address (one host) or a CIDR. The
// result is masked, and an IPv4-mapped IPv6 prefix (::ffff:192.0.2.0/120) becomes its IPv4
// form, so it matches the IPv4 addresses it names.
func ParsePrefix(s string) (netip.Prefix, error) {
	s = strings.TrimSpace(s)
	var p netip.Prefix
	if strings.Contains(s, "/") {
		var err error
		if p, err = netip.ParsePrefix(s); err != nil {
			return netip.Prefix{}, err
		}
	} else {
		a, err := netip.ParseAddr(s)
		if err != nil {
			return netip.Prefix{}, err
		}
		if a.Zone() != "" {
			return netip.Prefix{}, fmt.Errorf("%q: zoned addresses are not supported", s)
		}
		p = netip.PrefixFrom(a, a.BitLen())
	}
	if a := p.Addr(); a.Is4In6() && p.Bits() >= 96 {
		p = netip.PrefixFrom(a.Unmap(), p.Bits()-96)
	}
	return p.Masked(), nil
}

func isWindowsAbs(p string) bool {
	return len(p) >= 3 && p[1] == ':' && (p[2] == '\\' || p[2] == '/')
}

func envFor(key string) string {
	return envName(strings.Split(key, "."))
}

var demuxerName = regexp.MustCompile(`^[a-z0-9_+]+$`)

// networkDemuxers are demuxers that follow references to other files or URLs (playlists,
// scripts, streaming protocols). The worker also allows only the "fd" protocol, but they
// have no place in a whitelist of container formats.
// Names are ffprobe 7.1's demuxers (`ffprobe -demuxers`).
var networkDemuxers = []string{"hls", "dash", "concat", "rtsp", "rtp", "sdp", "sap", "imf"}
