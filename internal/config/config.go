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
	ListenAddress     string   `yaml:"listenAddress"`
	PublicBaseURL     string   `yaml:"publicBaseUrl"`
	UILanguages       []string `yaml:"uiLanguages"`
	LogFormat         string   `yaml:"logFormat"`
	Metrics           bool     `yaml:"metrics"`
	DataDir           string   `yaml:"dataDir"`
	JobStuckAfter     Duration `yaml:"jobStuckAfter"`
	ReadinessCacheTTL Duration `yaml:"readinessCacheTtl"`
	// ArrStallBackoff: how long a job stalled because an app did not answer waits before it
	// tries again, doubled on each further stall (at most 16 times).
	ArrStallBackoff Duration `yaml:"arrStallBackoff"`
	// ArrStallLimit: after this many stalls in a row in one state, the job fails instead.
	ArrStallLimit int `yaml:"arrStallLimit"`
	// ImportPollInterval: how often a running import is looked at (its command polled).
	ImportPollInterval Duration `yaml:"importPollInterval"`
	// PostStepAttempts: how often a post-processing step is run before it is recorded as
	// failed. A step's own retries inside one run count as one attempt.
	PostStepAttempts int `yaml:"postStepAttempts"`
	// PostStepBackoff: the wait after a step's first failed attempt, doubled for each
	// further one (at most 16 times).
	PostStepBackoff Duration `yaml:"postStepBackoff"`
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
	URL                string `yaml:"url"`
	PublicURL          string `yaml:"publicUrl"`
	APIKey             Secret `yaml:"apiKey"`
	QualityProfile     string `yaml:"qualityProfile"`
	RootFolder         string `yaml:"rootFolder"`
	Tag                string `yaml:"tag"`
	MonitorAfterImport bool   `yaml:"monitorAfterImport"`
	ImportMode         string `yaml:"importMode"`
	// MinimumAvailability is when a film Fukku adds counts as available (Radarr only; Sonarr ignores it).
	MinimumAvailability string   `yaml:"minimumAvailability"`
	RequestTimeout      Duration `yaml:"requestTimeout"`
}

type Subtitles struct {
	Provider        string   `yaml:"provider"`
	URL             string   `yaml:"url"`
	APIKey          Secret   `yaml:"apiKey"`
	HandOverUploads bool     `yaml:"handOverUploads"`
	RequestTimeout  Duration `yaml:"requestTimeout"`
	// Hand-over of uploaded subtitle files (plan §6, Subtitles).
	Attempts       int      `yaml:"attempts"`       // uploads of one file before giving up
	ConfirmTimeout Duration `yaml:"confirmTimeout"` // how long to wait for the history to show one upload
	PollInterval   Duration `yaml:"pollInterval"`   // how often to look at the history meanwhile
	RetryBackoff   Duration `yaml:"retryBackoff"`   // wait before the second upload; doubles each time
	MaxFileSize    ByteSize `yaml:"maxFileSize"`    // largest subtitle file read for the hand-over
}

type MediaServer struct {
	Provider          string   `yaml:"provider"`
	URL               string   `yaml:"url"`
	APIKey            Secret   `yaml:"apiKey"`
	RefreshMethod     string   `yaml:"refreshMethod"`
	RefreshFallback   bool     `yaml:"refreshFallback"`
	TitleLinkTemplate string   `yaml:"titleLinkTemplate"`
	RequestTimeout    Duration `yaml:"requestTimeout"`
	ItemWait          Duration `yaml:"itemWait"`
	ItemPollInterval  Duration `yaml:"itemPollInterval"`
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
	RequestIdleTimeout     Duration `yaml:"requestIdleTimeout"`
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
	LookupCacheTTL         Duration `yaml:"lookupCacheTtl"`
	// RuntimeMismatchTolerance: a file further than this (relative) from its episodes'
	// expected runtime is hinted as a runtime mismatch.
	RuntimeMismatchTolerance float64 `yaml:"runtimeMismatchTolerance"`
	// DVDOrderShare: when more than this share of the rows that can be compared are
	// runtime mismatches, the proposal hints at DVD order.
	DVDOrderShare float64 `yaml:"dvdOrderShare"`
	// LanguageWarning: warn on a proposal row whose custom-format score is below its quality
	// profile's minimum, and ask for an override to import it (decision #21).
	LanguageWarning bool `yaml:"languageWarning"`
}

type Merge struct {
	Enabled                     bool     `yaml:"enabled"`
	PreviewClips                int      `yaml:"previewClips"`
	PreviewClipLength           Duration `yaml:"previewClipLength"`
	PreviewHeight               int      `yaml:"previewHeight"`
	PreviewVideoBitrate         int      `yaml:"previewVideoBitrate"` // kbit/s
	PreviewTimeout              Duration `yaml:"previewTimeout"`
	LanguageTag                 string   `yaml:"languageTag"`
	TrackName                   string   `yaml:"trackName"`
	KeepOriginalDefault         bool     `yaml:"keepOriginalDefault"`
	CoverageTolerance           Duration `yaml:"coverageTolerance"`
	UnmonitorAfterMerge         bool     `yaml:"unmonitorAfterMerge"`
	UnmonitorAfterDubOnlyImport bool     `yaml:"unmonitorAfterDubOnlyImport"`
}

// Versions configures second versions (flow 2b, decision #20): a film's version is kept in
// Root, outside Radarr's root folders, and grouped with the film in the media server.
type Versions struct {
	Enabled bool `yaml:"enabled"`
	// Root is where films' second versions are placed, as Fukku sees it: outside every Radarr
	// root folder, inside a media server library, on the intake root's filesystem.
	Root                     string   `yaml:"root"`
	LabelTemplate            string   `yaml:"labelTemplate"`
	ForbiddenLabelTokens     []string `yaml:"forbiddenLabelTokens"`
	RequireNotUpgradeVerdict bool     `yaml:"requireNotUpgradeVerdict"`
	// RegroupInterval: how often every version's grouping is checked and re-applied (a
	// rename or upgrade of the film's file loses it, probe B3e); 0: never on a schedule.
	RegroupInterval Duration `yaml:"regroupInterval"`
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
	LibraryFormats    []string `yaml:"libraryFormats"`
	TaskTimeout       Duration `yaml:"taskTimeout"`
	Nice              int      `yaml:"nice"`
	HeartbeatInterval Duration `yaml:"heartbeatInterval"`
	HeartbeatMaxAge   Duration `yaml:"heartbeatMaxAge"`
	// What a probe keeps of one file; the rest is cut, so a result stays small.
	MaxTrackTitleBytes int `yaml:"maxTrackTitleBytes"`
	MaxTrackTagBytes   int `yaml:"maxTrackTagBytes"`
	MaxStreamsPerFile  int `yaml:"maxStreamsPerFile"`
}
