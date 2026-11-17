// Types of the JSON API. They are copied from the contracts of the issues that own them:
// the session and the error shape (#5), the media probe (#4), the upload refusals (#10) and
// the jobs (#11). When a contract changes there, change it here in the same pull request.

/** The health endpoint (`GET /healthz`), outside `/api/`. */
export type Health = { status: string; version: string };

import type { Ref } from "./proposal";

// ---- Errors (#5) ----------------------------------------------------------------------

/** The body of every error answer: `{"error": {"code": "...", "message": "..."}}`. */
export type ApiErrorBody = { error: { code: string; message: string } };

/** Codes every endpoint may return (#5). */
export const commonErrorCodes = [
  "forbidden",
  "origin_mismatch",
  "not_found",
  "bad_request",
  "too_large",
  "internal",
] as const;

/** Codes of the job endpoints (#11). */
export const jobErrorCodes = [
  "uploads_unfinished",
  "no_files",
  "wrong_state",
  "job_active",
  "too_many_jobs",
] as const;

/** Codes of the match endpoints (#37). */
export const matchErrorCodes = [
  "already_in_library",
  "title_in_use",
  "title_busy",
  "arr_unavailable",
  "app_not_configured",
  "title_not_found",
] as const;

/** Codes of the proposal and confirm endpoints (#38, #40). */
export const confirmErrorCodes = [
  "needs_override",
  "incomplete_rows",
  "nothing_to_import",
  "flow2",
] as const;

/**
 * Codes of flow 2 (#52, #53, #55, #56): the preview, the merge and the second version, and the
 * season-dub choice of the proposal's confirmation (#57). `merge_not_offered`, `preview_required`
 * and `library_changed` are the merge's (#91; assumed from its description until it is merged).
 */
export const flow2ErrorCodes = [
  "not_flow2",
  "merge_not_offered",
  "no_audio",
  "preview_required",
  "library_changed",
  "version_label",
  "version_unavailable",
  "version_exists",
  "version_upgrade",
  "version_unchecked",
  "season_dub_choice",
  "shared_file",
] as const;

/** Refusals of `/api/uploads/` (#10). */
export const uploadErrorCodes = [
  "bad_name",
  "job_not_uploading",
  "duplicate_name",
  "file_too_large",
  "extension_not_allowed",
  "too_many_uploads",
  "in_flight_limit",
  "insufficient_space",
  "storage_unavailable",
  "offset_mismatch",
  "upload_busy",
] as const;

/** Codes made up by the client: the proxy asked for a sign-in, or the server was unreachable. */
export const clientErrorCodes = ["signed_out", "network", "bad_response"] as const;

export type UploadErrorCode = (typeof uploadErrorCodes)[number];

/** Every error code the UI has a translation for (`errors.<code>`). */
export const knownErrorCodes = [
  ...commonErrorCodes,
  ...jobErrorCodes,
  ...matchErrorCodes,
  ...confirmErrorCodes,
  ...flow2ErrorCodes,
  ...uploadErrorCodes,
  ...clientErrorCodes,
] as const;

export type KnownErrorCode = (typeof knownErrorCodes)[number];

export function isKnownErrorCode(code: string): code is KnownErrorCode {
  return (knownErrorCodes as readonly string[]).includes(code);
}

// ---- Session (#5) ---------------------------------------------------------------------

export type Session = {
  user: { name: string; email: string; admin: boolean };
  version: string;
  authMode: string;
  insecureDev: boolean;
  uiLanguages: string[];
  apps: { radarr: boolean; sonarr: boolean };
  /** True when a subtitle manager is configured and uploaded subtitles are handed to it (#42). */
  subtitleManager: boolean;
  upload: {
    endpoint: string;
    chunkSize: number;
    maxFileSize: number;
    maxParallelPerUser: number;
    videoExtensions: string[];
    subtitleExtensions: string[];
    archiveExtensions: string[];
    largeFileHintThreshold: number;
  };
};

// ---- Admin: health and settings (#69) ---------------------------------------------------

export type ReadinessCheck = { name: string; ok: boolean; detail?: string };

export type AdminStatus = {
  version: string;
  readiness: { status: "ready" | "degraded" | "not_ready"; checks: ReadinessCheck[] };
  /** The configured apps and the version each reported; empty when it did not answer. */
  apps: { name: string; version: string }[];
  worker: { heartbeatAgeSeconds: number | null; maxAgeSeconds: number };
  jobs: { total: number; byState: Record<string, number> } | null;
  disk: { freeBytes: number | null; marginBytes: number; aboveMargin: boolean };
  checkedAt: string;
};

