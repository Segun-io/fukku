package config

import (
	"errors"
	"io/fs"
	"strings"
	"testing"
	"time"
)

// validWeb is the smallest environment that passes web validation.
var validWeb = []string{
	"FUKKU_AUTH_TRUSTED_PROXIES=192.0.2.10",
	"FUKKU_RADARR_URL=http://radarr:7878",
	"FUKKU_RADARR_API_KEY=00000000000000000000000000000000",
}

func files(m map[string]string) func(string) ([]byte, error) {
	return func(name string) ([]byte, error) {
		if v, ok := m[name]; ok {
			return []byte(v), nil
		}
		return nil, fs.ErrNotExist
	}
}

func mustLoad(t *testing.T, opts Options) Config {
	t.Helper()
	if opts.Environ == nil {
		opts.Environ = []string{}
	}
	if opts.ReadFile == nil {
		opts.ReadFile = files(nil)
	}
	cfg, err := Load(opts)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	return cfg
}

func TestDefaultsMatchPlan(t *testing.T) {
	d := Defaults()
	checks := map[string]bool{
		"chunk 16 MiB":       d.Upload.ChunkSize == 16<<20,
		"80 GiB per file":    d.Upload.MaxFileSize == 80<<30,
		"300 GiB in flight":  d.Upload.MaxInFlight == 300<<30,
		"100 GiB margin":     d.Upload.FreeSpaceMargin == 100<<30,
		"500 entries":        d.Extract.MaxEntries == 500,
		"ratio 20":           d.Extract.MaxRatio == 20,
		"200 GiB written":    d.Extract.MaxWrittenBytes == 200<<30,
		"0.5 s tolerance":    d.Match.RuntimeTolerance.D() == 500*time.Millisecond,
		"14 day retention":   d.Upload.JobRetention.D() == 14*24*time.Hour,
		"versions off":       !d.Versions.Enabled,
		"proxyHeaders":       d.Auth.Mode == "proxyHeaders",
		"origin check on":    d.Auth.OriginCheck,
		"es-419":             d.Merge.LanguageTag == "es-419",
		"split mode":         d.Mode == "split",
		"sonarr unmonitored": !d.Sonarr.MonitorAfterImport && d.Radarr.MonitorAfterImport,
	}
	for name, ok := range checks {
		if !ok {
			t.Errorf("default %s does not hold", name)
		}
	}
}

func TestDefaultsWithMinimalEnvAreValid(t *testing.T) {
	cfg := mustLoad(t, Options{Environ: validWeb})
	if err := Validate(cfg, RoleWeb, ValidateOptions{}); err != nil {
		t.Fatalf("Validate: %v", err)
	}
	if err := Validate(mustLoad(t, Options{}), RoleWorker, ValidateOptions{}); err != nil {
		t.Fatalf("worker with defaults: %v", err)
	}
}

func TestEnvNames(t *testing.T) {
	want := map[string]string{
		"server.listenAddress":          "FUKKU_SERVER_LISTEN_ADDRESS",
		"server.publicBaseUrl":          "FUKKU_SERVER_PUBLIC_BASE_URL",
		"radarr.apiKey":                 "FUKKU_RADARR_API_KEY",
		"mediaServer.titleLinkTemplate": "FUKKU_MEDIA_SERVER_TITLE_LINK_TEMPLATE",
		"paths.mappings.mediaServer":    "FUKKU_PATHS_MAPPINGS_MEDIA_SERVER",
		"match.palRatio":                "FUKKU_MATCH_PAL_RATIO",
	}
	cfg := Defaults()
	got := map[string]string{}
	for _, l := range Leaves(&cfg) {
		got[l.Key] = l.Env
	}
	for k, v := range want {
		if got[k] != v {
			t.Errorf("%s: env %q, want %q", k, got[k], v)
		}
	}
}

