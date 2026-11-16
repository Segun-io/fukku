// Data for the mock API (tests and `npm run dev:mock`), shaped like the contracts of #5 and
// #11. Names are made up; nothing here comes from a real library.

import type { Job, JobDetail, JobFile, JobState, ManualImport, Session } from "@/api/types";

export const session: Session = {
  user: { name: "user1", email: "user1@example.com", admin: false },
  version: "0.1.0",
  authMode: "proxyHeaders",
  insecureDev: false,
  uiLanguages: ["en", "es"],
  apps: { radarr: true, sonarr: true },
  subtitleManager: true,
  upload: {
    endpoint: "/api/uploads/",
    chunkSize: 16777216,
    maxFileSize: 85899345920,
    maxParallelPerUser: 3,
    videoExtensions: [".mkv", ".mp4", ".m4v", ".avi", ".mov", ".ts", ".webm"],
    subtitleExtensions: [".srt", ".ass", ".ssa"],
    archiveExtensions: [".zip"],
    largeFileHintThreshold: 21474836480,
  },
};

const id = (n: number) => n.toString(16).padStart(32, "0");

export const jobs: Job[] = [
  {
    id: id(1),
    owner: "user1",
    state: "uploading",
    attempt: 1,
    error: null,
    name: "Example Film (2001).mkv",
    fileCount: 1,
    totalSize: 4294967296,
    createdAt: "2026-01-01T12:00:00Z",
    updatedAt: "2026-01-01T12:00:00Z",
    expiresAt: null,
    actions: ["cancel", "submit"],
  },
  {
    id: id(2),
    owner: "user1",
    state: "probed",
    attempt: 1,
    error: null,
    name: "Another Film (1999).mkv",
    fileCount: 2,
    totalSize: 4294969000,
    createdAt: "2026-01-01T10:00:00Z",
    updatedAt: "2026-01-01T10:20:00Z",
    expiresAt: "2026-01-15T10:20:00Z",
    actions: ["cancel"],
  },
  {
    id: id(3),
    owner: "user1",
    state: "failed",
    attempt: 2,
    error: { code: "task_failed", message: "The job failed." },
    name: "Example Show S01.zip",
    fileCount: 1,
    totalSize: 21474836480,
    createdAt: "2025-12-31T09:00:00Z",
    updatedAt: "2025-12-31T09:30:00Z",
    expiresAt: "2026-01-14T09:30:00Z",
    actions: ["cancel", "retry", "delete"],
  },
  {
    id: id(4),
    owner: "user1",
    state: "done",
    attempt: 1,
    error: null,
    name: "Old Film (1985).mp4",
    fileCount: 1,
    totalSize: 1500000000,
    createdAt: "2025-12-20T09:00:00Z",
    updatedAt: "2025-12-20T10:00:00Z",
    expiresAt: "2026-01-03T10:00:00Z",
    actions: ["delete"],
  },
  {
    id: id(5),
    owner: "user1",
    state: "probed",
    attempt: 1,
    error: null,
    name: "Example Show S01.zip",
    fileCount: 2,
    totalSize: 4000001000,
    createdAt: "2026-01-01T08:00:00Z",
    updatedAt: "2026-01-01T08:30:00Z",
    expiresAt: "2026-01-15T08:30:00Z",
    actions: ["cancel"],
  },
  {
    id: id(6),
    owner: "user2",
    state: "stalled",
    attempt: 1,
    error: { code: "free_space", message: "The job is waiting for something outside Fukku." },
    name: "Other Show S02.zip",
    fileCount: 1,
    totalSize: 9000000000,
    createdAt: "2026-01-01T07:00:00Z",
    updatedAt: "2026-01-01T07:10:00Z",
    expiresAt: null,
    actions: ["cancel"],
  },
];