export type AdminSetting = {
  key: string;
  env: string;
  secret: boolean;
  /** For a secret: empty (not set) or "[redacted]". The server never sends its content. */
  value: string;
  source: "default" | "set";
};

export type AdminSettings = { settings: AdminSetting[] };

// ---- Media probe (#4) -----------------------------------------------------------------

export type VideoTrack = {
  index: number;
  codec: string;
  width: number;
  height: number;
  frameRate?: string;
  default: boolean;
};

export type AudioTrack = {
  index: number;
  codec: string;
  language?: string;
  title?: string;
  channels?: number;
  default: boolean;
};

export type SubtitleTrack = {
  index: number;
  codec: string;
  language?: string;
  title?: string;
  default: boolean;
  forced: boolean;
  hearingImpaired: boolean;
};

export type Probe = {
  container: string;
  durationMs: number;
  size: number;
  bitRate?: number;
  video: VideoTrack[];
  audio: AudioTrack[];
  subtitles: SubtitleTrack[];
};

// ---- Jobs (#11) -----------------------------------------------------------------------

/** The job states of #1. */
export const jobStates = [
  "uploading",
  "uploaded",
  "probing",
  "probed",
  "matched",
  "awaiting-confirmation",
  "importing",
  "imported",
  "post-processing",
  "done",
  "failed",
  "stalled",
  "cancelled",
] as const;

export type JobState = (typeof jobStates)[number];

/** What a user may do with a job right now. */
export type JobAction = "cancel" | "retry" | "delete" | "submit";

export type JobError = { code: string; message: string };

export type Job = {
  id: string;
  owner: string;
  state: JobState;
  attempt: number;
  error: JobError | null;
  name: string | null;
  fileCount: number;
  totalSize: number;
  createdAt: string;
  updatedAt: string;
  expiresAt: string | null;
  actions: JobAction[];
};

export type JobFileKind = "video" | "subtitle" | "archive";
export type JobFileStatus = "pending" | "ok" | "refused";

/** The extract output of #3 as the API passes it on: `refused` is left out when empty and
 * `skipped` is null when nothing was skipped. */
export type ArchiveExtraction = {
  refused?: string | null;
  skipped?: { name: string; reason: string }[] | null;
};

export type JobFile = {
  id: number;
  path: string;
  name: string;
  kind: JobFileKind;
  size: number;
  status: JobFileStatus;
  /** The refusal code when `status` is `refused`. */
  reason: string | null;
  /** For an extracted file, the path of its archive. */
  from: string | null;
  probe: Probe | null;
  extraction: ArchiveExtraction | null;
};

export type JobUpload = { id: string; name: string; size: number; offset: number };

export type JobEvent = {
  at: string;
  from: JobState | null;
  to: JobState;
  actor: string;
  note: string;
};

export type ManualImport = {
  app: "radarr" | "sonarr";
  folder: string;
  url: string | null;
};

/** The title a job was matched to (#37); `null` until the job is matched. */
export type Target = { app: AppName; titleId: number; title: string; year: number };

/** How far the import is (#40). `status` is `running` while the app works. */
export type ImportInfo = {
  status: string;
  /** `result`: empty, `imported`, `failed`, `skipped`, or `version` (kept as a second version, #58). */
  rows: { id: string; name: string; result: string }[];
  /** The merge of a flow 2 job (#55); null for every other job. */
  merge?: MergeInfo | null;
};

/**
 * How far a merge is (#55). `failed`: the job failed before the merge was made; `refusal` is
 * the merge task's own code (`verify_failed`, `free_space`...) or empty.
 */
export type MergeInfo = {
  state: "pending" | "running" | "merged" | "failed";
  refusal: string;
  audioTracks: number[];
  subtitleTracks: number[];
  offsetMs: number;
  languageTag: string;
  trackName: string;
  keepOriginalDefault: boolean;
  /** Radarr's language for the tag; null when it has none. */
  arrLanguage: Ref | null;
  name: string;
  size: number;
};

/** What can still replace a merged file (B6): `search_can_replace` or `upgrade_can_replace` (#55). */
export type MergeWarning = { code: string; message: string };

export type MergeResult = { name: string; warnings: MergeWarning[] };

