// The state of an upload, with no React and no browser in it: a reducer over the files of one
// job, and the rules around it (the checks that mirror the server, how an error is classified,
// the backoff, which file starts next, whether a chosen file is the one that was interrupted).
// `use-upload.ts` runs the effects (tus, timers, the session check) and feeds the results in.

import type { UploadStatus } from "@/components/patterns/file-progress";

/** What the page needs of `session.upload` (#5). */
export type Rules = {
  maxFileSize: number;
  maxParallelPerUser: number;
  videoExtensions: string[];
  subtitleExtensions: string[];
  archiveExtensions: string[];
  largeFileHintThreshold: number;
};

/** A file as the queue knows it: no bytes, only what is compared and shown. */
export type FileInfo = { key: string; name: string; size: number; lastModified: number };

/** `awaiting`: restored after a reload, the file has to be chosen again. */
export type EntryStatus = UploadStatus | "awaiting";

export type Entry = {
  key: string;
  name: string;
  size: number;
  lastModified: number;
  status: EntryStatus;
  /** Bytes the server has confirmed, as far as this page knows. */
  sent: number;
  /** Where the tus upload lives, once it was created (a path on this origin). */
  uploadUrl?: string;
  /** Bytes per second, smoothed. */
  speed?: number;
  /** Failures in a row since the last progress; drives the backoff. */
  attempt: number;
  /** When a `reconnecting` file is tried again (ms since the epoch). */
  retryAt?: number;
  /** Why a `failed` file stopped: an error code, or `file_mismatch`. */
  code?: string;
  /** Set while `speed` has no baseline yet: the next progress only sets the baseline. */
  baseline?: boolean;
  /** Bytes the server already had when this run of the upload began. */
  resumedFrom?: number;
  lastAt?: number;
  lastSent?: number;
};

/** A chosen file that was not added, and why (`errors.<code>` or `upload.reason.<code>`). */
export type Rejected = { name: string; code: string };

export type Phase = "choosing" | "uploading" | "submitting" | "submitted";

export type QueueState = {
  rules: Rules;
  phase: Phase;
  jobId: string | null;
  entries: Entry[];
  rejected: Rejected[];
  online: boolean;
  /** A request got 401 or was redirected to a sign-in: everything waits. */
  signedOut: boolean;
  /** The job itself refuses uploads: its code. */
  jobError: string | null;
  /** The last submit failed: its code. */
  submitError: string | null;
};

export function initialState(rules: Rules, online = true): QueueState {
  return {
    rules,
    phase: "choosing",
    jobId: null,
    entries: [],
    rejected: [],
    online,
    signedOut: false,
    jobError: null,
    submitError: null,
  };
}

// ---- Checks, as the server makes them (#10) ---------------------------------------------

export function acceptedExtensions(rules: Rules): string[] {
  return [...rules.videoExtensions, ...rules.subtitleExtensions, ...rules.archiveExtensions].map(
    (e) => e.toLowerCase(),
  );
}

function extensionOf(name: string): string {
  const dot = name.lastIndexOf(".");
  return dot <= 0 ? "" : name.slice(dot).toLowerCase();
}

/** The refusal code the server would give this file, or null. `taken` are names already in the job. */
export function checkFile(
  file: { name: string; size: number },
  rules: Rules,
  taken: readonly string[],
): string | null {
  const name = file.name.trim();
  if (name === "" || /[/\\]/.test(name) || name.startsWith(".")) return "bad_name";
  if (!acceptedExtensions(rules).includes(extensionOf(name))) return "extension_not_allowed";
  if (rules.maxFileSize > 0 && file.size > rules.maxFileSize) return "file_too_large";
  const lower = name.toLowerCase();
  if (taken.some((n) => n.toLowerCase() === lower)) return "duplicate_name";
  return null;
}

/** Whether a file the user chose again is the one that was being uploaded. */
export function sameFile(a: FileInfo | Entry, b: FileInfo | Entry): boolean {
  return a.name === b.name && a.size === b.size && a.lastModified === b.lastModified;
}

