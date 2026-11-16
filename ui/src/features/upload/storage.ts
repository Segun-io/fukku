// The unfinished job, kept in localStorage so a reload can offer to continue it. A browser
// cannot re-open files by itself, so only what identifies each file is kept, never its bytes.

import type { QueueState, StoredFile } from "./queue";

export const storageKey = "fukku.upload";

export type StoredJob = { jobId: string; files: StoredFile[] };

function isStoredFile(v: unknown): v is StoredFile {
  if (typeof v !== "object" || v === null) return false;
  const f = v as Record<string, unknown>;
  return (
    typeof f.name === "string" &&
    typeof f.size === "number" &&
    typeof f.lastModified === "number" &&
    (f.uploadUrl === undefined || typeof f.uploadUrl === "string")
  );
}

export function loadJob(): StoredJob | null {
  try {
    const raw = localStorage.getItem(storageKey);
    if (!raw) return null;
    const v: unknown = JSON.parse(raw);
    if (typeof v !== "object" || v === null) return null;
    const job = v as Record<string, unknown>;
    if (typeof job.jobId !== "string" || !Array.isArray(job.files)) return null;
    return { jobId: job.jobId, files: job.files.filter(isStoredFile) };
  } catch {
    return null;
  }
}

export function clearJob(): void {
  try {
    localStorage.removeItem(storageKey);
  } catch {
    // Storage may be blocked; the upload still works, it just cannot be resumed after a reload.
  }
}

/** Writes the job of `state`; removes it once the job is submitted or cannot continue. */
export function saveJob(state: QueueState): void {
  if (state.phase === "choosing") return;
  if (state.phase !== "uploading" || state.jobId === null || state.jobError) {
    clearJob();
    return;
  }
  const job: StoredJob = {
    jobId: state.jobId,
    files: state.entries.map((e) => ({
      name: e.name,
      size: e.size,
      lastModified: e.lastModified,
      ...(e.uploadUrl ? { uploadUrl: e.uploadUrl } : {}),
    })),
  };
  try {
    localStorage.setItem(storageKey, JSON.stringify(job));
  } catch {
    // See clearJob.
  }
}