/** The second version a flow 2 job placed (#56) and how the media server groups it. */
export type VersionResult = {
  name: string;
  label: string;
  /** `placing`, `linked`, `grouped`, `waiting`, `film_deleted` or `missing`. */
  status: string;
  detail: string;
  groupedAt: string | null;
  primaryChangedAt: string | null;
  notices: string[];
};

/**
 * One episode kept as a second version (#58). `status` is the record's (`placing`, `linked`,
 * `grouped`, `waiting`, `missing`, and from #96 `adopted`, `adoptable`, `episode_file_missing`,
 * `series_moved`, `series_deleted`), or `refused` when the placement did not place it, with the
 * code in `refusal` (`version_upgrade`, `version_unchecked`, `version_exists`, `version_failed`).
 * `detail` is for admins.
 */
export type EpisodeVersionResult = {
  row: string;
  seasonNumber: number;
  episodeNumber: number;
  name: string;
  status: string;
  refusal: string;
  detail: string;
  groupedAt: string | null;
};

/** The episodes a job kept as second versions (#58), and how the media server groups them. */
export type EpisodeVersionsResult = {
  label: string;
  versions: EpisodeVersionResult[];
  notices: string[];
};

/** What the season-dub choice did (#57): `replace` covers `seasons`; `subtitlesOnly` imported nothing. */
export type SeasonDubResult = { policy: string; seasons: number[] };

export type PostStepStatus = "pending" | "ok" | "failed" | "skipped";
export type PostStepInfo = { step: string; status: PostStepStatus; detail: string };

/** What the finished import left (#40). `titleLink` is null without a media server link. */
export type ImportResult = {
  titleLink: string | null;
  postProcessing: PostStepInfo[];
  /** Null until the monitoring is set. False: automatic upgrades will not replace the file. */
  monitored: boolean | null;
  /** Why Fukku left the title unmonitored: `dub_only`, `chosen`, `merged` (#55), or empty. */
  unmonitorReason: "dub_only" | "chosen" | "merged" | "";
  /** The season-dub choice that was applied (#57); null otherwise. */
  seasonDub?: SeasonDubResult | null;
  /** The second version (#56); null for every other job. */
  version?: VersionResult | null;
  /** The merged file (#55), with what can still replace it; null for every other job. */
  merge?: MergeResult | null;
  /** The episodes kept as second versions (#58); null for every other job. */
  episodeVersions?: EpisodeVersionsResult | null;
};

// ---- Flow 2 (#52, #56) ------------------------------------------------------------------

export type Flow2Finding = "match" | "pal" | "different";
export type Flow2Offer = "merge" | "version";

/** One side of the comparison. The library's quality and languages are Radarr's; the upload's are empty. */
export type Flow2File = {
  fileId: number;
  name: string;
  size: number;
  quality: Ref | null;
  languages: Ref[];
  runtimeMs?: number;
  /** The video stream's duration, the one compared; null until the file is probed. */
  durationMs: number | null;
  audio: AudioTrack[];
  subtitles: SubtitleTrack[];
};

export type NotOffered = {
  offer: Flow2Offer;
  /** `merge_disabled`, `versions_disabled`, `versions_unsupported`, `runtime_differs`. */
  reason: string;
};

/** Why a version label cannot be used (`label_empty`, `label_forbidden`...); `message` is English. */
export type LabelProblem = { reason: string; token?: string; message: string };

export type VersionOffer = {
  /** The proposed label; empty when none can be proposed. */
  label: string;
  language: string;
  name: string;
  problem: LabelProblem | null;
  /** `outside_radarr`, `regrouped_after_changes`, `kept_on_delete`, `subtitles_not_handed_over`. */
  notices: string[];
};

/**
 * A merge the worker refused for a reason the same choice would meet again (#95): the job is
 * back at its confirmation. `code` is a merge refusal (`verify_failed`, `mux_failed`,
 * `track_not_found`); `message` is English; the rest is the choice that was refused.
 */
export type MergeRefusal = {
  code: string;
  message: string;
  audioTracks: number[];
  subtitleTracks: number[];
  offsetMs: number;
  languageTag: string;
  at: string;
};

/** `flow2` in the job detail: the film already has a file, the comparison and what can be done. */
export type Flow2 = {
  state: "pending" | "ready";
  library: Flow2File;
  /** Null: the job has no accepted video. */
  upload: Flow2File | null;
  finding: Flow2Finding | null;
  /** Upload minus library, in milliseconds. */
  deltaMs: number | null;
  /** The longer duration over the shorter, at least 1. */
  ratio: number | null;
  offers: Flow2Offer[];
  notOffered: NotOffered[];
  version: VersionOffer | null;
  /** Why the last merge was refused, while the job waits for another confirmation; null otherwise. */
  mergeRefusal: MergeRefusal | null;
};

