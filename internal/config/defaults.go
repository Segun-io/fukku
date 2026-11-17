package config

import "time"

// Defaults returns the default value of every setting. docs/settings.md documents them;
// a test keeps the two in step.
func Defaults() Config {
	arr := func(monitor bool) Arr {
		return Arr{
			Tag:                 "fukku",
			MonitorAfterImport:  monitor,
			ImportMode:          "move",
			MinimumAvailability: "released",
			RequestTimeout:      Duration(30 * time.Second),
		}
	}
	return Config{
		Mode: "split",
		Server: Server{
			ListenAddress:     ":8080",
			UILanguages:       []string{"en", "es"},
			LogFormat:         "logfmt",
			DataDir:           "/var/lib/fukku",
			JobStuckAfter:     Duration(time.Hour),
			ReadinessCacheTTL: Duration(15 * time.Second),
			ArrStallBackoff:   Duration(30 * time.Second),
			ArrStallLimit:     5,
			// Polling a command is cheap; the import of a large file by rename is quick.
			ImportPollInterval: Duration(2 * time.Second),
			PostStepAttempts:   5,
			PostStepBackoff:    Duration(30 * time.Second),
		},
		Auth: Auth{
			Mode:        "proxyHeaders",
			UserHeader:  "Remote-User",
			EmailHeader: "Remote-Email",
			// No groups header by default (#133): a proxy that sets only the user header
			// would otherwise pass a client's own groups through. Set it, together with
			// auth.adminGroup, when the proxy sets and strips it.
			GroupsHeader:    "",
			GroupsSeparator: ",",
			OriginCheck:     true,
			// Safe by default (#116): a household turns it off when everyone may change the library.
			LibraryChangesRequireAdmin: true,
		},
		Radarr: arr(true),
		// New shows are added with nothing monitored (plan §8, flow 3).
		Sonarr: arr(false),
		Subtitles: Subtitles{
			Provider:        "none",
			HandOverUploads: true,
			RequestTimeout:  Duration(30 * time.Second),
			Attempts:        4,
			ConfirmTimeout:  Duration(20 * time.Second),
			PollInterval:    Duration(time.Second),
			RetryBackoff:    Duration(5 * time.Second),
			MaxFileSize:     ByteSize(2 << 20),
		},
		MediaServer: MediaServer{
			Provider:        "none",
			RefreshMethod:   "libraryRefresh",
			RefreshFallback: true,
			RequestTimeout:  Duration(30 * time.Second),
			// pathUpdate shows an item only after the server's LibraryMonitorDelay (60 s by
			// default), so the wait must outlast it.
			ItemWait:         Duration(2 * time.Minute),
			ItemPollInterval: Duration(2 * time.Second),
		},
		Paths: Paths{
			IntakeRoot:            "/data/intake",
			RequireSameFilesystem: "auto",
		},
		Upload: Upload{
			ChunkSize:              16 << 20,
			MaxFileSize:            80 << 30,
			MaxInFlight:            300 << 30,
			MaxParallelPerUser:     3,
			MaxOpenJobsPerUser:     20,
			FreeSpaceMargin:        100 << 30,
			VideoExtensions:        []string{".mkv", ".mp4", ".m4v", ".avi", ".mov", ".ts", ".webm"},
			SubtitleExtensions:     []string{".srt", ".ass", ".ssa"},
			ArchiveExtensions:      []string{".zip"},
			IdleUploadExpiry:       Duration(48 * time.Hour),
			RequestIdleTimeout:     Duration(30 * time.Second),
			JobRetention:           Duration(14 * 24 * time.Hour),
			FinishedJobRetention:   Duration(30 * 24 * time.Hour),
			SweepInterval:          Duration(15 * time.Minute),
			LargeFileHintThreshold: 20 << 30,
		},
		Extract: Extract{
			MaxEntries:      500,
			MaxWrittenBytes: 200 << 30,
			MaxRatio:        20,
			RatioFloor:      1 << 20,
			MaxNameBytes:    200,
			MaxPathSegments: 32,
			MaxPathBytes:    1024,
			AllowedTypes:    []string{"zip"},
		},
		Match: Match{
			RuntimeTolerance:         Duration(500 * time.Millisecond),
			PALRatio:                 1.0427,
			PALTolerance:             0.002,
			DefaultSource:            "WEBDL",
			DoubleEpisodeRatio:       2.0,
			DoubleEpisodeTolerance:   0.15,
			LookupCacheTTL:           Duration(5 * time.Minute),
			RuntimeMismatchTolerance: 0.25,
			DVDOrderShare:            0.5,
			LanguageWarning:          true,
		},
		Merge: Merge{
			Enabled:                     true,
			PreviewClips:                3,
			PreviewClipLength:           Duration(20 * time.Second),
			PreviewHeight:               360,
			PreviewVideoBitrate:         800,
			PreviewTimeout:              Duration(5 * time.Minute),
			LanguageTag:                 "es-419",
			TrackName:                   "Español (Latino)",
			KeepOriginalDefault:         true,
			CoverageTolerance:           Duration(3 * time.Second),
			UnmonitorAfterMerge:         true,
			UnmonitorAfterDubOnlyImport: true,
		},
		Versions: Versions{
			Enabled:       false,
			LabelTemplate: "{languageName}",
			ForbiddenLabelTokens: []string{
				"2160p", "1080p", "720p", "576p", "480p", "4k", "uhd", "hd", "sd",
				"bluray", "blu-ray", "bdrip", "brrip", "remux", "web", "webdl", "web-dl", "webrip",
				"hdtv", "dvd", "dvdrip", "hdr", "dv", "proper", "repack",
			},
			RequireNotUpgradeVerdict: true,
			RegroupInterval:          Duration(10 * time.Minute),
		},
		SeasonDub: SeasonDub{
			Policy: "ask",
			Order:  []string{"merge", "version", "replace", "subtitlesOnly"},
		},
		Worker: Worker{
			Concurrency:       1,
			SpoolDir:          "/spool",
			PollInterval:      Duration(time.Second),
			ProbeTimeout:      Duration(60 * time.Second),
			LibraryFormats:    []string{"matroska", "webm", "mov", "mp4", "avi", "mpegts", "mpeg", "asf", "flv", "ogg"},
			TaskTimeout:       Duration(6 * time.Hour),
			Nice:              10,
			ToolMemoryLimit:   6 << 30,
			HeartbeatInterval: Duration(15 * time.Second),
			HeartbeatMaxAge:   Duration(2 * time.Minute),

			MaxTrackTitleBytes: 256,
			MaxTrackTagBytes:   64,
			MaxStreamsPerFile:  256,

			MaxProbeResultBytes:   8 << 20,
			RequireSeparateMounts: true,
			SweepMaxEntries:       100_000,
			SweepMaxDuration:      Duration(30 * time.Second),
		},
	}
}
