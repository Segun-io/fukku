package config

import "time"

// Defaults returns the default value of every setting. docs/settings.md documents them;
// a test keeps the two in step.
func Defaults() Config {
	arr := func(monitor bool) Arr {
		return Arr{
			Tag:                "fukku",
			MonitorAfterImport: monitor,
			ImportMode:         "move",
			RequestTimeout:     Duration(30 * time.Second),
		}
	}
	return Config{
		Mode: "split",
		Server: Server{
			ListenAddress: ":8080",
			UILanguages:   []string{"en", "es"},
			LogFormat:     "logfmt",
			DataDir:       "/var/lib/fukku",
			JobStuckAfter: Duration(time.Hour),
		},
		Auth: Auth{
			Mode:            "proxyHeaders",
			UserHeader:      "Remote-User",
			EmailHeader:     "Remote-Email",
			GroupsHeader:    "Remote-Groups",
			GroupsSeparator: ",",
			OriginCheck:     true,
		},
		Radarr: arr(true),
		// New shows are added with nothing monitored (plan §8, flow 3).
		Sonarr: arr(false),
		Subtitles: Subtitles{
			Provider:        "none",
			HandOverUploads: true,
			RequestTimeout:  Duration(30 * time.Second),
		},
		MediaServer: MediaServer{
			Provider:       "none",
			RefreshMethod:  "pathUpdate",
			RequestTimeout: Duration(30 * time.Second),
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
			FreeSpaceMargin:        100 << 30,
			VideoExtensions:        []string{".mkv", ".mp4", ".m4v", ".avi", ".mov", ".ts", ".webm"},
			SubtitleExtensions:     []string{".srt", ".ass", ".ssa"},
			ArchiveExtensions:      []string{".zip"},
			IdleUploadExpiry:       Duration(48 * time.Hour),
			JobRetention:           Duration(14 * 24 * time.Hour),
			LargeFileHintThreshold: 20 << 30,
		},
		Extract: Extract{
			MaxEntries:      500,
			MaxWrittenBytes: 200 << 30,
			MaxRatio:        20,
			MaxNameBytes:    200,
			AllowedTypes:    []string{"zip"},
		},
		Match: Match{
			RuntimeTolerance:       Duration(500 * time.Millisecond),
			PALRatio:               1.0427,
			PALTolerance:           0.002,
			DefaultSource:          "WEBDL",
			DoubleEpisodeRatio:     2.0,
			DoubleEpisodeTolerance: 0.15,
		},
		Merge: Merge{
			Enabled:                     true,
			PreviewClips:                3,
			PreviewClipLength:           Duration(20 * time.Second),
			LanguageTag:                 "es-419",
			TrackName:                   "Español (Latino)",
			KeepOriginalDefault:         true,
			UnmonitorAfterMerge:         true,
			UnmonitorAfterDubOnlyImport: true,
		},
		Versions: Versions{
			Enabled:       false,
			Strategy:      "naming",
			LabelTemplate: "{languageName}",
			ForbiddenLabelTokens: []string{
				"2160p", "1080p", "720p", "576p", "480p", "4k", "uhd", "hd", "sd",
				"bluray", "blu-ray", "bdrip", "brrip", "remux", "web", "webdl", "web-dl", "webrip",
				"hdtv", "dvd", "dvdrip", "hdr", "dv", "proper", "repack",
			},
			RequireNotUpgradeVerdict: true,
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
			TaskTimeout:       Duration(6 * time.Hour),
			Nice:              10,
			HeartbeatInterval: Duration(15 * time.Second),
			HeartbeatMaxAge:   Duration(2 * time.Minute),
		},
	}
}