export function showLargeHint(state: Pick<QueueState, "rules" | "entries">): boolean {
  const limit = state.rules.largeFileHintThreshold;
  return limit > 0 && state.entries.some((e) => e.size > limit);
}

// ---- Errors and backoff -----------------------------------------------------------------

/**
 * What an error means for the queue:
 *
 *   network    no answer, or an answer that is not the API's: ask the session, then retry
 *   auth       401: the session ended
 *   busy       the server asks to try again in a moment (offset_mismatch, upload_busy, ...)
 *   gone       404: the upload (or the job) is not there
 *   server     a 5xx: retry a few times
 *   refused    a 4xx that will not change by itself: the user decides
 */
export type ErrorKind = "network" | "auth" | "busy" | "gone" | "server" | "refused";

const busyCodes = ["offset_mismatch", "upload_busy", "too_many_uploads"];
// 507 answers, but waiting does not make room: the user decides.
const stillRefused = ["in_flight_limit", "insufficient_space"];

export function classify(status: number, code: string | undefined): ErrorKind {
  if (status === 401 || code === "signed_out") return "auth";
  if (code && busyCodes.includes(code)) return "busy";
  if (status === 404 || code === "not_found") return "gone";
  if (code && stillRefused.includes(code)) return "refused";
  if (status >= 500 || code === "storage_unavailable") return "server";
  if (status >= 400) return "refused";
  return "network";
}

/** Failures of a 5xx kind in a row before the file is given up. */
export const maxServerAttempts = 5;
/** Failures of the "try again in a moment" kind in a row before the file is given up. */
export const maxBusyAttempts = 30;

/** Milliseconds to wait after the `attempt`th failure in a row: 1 s, 2 s, 4 s ... 30 s. */
export function backoffMs(attempt: number, kind: ErrorKind = "network"): number {
  if (kind === "busy") return Math.min(500 * Math.max(1, attempt), 5000);
  return Math.min(1000 * 2 ** Math.max(0, attempt - 1), 30_000);
}

// ---- Actions ----------------------------------------------------------------------------

export type StoredFile = { name: string; size: number; lastModified: number; uploadUrl?: string };

export type Action =
  | { type: "add"; files: FileInfo[] }
  | { type: "dismissRejected" }
  | { type: "jobCreated"; jobId: string }
  /** After a reload: the stored job. `onServer` are the names the job already has. */
  | { type: "restore"; jobId: string; files: StoredFile[]; onServer: string[] }
  | { type: "reset" }
  | { type: "start"; key: string; now: number }
  | { type: "uploadUrl"; key: string; url: string }
  | { type: "progress"; key: string; sent: number; now: number }
  | { type: "done"; key: string }
  | { type: "error"; key: string; kind: ErrorKind; code: string; now: number }
  | { type: "pause"; key: string }
  | { type: "resume"; key: string }
  | { type: "retry"; key: string }
  | { type: "retryDue"; key: string; now: number }
  | { type: "remove"; key: string }
  | { type: "online"; online: boolean }
  | { type: "signedOut" }
  | { type: "signedIn" }
  | { type: "submitting" }
  | { type: "submitted" }
  | { type: "submitFailed"; code: string };

const live: EntryStatus[] = ["uploading", "reconnecting", "queued"];

function patch(state: QueueState, key: string, f: (e: Entry) => Entry): QueueState {
  return { ...state, entries: state.entries.map((e) => (e.key === key ? f(e) : e)) };
}

function newEntry(f: FileInfo): Entry {
  return {
    key: f.key,
    name: f.name,
    size: f.size,
    lastModified: f.lastModified,
    status: "queued",
    sent: 0,
    attempt: 0,
  };
}