const probedFiles: JobFile[] = [
  {
    id: 12,
    path: "files/Another Film (1999).mkv",
    name: "Another Film (1999).mkv",
    kind: "video",
    size: 4294967296,
    status: "ok",
    reason: null,
    from: null,
    probe: {
      container: "matroska,webm",
      durationMs: 5400000,
      size: 4294967296,
      video: [
        {
          index: 0,
          codec: "h264",
          width: 1920,
          height: 1080,
          frameRate: "24000/1001",
          default: true,
        },
      ],
      audio: [
        { index: 1, codec: "aac", language: "eng", channels: 2, default: true },
        { index: 2, codec: "aac", language: "spa", title: "Español", channels: 2, default: false },
      ],
      subtitles: [
        {
          index: 3,
          codec: "subrip",
          language: "eng",
          default: false,
          forced: false,
          hearingImpaired: false,
        },
      ],
    },
    extraction: null,
  },
  {
    id: 13,
    path: "files/Another Film (1999).es.srt",
    name: "Another Film (1999).es.srt",
    kind: "subtitle",
    size: 1704,
    status: "ok",
    reason: null,
    from: null,
    probe: null,
    extraction: null,
  },
];

const manualImport: ManualImport[] = [
  {
    app: "radarr",
    folder: `/data/intake/jobs/${id(2)}/files`,
    url: "https://radarr.example.com/wanted/missing",
  },
];

// A season zip: two episodes came out of it, one entry was skipped, one file was refused.
const seasonFiles: JobFile[] = [
  {
    id: 20,
    path: "archives/Example Show S01.zip",
    name: "Example Show S01.zip",
    kind: "archive",
    size: 3000000000,
    status: "ok",
    reason: null,
    from: null,
    probe: null,
    extraction: {
      skipped: [
        { name: "../outside.mkv", reason: "traversal" },
        { name: "Extras/readme.exe", reason: "extension" },
        { name: "Odd.mkv", reason: "something_new" },
      ],
    },
  },
  {
    id: 21,
    path: "files/Example Show S01/Example Show S01E01.mkv",
    name: "Example Show S01E01.mkv",
    kind: "video",
    size: 1000001000,
    status: "ok",
    reason: null,
    from: "archives/Example Show S01.zip",
    probe: { ...(probedFiles[0]?.probe as NonNullable<JobFile["probe"]>), durationMs: 2700000 },
    extraction: null,
  },
  {
    id: 22,
    path: "files/Example Show S01/Example Show S01E02.mkv",
    name: "Example Show S01E02.mkv",
    kind: "video",
    size: 1000001000,
    status: "refused",
    reason: "no_video_stream",
    from: "archives/Example Show S01.zip",
    probe: null,
    extraction: null,
  },
];

const failedFiles: JobFile[] = [
  {
    id: 30,
    path: "archives/Example Show S01.zip",
    name: "Example Show S01.zip",
    kind: "archive",
    size: 21474836480,
    status: "pending",
    reason: null,
    from: null,
    probe: null,
    extraction: null,
  },
];

const filesByJob: Record<string, JobFile[]> = {
  [id(2)]: probedFiles,
  [id(3)]: failedFiles,
  [id(5)]: seasonFiles,
};

/** The states a job passed on its way to `state`, as the store records them. */
function historyFor(job: Job): JobDetail["history"] {
  const path: JobState[] = [
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
  ];
  const reached: JobState[] =
    job.state === "failed" || job.state === "stalled"
      ? ["uploading", "uploaded", "probing", job.state]
      : job.state === "cancelled" || job.state === "done"
        ? ["uploading", job.state]
        : path.slice(0, Math.max(1, path.indexOf(job.state) + 1));
  const start = new Date(job.createdAt).getTime();
  return reached.map((to, i) => ({
    at: new Date(start + i * 60_000).toISOString(),
    from: i === 0 ? null : (reached[i - 1] ?? null),
    to,
    actor: i === 0 ? job.owner : "fukku",
    note: "",
  }));
}

/** The detail of a job in the list, with files and history that fit its state. */
export function jobDetail(job: Job): JobDetail {
  const probed = job.state === "probed";
  return {
    ...job,
    files: filesByJob[job.id] ?? [],
    uploads:
      job.state === "uploading" ? [{ id: id(99), name: "Extra.mkv", size: 1000, offset: 400 }] : [],
    history: historyFor(job),
    manualImport: probed
      ? manualImport.map((m) => ({ ...m, folder: m.folder.replace(id(2), job.id) }))
      : [],
  };
}