export type JobDetail = Job & {
  /** Set when the matched film already has a file (#52). */
  flow2?: Flow2 | null;
  /** From `importing` on (#40). */
  import?: ImportInfo | null;
  result?: ImportResult | null;
  /** Set from `matched` on (#37). */
  target?: Target | null;
  files: JobFile[];
  /** Unfinished uploads, only while `uploading`. */
  uploads: JobUpload[];
  history: JobEvent[];
  /** Only in `probed`. */
  manualImport: ManualImport[];
};

export type JobFilter = {
  states?: JobState[];
  mine?: boolean;
  limit?: number;
  offset?: number;
};

export type JobList = { jobs: Job[] };

// ---- Match (#37) ----------------------------------------------------------------------

export type AppName = "radarr" | "sonarr";
export type SeriesType = "standard" | "anime" | "daily";

export type QualityProfile = { id: number; name: string };
export type RootFolder = { path: string };

/** `qualityProfileId` and `rootFolder` are null when the settings name nothing the app lists. */
type AppDefaults = { qualityProfileId: number | null; rootFolder: string | null };

export type AppInfo = {
  qualityProfiles: QualityProfile[];
  rootFolders: RootFolder[];
  /** True when the app did not answer: the lists are then empty. */
  unavailable?: boolean;
};

export type RadarrInfo = AppInfo & { defaults: AppDefaults & { monitored: boolean } };

export type SonarrInfo = AppInfo & {
  seriesTypes: SeriesType[];
  defaults: AppDefaults & { seriesType: SeriesType };
};

/** `GET /api/apps`: an app that is not configured is left out. */
export type Apps = { radarr?: RadarrInfo; sonarr?: SonarrInfo };

export type MovieHit = {
  tmdbId: number;
  title: string;
  year: number;
  overview: string;
  inLibrary: boolean;
  hasFile: boolean;
};

export type SeriesHit = {
  tvdbId: number;
  title: string;
  year: number;
  overview: string;
  inLibrary: boolean;
  seasons: number[];
};

export type MatchRequest =
  | {
      app: "radarr";
      tmdbId: number;
      qualityProfileId?: number;
      rootFolder?: string;
      monitored: boolean;
    }
  | {
      app: "sonarr";
      tvdbId: number;
      qualityProfileId?: number;
      rootFolder?: string;
      seriesType: SeriesType;
    };

// ---- Confirm (#40) ----------------------------------------------------------------------

/** What the user decided for an uploaded subtitle file: the language is ISO 639-1 (two letters). */
export type SubtitleChoice = {
  fileId: number;
  language: string;
  forced: boolean;
  hearingImpaired: boolean;
};

export type ConfirmRequest = {
  overrides: string[];
  subtitles?: SubtitleChoice[];
  /** The season-dub choice (#57), one of the proposal's `seasonDub.offers`. */
  seasonDub?: string;
  /** The label of the episodes kept as second versions (`seasonDub` `version`, #58); optional. */
  version?: { label?: string };
};

/** The merge of a flow 2 job (#55). Every field is optional; the server fills the gaps. */
export type MergeRequest = {
  audioTracks?: number[];
  subtitleTracks?: number[];
  offsetMs?: number;
  languageTag?: string;
  trackName?: string;
  keepOriginalDefault?: boolean;
};

/** The confirmation of a flow 2 job (#55, #56): a merge or a second version. */
export type Flow2ConfirmRequest =
  | { flow2: "merge"; merge: MergeRequest; subtitles?: SubtitleChoice[] }
  | { flow2: "version"; version: { label?: string } };

// ---- Sync check (#53) -------------------------------------------------------------------

export type PreviewState = "none" | "pending" | "ready" | "failed";

export type PreviewClip = {
  n: number;
  startMs: number;
  lengthMs: number;
  size: number;
  url: string;
};

export type Preview = {
  state: PreviewState;
  /** Null while `state` is `none`. */
  offsetMs: number | null;
  audioTrack: number | null;
  requestedAt: string | null;
  requestedBy: string | null;
  error: JobError | null;
  /** Empty unless `ready`. */
  clips: PreviewClip[];
};

export type PreviewRequest = { offsetMs?: number; audioTrack?: number };
