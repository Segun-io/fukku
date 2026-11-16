// Package config defines Fukku's settings catalogue (docs/settings.md), its defaults,
// loading from a YAML file plus FUKKU_* environment overrides, and validation.
package config

// Config is the whole settings catalogue. YAML keys are the camelCase field tags; each leaf
// also has an environment override named FUKKU_<GROUP>_<KEY> in upper snake case, for
// example server.listenAddress → FUKKU_SERVER_LISTEN_ADDRESS. Secret fields additionally
// accept <NAME>_FILE, the path of a file holding the value.
type Config struct {
	// Mode is "split" (web and worker run as separate processes, the reference layout) or
	// "combined" (both in one process; simpler, but the parser shares the web process's
	// network and secrets).
	Mode string `yaml:"mode"`

	Server      Server      `yaml:"server"`
	Auth        Auth        `yaml:"auth"`
	Radarr      Arr         `yaml:"radarr"`
	Sonarr      Arr         `yaml:"sonarr"`
	Subtitles   Subtitles   `yaml:"subtitles"`
	MediaServer MediaServer `yaml:"mediaServer"`
	Paths       Paths       `yaml:"paths"`
	Upload      Upload      `yaml:"upload"`
	Extract     Extract     `yaml:"extract"`
	Match       Match       `yaml:"match"`
	Merge       Merge       `yaml:"merge"`
	Versions    Versions    `yaml:"versions"`
	SeasonDub   SeasonDub   `yaml:"seasonDub"`
	Worker      Worker      `yaml:"worker"`
}

type Server struct {
	ListenAddress string   `yaml:"listenAddress"`
	PublicBaseURL string   `yaml:"publicBaseUrl"`
	UILanguages   []string `yaml:"uiLanguages"`
	LogFormat     string   `yaml:"logFormat"`
	Metrics       bool     `yaml:"metrics"`
	DataDir       string   `yaml:"dataDir"`
	JobStuckAfter Duration `yaml:"jobStuckAfter"`
}

type Auth struct {
	Mode              string   `yaml:"mode"`
	TrustedProxies    []string `yaml:"trustedProxies"`
	UserHeader        string   `yaml:"userHeader"`
	EmailHeader       string   `yaml:"emailHeader"`
	GroupsHeader      string   `yaml:"groupsHeader"`
	GroupsSeparator   string   `yaml:"groupsSeparator"`
	AdminGroup        string   `yaml:"adminGroup"`
	AllUsersAreAdmins bool     `yaml:"allUsersAreAdmins"`
	OriginCheck       bool     `yaml:"originCheck"`
}

// Arr configures one Radarr or Sonarr instance. It is enabled when URL is set.
type Arr struct {
	URL                string   `yaml:"url"`
	PublicURL          string   `yaml:"publicUrl"`
	APIKey             Secret   `yaml:"apiKey"`
	QualityProfile     string   `yaml:"qualityProfile"`
	RootFolder         string   `yaml:"rootFolder"`
	Tag                string   `yaml:"tag"`
	MonitorAfterImport bool     `yaml:"monitorAfterImport"`
	ImportMode         string   `yaml:"importMode"`
	RequestTimeout     Duration `yaml:"requestTimeout"`
}

type Subtitles struct {
	Provider        string   `yaml:"provider"`
	URL             string   `yaml:"url"`
	APIKey          Secret   `yaml:"apiKey"`
	HandOverUploads bool     `yaml:"handOverUploads"`
	RequestTimeout  Duration `yaml:"requestTimeout"`
}

type MediaServer struct {
	Provider          string   `yaml:"provider"`
	URL               string   `yaml:"url"`
	APIKey            Secret   `yaml:"apiKey"`
	RefreshMethod     string   `yaml:"refreshMethod"`
	TitleLinkTemplate string   `yaml:"titleLinkTemplate"`
	RequestTimeout    Duration `yaml:"requestTimeout"`
}

// PathMapping says that a path Fukku sees under From is seen by another app under To.
type PathMapping struct {
	From string `yaml:"from" json:"from"`
	To   string `yaml:"to" json:"to"`
}

