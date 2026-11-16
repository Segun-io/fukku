// Types of the JSON API. They are copied from the contracts of the issues that own them:
// the session and the error shape (#5), the media probe (#4), the upload refusals (#10) and
// the jobs (#11). When a contract changes there, change it here in the same pull request.

/** The health endpoint (`GET /healthz`), outside `/api/`. */
export type Health = { status: string; version: string };

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

export type JobDetail = Job & {
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