func TestFileThenEnvPrecedence(t *testing.T) {
	yamlFile := `
server:
  listenAddress: ":9000"
  logFormat: json
upload:
  chunkSize: 8MiB
  jobRetention: 7d
paths:
  mappings:
    radarr:
      - {from: /data/intake, to: /intake}
`
	cfg := mustLoad(t, Options{
		File:     "/etc/fukku.yaml",
		ReadFile: files(map[string]string{"/etc/fukku.yaml": yamlFile}),
		Environ: []string{
			"FUKKU_SERVER_LISTEN_ADDRESS=:9100",
			"FUKKU_UPLOAD_MAX_FILE_SIZE=1.5GiB",
			"FUKKU_SERVER_UI_LANGUAGES=es, en",
			"FUKKU_AUTH_ALL_USERS_ARE_ADMINS=true",
			"FUKKU_EXTRACT_MAX_RATIO=12.5",
			`FUKKU_PATHS_MAPPINGS_SONARR=[{"from":"/data/intake","to":"/srv/intake"}]`,
			"PATH=/usr/bin", // ignored: not ours
		},
	})
	if cfg.Server.ListenAddress != ":9100" {
		t.Errorf("env must win over file: %q", cfg.Server.ListenAddress)
	}
	if cfg.Server.LogFormat != "json" {
		t.Errorf("file must win over default: %q", cfg.Server.LogFormat)
	}
	if cfg.Upload.ChunkSize != 8<<20 || cfg.Upload.MaxFileSize != 3<<29 {
		t.Errorf("sizes: %d %d", cfg.Upload.ChunkSize, cfg.Upload.MaxFileSize)
	}
	if cfg.Upload.JobRetention.D() != 7*24*time.Hour {
		t.Errorf("retention %v", cfg.Upload.JobRetention)
	}
	if strings.Join(cfg.Server.UILanguages, ",") != "es,en" || !cfg.Auth.AllUsersAreAdmins || cfg.Extract.MaxRatio != 12.5 {
		t.Errorf("env scalars not applied: %+v %+v", cfg.Server, cfg.Auth)
	}
	if len(cfg.Paths.Mappings.Radarr) != 1 || cfg.Paths.Mappings.Radarr[0].To != "/intake" {
		t.Errorf("yaml mappings: %+v", cfg.Paths.Mappings.Radarr)
	}
	if len(cfg.Paths.Mappings.Sonarr) != 1 || cfg.Paths.Mappings.Sonarr[0].To != "/srv/intake" {
		t.Errorf("env mappings: %+v", cfg.Paths.Mappings.Sonarr)
	}
}

func TestSecretFromFile(t *testing.T) {
	cfg := mustLoad(t, Options{
		Environ:  []string{"FUKKU_SONARR_API_KEY_FILE=/run/secrets/sonarr"},
		ReadFile: files(map[string]string{"/run/secrets/sonarr": "abc123\n"}),
	})
	if cfg.Sonarr.APIKey.Reveal() != "abc123" {
		t.Fatalf("secret file not read or not trimmed: %q", cfg.Sonarr.APIKey.Reveal())
	}
	if s := cfg.Sonarr.APIKey.String(); s != "[redacted]" {
		t.Fatalf("secret printed as %q", s)
	}
}

func TestLoadErrors(t *testing.T) {
	cases := map[string]Options{
		"unknown env":         {Environ: []string{"FUKKU_SERVER_LISTEN_ADDR=:1"}},
		"bad bool":            {Environ: []string{"FUKKU_SERVER_METRICS=yes please"}},
		"bad size":            {Environ: []string{"FUKKU_UPLOAD_CHUNK_SIZE=16 potatoes"}},
		"bad duration":        {Environ: []string{"FUKKU_UPLOAD_JOB_RETENTION=fortnight"}},
		"both secret forms":   {Environ: []string{"FUKKU_RADARR_API_KEY=a", "FUKKU_RADARR_API_KEY_FILE=/x"}},
		"missing secret":      {Environ: []string{"FUKKU_RADARR_API_KEY_FILE=/nope"}},
		"unknown yaml key":    {File: "/c.yaml", ReadFile: files(map[string]string{"/c.yaml": "server:\n  listenAddr: x\n"})},
		"no file":             {File: "/missing.yaml"},
		"_FILE on non-secret": {Environ: []string{"FUKKU_SERVER_LISTEN_ADDRESS_FILE=/x"}},
	}
	for name, opts := range cases {
		t.Run(name, func(t *testing.T) {
			if opts.ReadFile == nil {
				opts.ReadFile = files(nil)
			}
			if _, err := Load(opts); err == nil {
				t.Fatal("expected an error")
			}
		})
	}
}