type PathMappings struct {
	Radarr      []PathMapping `yaml:"radarr"`
	Sonarr      []PathMapping `yaml:"sonarr"`
	Subtitles   []PathMapping `yaml:"subtitles"`
	MediaServer []PathMapping `yaml:"mediaServer"`
}

type Paths struct {
	IntakeRoot            string       `yaml:"intakeRoot"`
	LibraryRoots          []string     `yaml:"libraryRoots"`
	Mappings              PathMappings `yaml:"mappings"`
	Sentinels             []string     `yaml:"sentinels"`
	RequireSameFilesystem string       `yaml:"requireSameFilesystem"`
}

type Upload struct {
	ChunkSize              ByteSize `yaml:"chunkSize"`
	MaxFileSize            ByteSize `yaml:"maxFileSize"`
	MaxInFlight            ByteSize `yaml:"maxInFlight"`
	MaxParallelPerUser     int      `yaml:"maxParallelPerUser"`
	FreeSpaceMargin        ByteSize `yaml:"freeSpaceMargin"`
	VideoExtensions        []string `yaml:"videoExtensions"`
	SubtitleExtensions     []string `yaml:"subtitleExtensions"`
	ArchiveExtensions      []string `yaml:"archiveExtensions"`
	IdleUploadExpiry       Duration `yaml:"idleUploadExpiry"`
	JobRetention           Duration `yaml:"jobRetention"`
	FinishedJobRetention   Duration `yaml:"finishedJobRetention"`
	SweepInterval          Duration `yaml:"sweepInterval"`
	LargeFileHintThreshold ByteSize `yaml:"largeFileHintThreshold"`
}

type Extract struct {
	MaxEntries      int      `yaml:"maxEntries"`
	MaxWrittenBytes ByteSize `yaml:"maxWrittenBytes"`
	MaxRatio        float64  `yaml:"maxRatio"`
	RatioFloor      ByteSize `yaml:"ratioFloor"`
	MaxNameBytes    int      `yaml:"maxNameBytes"`
	MaxPathSegments int      `yaml:"maxPathSegments"`
	MaxPathBytes    int      `yaml:"maxPathBytes"`
	AllowedTypes    []string `yaml:"allowedTypes"`
}

type Match struct {
	RuntimeTolerance       Duration `yaml:"runtimeTolerance"`
	PALRatio               float64  `yaml:"palRatio"`
	PALTolerance           float64  `yaml:"palTolerance"`
	DefaultSource          string   `yaml:"defaultSource"`
	DoubleEpisodeRatio     float64  `yaml:"doubleEpisodeRatio"`
	DoubleEpisodeTolerance float64  `yaml:"doubleEpisodeTolerance"`
}

type Merge struct {
	Enabled                     bool     `yaml:"enabled"`
	PreviewClips                int      `yaml:"previewClips"`
	PreviewClipLength           Duration `yaml:"previewClipLength"`
	LanguageTag                 string   `yaml:"languageTag"`
	TrackName                   string   `yaml:"trackName"`
	KeepOriginalDefault         bool     `yaml:"keepOriginalDefault"`
	UnmonitorAfterMerge         bool     `yaml:"unmonitorAfterMerge"`
	UnmonitorAfterDubOnlyImport bool     `yaml:"unmonitorAfterDubOnlyImport"`
}

type Versions struct {
	Enabled                  bool     `yaml:"enabled"`
	Strategy                 string   `yaml:"strategy"`
	LabelTemplate            string   `yaml:"labelTemplate"`
	ForbiddenLabelTokens     []string `yaml:"forbiddenLabelTokens"`
	RequireNotUpgradeVerdict bool     `yaml:"requireNotUpgradeVerdict"`
}

type SeasonDub struct {
	Policy string   `yaml:"policy"`
	Order  []string `yaml:"order"`
}

type Worker struct {
	Concurrency       int      `yaml:"concurrency"`
	SpoolDir          string   `yaml:"spoolDir"`
	PollInterval      Duration `yaml:"pollInterval"`
	ProbeTimeout      Duration `yaml:"probeTimeout"`
	TaskTimeout       Duration `yaml:"taskTimeout"`
	Nice              int      `yaml:"nice"`
	HeartbeatInterval Duration `yaml:"heartbeatInterval"`
	HeartbeatMaxAge   Duration `yaml:"heartbeatMaxAge"`
}