function add(state: QueueState, files: FileInfo[]): QueueState {
  let entries = state.entries;
  const rejected: Rejected[] = [];
  for (const file of files) {
    const same = (e: Entry) => e.name.toLowerCase() === file.name.toLowerCase();
    const waiting = entries.find((e) => e.status === "awaiting" && same(e));
    if (waiting) {
      // The file of an interrupted upload, chosen again: it must be the same file.
      if (!sameFile(waiting, file)) {
        rejected.push({ name: file.name, code: "file_mismatch" });
        continue;
      }
      entries = entries.map((e) =>
        e === waiting ? { ...e, key: file.key, status: "queued", attempt: 0, code: undefined } : e,
      );
      continue;
    }
    const code = checkFile(
      file,
      state.rules,
      entries.map((e) => e.name),
    );
    if (code) {
      rejected.push({ name: file.name, code });
      continue;
    }
    entries = [...entries, newEntry(file)];
  }
  return { ...state, entries, rejected: [...state.rejected, ...rejected] };
}

function restore(state: QueueState, a: Extract<Action, { type: "restore" }>): QueueState {
  const onServer = new Set(a.onServer.map((n) => n.toLowerCase()));
  const entries: Entry[] = a.files.map((f, i) => {
    const done = onServer.has(f.name.toLowerCase());
    return {
      key: `restored-${i}`,
      name: f.name,
      size: f.size,
      lastModified: f.lastModified,
      status: done ? "done" : "awaiting",
      sent: done ? f.size : 0,
      uploadUrl: done ? undefined : f.uploadUrl,
      attempt: 0,
    };
  });
  return { ...state, phase: "uploading", jobId: a.jobId, entries, rejected: [] };
}

function stopLive(state: QueueState): Entry[] {
  return state.entries.map((e) =>
    live.includes(e.status) ? { ...e, status: "paused" as const, speed: undefined } : e,
  );
}

function applyError(state: QueueState, a: Extract<Action, { type: "error" }>): QueueState {
  const entry = state.entries.find((e) => e.key === a.key);
  // Only a file that is sending can fail; an answer that arrives after a pause or a sign-out is stale.
  if (entry?.status !== "uploading") return state;

  switch (a.kind) {
    case "auth":
      return reduce(state, { type: "signedOut" });
    case "gone":
      // The upload vanished (expired, or deleted): start it again. If it was never created,
      // the job is what is missing.
      if (entry.uploadUrl) {
        return patch(state, a.key, (e) => ({
          ...e,
          uploadUrl: undefined,
          sent: 0,
          status: "queued",
          attempt: e.attempt + 1,
        }));
      }
      return { ...state, jobError: a.code, entries: stopLive(state) };
    case "refused":
      if (a.code === "job_not_uploading") {
        return { ...state, jobError: a.code, entries: stopLive(state) };
      }
      return patch(state, a.key, (e) => ({ ...e, status: "failed", code: a.code }));
    default: {
      const attempt = entry.attempt + 1;
      const limit =
        a.kind === "server" ? maxServerAttempts : a.kind === "busy" ? maxBusyAttempts : Infinity;
      if (attempt > limit) {
        return patch(state, a.key, (e) => ({ ...e, status: "failed", code: a.code, attempt }));
      }
      return patch(state, a.key, (e) => ({
        ...e,
        status: "reconnecting",
        attempt,
        retryAt: a.now + backoffMs(attempt, a.kind),
        speed: undefined,
      }));
    }
  }
}