func TestValidationErrors(t *testing.T) {
	cases := map[string]struct {
		env  []string
		role Role
		dev  bool
		want string
	}{
		"no trusted proxies":     {env: validWeb[1:], role: RoleWeb, want: "auth.trustedProxies"},
		"bad CIDR":               {env: append([]string{"FUKKU_AUTH_TRUSTED_PROXIES=192.0.2.0/33"}, validWeb[1:]...), role: RoleWeb, want: "auth.trustedProxies"},
		"no arr":                 {env: validWeb[:1], role: RoleWeb, want: "radarr.url"},
		"arr without key":        {env: validWeb[:2], role: RoleWeb, want: "radarr.apiKey"},
		"auth none no flag":      {env: append([]string{"FUKKU_AUTH_MODE=none"}, validWeb[1:]...), role: RoleWeb, want: "--insecure-dev"},
		"oidc not yet":           {env: append([]string{"FUKKU_AUTH_MODE=oidc"}, validWeb[1:]...), role: RoleWeb, want: "backlog"},
		"bazarr no url":          {env: append([]string{"FUKKU_SUBTITLES_PROVIDER=bazarr"}, validWeb...), role: RoleWeb, want: "subtitles.url"},
		"url with creds":         {env: append([]string{"FUKKU_RADARR_URL=http://u:p@radarr"}, validWeb[:1]...), role: RoleWeb, want: "credentials"},
		"worker with key":        {env: []string{"FUKKU_RADARR_API_KEY=k"}, role: RoleWorker, want: "must not be given API keys"},
		"relative path":          {env: []string{"FUKKU_PATHS_INTAKE_ROOT=data"}, role: RoleWorker, want: "paths.intakeRoot"},
		"chunk > file":           {env: []string{"FUKKU_UPLOAD_CHUNK_SIZE=2GiB", "FUKKU_UPLOAD_MAX_FILE_SIZE=1GiB"}, role: RoleWorker, want: "upload.chunkSize"},
		"bad extension":          {env: []string{"FUKKU_UPLOAD_VIDEO_EXTENSIONS=mkv"}, role: RoleWorker, want: "upload.videoExtensions"},
		"empty library formats":  {env: []string{"FUKKU_WORKER_LIBRARY_FORMATS="}, role: RoleWorker, want: "worker.libraryFormats"},
		"playlist format":        {env: []string{"FUKKU_WORKER_LIBRARY_FORMATS=matroska,hls"}, role: RoleWorker, want: "worker.libraryFormats"},
		"bad format name":        {env: []string{"FUKKU_WORKER_LIBRARY_FORMATS=mp4;rm"}, role: RoleWorker, want: "worker.libraryFormats"},
		"sweep below extraction": {env: []string{"FUKKU_EXTRACT_MAX_ENTRIES=30000"}, role: RoleWorker, want: "worker.sweepMaxEntries"},
		"sweep too small":        {env: []string{"FUKKU_WORKER_SWEEP_MAX_ENTRIES=100"}, role: RoleWorker, want: "worker.sweepMaxEntries"},
		"sweep without time":     {env: []string{"FUKKU_WORKER_SWEEP_MAX_DURATION=0s"}, role: RoleWorker, want: "worker.sweepMaxDuration"},
		"bad policy":             {env: []string{"FUKKU_SEASON_DUB_POLICY=yolo"}, role: RoleWorker, want: "seasonDub.policy"},
		"bad availability":       {env: append([]string{"FUKKU_RADARR_MINIMUM_AVAILABILITY=soon"}, validWeb...), role: RoleWeb, want: "radarr.minimumAvailability"},
		"bad refresh method":     {env: append([]string{"FUKKU_MEDIA_SERVER_REFRESH_METHOD=sometimes"}, validWeb...), role: RoleWeb, want: "mediaServer.refreshMethod"},
		"zero item wait":         {env: append([]string{"FUKKU_MEDIA_SERVER_ITEM_WAIT=0s"}, validWeb...), role: RoleWeb, want: "mediaServer.itemWait"},
		"combined mismatch":      {env: []string{"FUKKU_MODE=combined"}, role: RoleWorker, want: "fukku combined"},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			cfg := mustLoad(t, Options{Environ: c.env})
			err := Validate(cfg, c.role, ValidateOptions{InsecureDev: c.dev})
			if err == nil || !strings.Contains(err.Error(), c.want) {
				t.Fatalf("want error mentioning %q, got %v", c.want, err)
			}
		})
	}
}

func TestRefreshMethodDefaultsToLibraryRefreshAndAcceptsEveryMethod(t *testing.T) {
	if got := Defaults().MediaServer.RefreshMethod; got != "libraryRefresh" {
		t.Errorf("default refresh method = %q", got)
	}
	for _, m := range []string{"libraryRefresh", "pathUpdate", "libraryScan", "none"} {
		env := append([]string{"FUKKU_MEDIA_SERVER_REFRESH_METHOD=" + m}, validWeb...)
		if err := Validate(mustLoad(t, Options{Environ: env}), RoleWeb, ValidateOptions{}); err != nil {
			t.Errorf("%s: %v", m, err)
		}
	}
}

func TestInsecureDevAllowsAuthNone(t *testing.T) {
	env := append([]string{"FUKKU_AUTH_MODE=none"}, validWeb[1:]...)
	if err := Validate(mustLoad(t, Options{Environ: env}), RoleWeb, ValidateOptions{InsecureDev: true}); err != nil {
		t.Fatal(err)
	}
}