export function reduce(state: QueueState, a: Action): QueueState {
  switch (a.type) {
    case "add":
      return add(state, a.files);
    case "dismissRejected":
      return { ...state, rejected: [] };
    case "jobCreated":
      return { ...state, phase: "uploading", jobId: a.jobId };
    case "restore":
      return restore(state, a);
    case "reset":
      return initialState(state.rules, state.online);
    case "start":
      return patch(state, a.key, (e) => ({
        ...e,
        status: "uploading",
        baseline: true,
        speed: undefined,
        lastAt: a.now,
        lastSent: e.sent,
        retryAt: undefined,
        resumedFrom: undefined,
      }));
    case "uploadUrl":
      return patch(state, a.key, (e) => ({ ...e, uploadUrl: a.url }));
    case "progress":
      return patch(state, a.key, (e) => {
        if (e.status !== "uploading") return e;
        const sent = Math.min(e.size, Math.max(a.sent, 0));
        if (e.baseline) {
          // The first figure after (re)starting is where the server was, not a speed.
          return {
            ...e,
            sent,
            baseline: false,
            lastAt: a.now,
            lastSent: sent,
            resumedFrom: sent > 0 ? sent : undefined,
          };
        }
        const dt = a.now - (e.lastAt ?? a.now);
        let speed = e.speed;
        if (dt > 0) {
          const instant = (sent - (e.lastSent ?? sent)) / (dt / 1000);
          speed = speed === undefined ? instant : 0.7 * speed + 0.3 * instant;
        }
        return {
          ...e,
          sent,
          speed,
          lastAt: dt > 0 ? a.now : e.lastAt,
          lastSent: dt > 0 ? sent : e.lastSent,
          attempt: sent > e.sent ? 0 : e.attempt,
        };
      });
    case "done":
      return patch(state, a.key, (e) => ({
        ...e,
        status: "done",
        sent: e.size,
        speed: undefined,
        attempt: 0,
        retryAt: undefined,
      }));
    case "error":
      return applyError(state, a);
    case "pause":
      return patch(state, a.key, (e) =>
        live.includes(e.status)
          ? { ...e, status: "paused", speed: undefined, retryAt: undefined }
          : e,
      );
    case "resume":
      return patch(state, a.key, (e) =>
        e.status === "paused" ? { ...e, status: "queued", attempt: 0 } : e,
      );
    case "retry":
      return patch(state, a.key, (e) =>
        e.status === "failed" ? { ...e, status: "queued", attempt: 0, code: undefined } : e,
      );
    case "retryDue":
      return patch(state, a.key, (e) =>
        e.status === "reconnecting" && (e.retryAt ?? 0) <= a.now
          ? { ...e, status: "queued", retryAt: undefined }
          : e,
      );
    case "remove":
      return { ...state, entries: state.entries.filter((e) => e.key !== a.key) };
    case "online":
      if (a.online === state.online) return state;
      return {
        ...state,
        online: a.online,
        // The connection is back: no reason to wait out the backoff.
        entries: a.online
          ? state.entries.map((e) =>
              e.status === "reconnecting"
                ? { ...e, status: "queued" as const, retryAt: undefined }
                : e,
            )
          : state.entries,
      };
    case "signedOut":
      return {
        ...state,
        signedOut: true,
        // Everything that was moving waits, in line, for the session.
        entries: state.entries.map((e) =>
          e.status === "uploading" || e.status === "reconnecting"
            ? { ...e, status: "queued" as const, speed: undefined, retryAt: undefined }
            : e,
        ),
      };
    case "signedIn":
      return { ...state, signedOut: false };
    case "submitting":
      return { ...state, phase: "submitting", submitError: null };
    case "submitted":
      return { ...state, phase: "submitted" };
    case "submitFailed":
      return { ...state, phase: "uploading", submitError: a.code };
  }
}

// ---- Questions the effects ask ----------------------------------------------------------

/** The keys of the files to start now: queued ones, in order, while a slot is free. */
export function toStart(state: QueueState): string[] {
  if (state.phase !== "uploading" || state.signedOut || !state.online || state.jobError) return [];
  const active = state.entries.filter((e) => e.status === "uploading").length;
  const free = Math.max(0, Math.max(1, state.rules.maxParallelPerUser) - active);
  return state.entries
    .filter((e) => e.status === "queued")
    .slice(0, free)
    .map((e) => e.key);
}

/** The job can be submitted: there are files and every one of them is on the server. */
export function allDone(state: QueueState): boolean {
  return (
    state.phase === "uploading" &&
    !state.jobError &&
    state.submitError === null &&
    state.entries.length > 0 &&
    state.entries.every((e) => e.status === "done")
  );
}

/** Unfinished work that a reload would interrupt. */
export function hasUnfinished(state: QueueState): boolean {
  return state.phase === "uploading" && state.entries.some((e) => e.status !== "done");
}

export function secondsLeft(e: Entry): number | undefined {
  if (e.status !== "uploading" || !e.speed || e.speed <= 0) return undefined;
  return (e.size - e.sent) / e.speed;
}

export function awaitingFiles(state: QueueState): Entry[] {
  return state.entries.filter((e) => e.status === "awaiting");
}