func TestValidateReportsEveryProblem(t *testing.T) {
	cfg := mustLoad(t, Options{Environ: []string{"FUKKU_WORKER_NICE=40", "FUKKU_MERGE_PREVIEW_CLIPS=0"}})
	err := Validate(cfg, RoleWorker, ValidateOptions{})
	var joined interface{ Unwrap() []error }
	if !errors.As(err, &joined) || len(joined.Unwrap()) != 2 {
		t.Fatalf("want 2 problems, got %v", err)
	}
}

func TestByteSizeAndDurationRoundTrip(t *testing.T) {
	for _, s := range []string{"16MiB", "80GiB", "1KiB", "12345"} {
		b, err := ParseByteSize(s)
		if err != nil || b.String() != s {
			t.Errorf("%s → %v %v", s, b, err)
		}
	}
	for _, s := range []string{"14d", "36h", "1h30m", "500ms", "1m", "2m30s", "45s", "30s", "10s"} {
		d, err := ParseDuration(s)
		if err != nil || d.String() != s {
			t.Errorf("%s → %v %v", s, d, err)
		}
	}
}

func TestParsePrefix(t *testing.T) {
	want := map[string]string{
		"192.0.2.10":           "192.0.2.10/32",
		" 192.0.2.10 ":         "192.0.2.10/32",
		"192.0.2.77/24":        "192.0.2.0/24",
		"2001:db8::1":          "2001:db8::1/128",
		"2001:db8::1/64":       "2001:db8::/64",
		"::ffff:192.0.2.10":    "192.0.2.10/32",
		"::ffff:192.0.2.0/120": "192.0.2.0/24",
	}
	for in, out := range want {
		p, err := ParsePrefix(in)
		if err != nil || p.String() != out {
			t.Errorf("%q → %v %v, want %s", in, p, err, out)
		}
	}
	for _, bad := range []string{"", "proxy", "192.0.2.0/33", "192.0.2.300", "fe80::1%eth0", "192.0.2.0/24/1"} {
		if _, err := ParsePrefix(bad); err == nil {
			t.Errorf("%q was accepted", bad)
		}
	}
}

// The merge (#54) hands merge.languageTag and merge.trackName to mkvmerge: a tag that is
// not BCP 47, or a name with control characters, is refused at start-up.
func TestMergeTagAndName(t *testing.T) {
	for env, want := range map[string]string{
		"FUKKU_MERGE_LANGUAGE_TAG=":                          "merge.languageTag",
		"FUKKU_MERGE_LANGUAGE_TAG=Spanish Latin":             "merge.languageTag",
		"FUKKU_MERGE_LANGUAGE_TAG=en_US":                     `use "en-US"`, // mkvmerge refuses it
		"FUKKU_MERGE_LANGUAGE_TAG=iw":                        `use "he"`,    // mkvmerge rewrites these
		"FUKKU_MERGE_LANGUAGE_TAG=zh-yue":                    `use "yue"`,
		"FUKKU_MERGE_LANGUAGE_TAG=sgn-BE-FR":                 `use "sfb"`,
		"FUKKU_MERGE_LANGUAGE_TAG=ES-419":                    `use "es-419"`,
		"FUKKU_MERGE_LANGUAGE_TAG=sgn-BR":                    "bzs, not sgn-BR", // mkvmerge stores bzs
		"FUKKU_MERGE_COVERAGE_TOLERANCE=0s":                  "merge.coverageTolerance",
		"FUKKU_MERGE_COVERAGE_TOLERANCE=2m":                  "merge.coverageTolerance",
		"FUKKU_MERGE_TRACK_NAME=a\tb":                        "merge.trackName",
		"FUKKU_MERGE_TRACK_NAME=" + strings.Repeat("n", 257): "merge.trackName",
	} {
		err := Validate(mustLoad(t, Options{Environ: []string{env}}), RoleWorker, ValidateOptions{})
		if err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("%.40s: %v, want %s", env, err, want)
		}
	}
	for _, env := range []string{"FUKKU_MERGE_LANGUAGE_TAG=pt-BR", "FUKKU_MERGE_LANGUAGE_TAG=es", "FUKKU_MERGE_LANGUAGE_TAG=sr-Latn-RS", "FUKKU_MERGE_LANGUAGE_TAG=yue", "FUKKU_MERGE_LANGUAGE_TAG=bzs", "FUKKU_MERGE_TRACK_NAME=", "FUKKU_MERGE_TRACK_NAME=Português (Brasil)"} {
		if err := Validate(mustLoad(t, Options{Environ: []string{env}}), RoleWorker, ValidateOptions{}); err != nil {
			t.Errorf("%s: %v", env, err)
		}
	}
}
