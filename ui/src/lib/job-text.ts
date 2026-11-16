// The job pages' rules that need no React: which texts an error code or a refusal reason
// maps to, how a probe reads, which actions show, and how often to poll (#13).

import type { JobAction, JobFilter, JobState, Probe } from "@/api/types";
import type { TKey } from "@/i18n";

// ---- Error codes and refusal reasons to text ------------------------------------------

/** The error codes of the job runner (#29) the UI has a sentence for (`jobError.<code>`). */
export const jobErrorCodes = [
  "worker_unavailable",
  "free_space",
  "task_timeout",
  "task_failed",
  "no_usable_files",
  "import_failed",
  "arr_unavailable",
] as const;

/** Refusal codes of a whole archive (#3). */
export const archiveRefusalCodes = [
  "not_a_zip",
  "too_many_entries",
  "too_large",
  "ratio",
  "free_space",
] as const;

/** Refusal codes of the content check (#4). */
export const fileRefusalCodes = [
  "extension",
  "no_video_stream",
  "no_duration",
  "not_text",
  "unreadable",
  "timeout",
] as const;

/** Reasons an archive entry was skipped (#3). */
export const skipReasonCodes = [
  "traversal",
  "absolute",
  "drive_letter",
  "backslash",
  "symlink",
  "special",
  "nested_archive",
  "extension",
  "name_too_long",
  "duplicate",
  "encrypted",
] as const;

const known = (list: readonly string[], code: string) => list.includes(code);

export type CodeText = { key: TKey; params?: Record<string, string> };

/**
 * The sentence for an error code of a failed or stalled job. An unknown code (a newer
 * server) falls back to a generic sentence that carries the code, so that it can be reported.
 */
export function jobErrorText(code: string, app = ""): CodeText {
  // The app that does not answer is named in the sentence.
  if (code === "arr_unavailable") return { key: "jobError.arr_unavailable", params: { app } };
  if (known(jobErrorCodes, code)) return { key: `jobError.${code}` as TKey };
  return { key: "jobError.unknown", params: { code } };
}

/**
 * Why a file or an archive was refused. The archive check and the content check share one
 * sentence per code, `refusal.<code>`.
 */
export function refusalText(code: string): CodeText {
  if (known(archiveRefusalCodes, code) || known(fileRefusalCodes, code)) {
    return { key: `refusal.${code}` as TKey };
  }
  return { key: "refusal.unknown", params: { code } };
}

/** Why an entry of an archive was skipped. */
export function skipReasonText(code: string): CodeText {
  if (known(skipReasonCodes, code)) return { key: `skip.${code}` as TKey };
  return { key: "skip.unknown", params: { code } };
}

// ---- Which meaning a state has (docs/design.md section 3) -------------------------------

export type StateTone = "moving" | "waiting" | "error" | "done";

export function stateTone(state: JobState): StateTone {
  switch (state) {
    case "failed":
      return "error";
    case "stalled":
    case "cancelled":
    case "awaiting-confirmation":
      return "waiting";
    case "probed":
    case "done":
      return "done";
    default:
      return "moving";
  }
}

// ---- Actions ----------------------------------------------------------------------------

/** The actions the job page offers, in the order they show. `submit` belongs to /upload. */
export const pageActions = ["retry", "cancel", "delete"] as const;
export type PageAction = (typeof pageActions)[number];

export function visibleActions(actions: readonly JobAction[]): PageAction[] {
  return pageActions.filter((a) => actions.includes(a));
}

// ---- Polling ----------------------------------------------------------------------------

/** States in which something is happening that the page follows closely. */
const moving: readonly JobState[] = [
  "uploading",
  "uploaded",
  "probing",
  "matched",
  "importing",
  "imported",
  "post-processing",
  "stalled",
];
/** States a job stays in until a person acts: nothing to poll for. */
const settled: readonly JobState[] = ["probed", "done", "cancelled", "failed"];

export const movingInterval = 2_000;
export const slowInterval = 10_000;
export const idleListInterval = 30_000;

/** Milliseconds until the next refresh of one job, or false: stop. */
export function jobPollInterval(state: JobState | undefined): number | false {
  if (state === undefined) return false;
  if (moving.includes(state)) return movingInterval;
  if (settled.includes(state)) return false;
  return slowInterval;
}

/** The same for a list: as fast as its fastest job. */
export function jobsPollInterval(states: readonly JobState[]): number {
  if (states.some((s) => moving.includes(s))) return movingInterval;
  if (states.some((s) => !settled.includes(s))) return slowInterval;
  return idleListInterval;
}

/** The filter of the list page as the API takes it. */
export function listFilter(state: JobState | "", mine: boolean, limit: number): JobFilter {
  return { states: state ? [state] : undefined, mine: mine || undefined, limit };
}

// ---- Probe ------------------------------------------------------------------------------

/** "1920x1080" with a multiplication sign. */
export function formatResolution(width: number, height: number): string {
  return `${width}×${height}`;
}

/** "H264", "AAC": the codec as the probe names it, for a person who knows it. */
export function formatCodec(codec: string): string {
  return codec.toUpperCase();
}

/** "2.0", "5.1": the channel layout by count, the usual way of writing it. */
export function formatChannels(channels: number): string {
  switch (channels) {
    case 1:
      return "1.0";
    case 2:
      return "2.0";
    case 6:
      return "5.1";
    case 8:
      return "7.1";
    default:
      return String(channels);
  }
}

/** "24000/1001" is "23.976"; undefined when it is not a usable fraction. */
export function formatFrameRate(fraction: string | undefined): string | undefined {
  if (!fraction) return undefined;
  const [num, den] = fraction.split("/").map(Number);
  if (num === undefined || !Number.isFinite(num) || num <= 0) return undefined;
  const value = num / (den && Number.isFinite(den) && den > 0 ? den : 1);
  return String(Math.round(value * 1000) / 1000);
}

/**
 * A language as tagged in a file ("eng", "spa", "es-419") in the user's language: "English".
 * An empty or "und" tag is `undefined`; a tag the runtime does not know comes back as it was.
 */
export function languageName(tag: string | undefined, lang: string): string | undefined {
  if (!tag || tag === "und") return undefined;
  try {
    const name = new Intl.DisplayNames([lang], { type: "language" }).of(tag);
    return name && name !== tag ? name : tag;
  } catch {
    return tag;
  }
}

export type TrackFlag = "default" | "forced" | "hearingImpaired";

export type TrackLine = {
  key: string;
  language?: string;
  title?: string;
  /** "AAC 2.0", "SUBRIP". */
  detail: string;
  flags: TrackFlag[];
};

export type ProbeSummary = {
  duration: string;
  /** Of the first video track. */
  resolution?: string;
  videoCodec?: string;
  frameRate?: string;
  audio: TrackLine[];
  subtitles: TrackLine[];
};

type ProbeFormat = { formatDuration: (ms: number) => string; lang: string };

/** What the page shows of a probe: duration, resolution, codec, and the tracks. */
export function summarizeProbe(probe: Probe, fmt: ProbeFormat): ProbeSummary {
  const video = probe.video[0];
  return {
    duration: fmt.formatDuration(probe.durationMs),
    resolution: video ? formatResolution(video.width, video.height) : undefined,
    videoCodec: video ? formatCodec(video.codec) : undefined,
    frameRate: formatFrameRate(video?.frameRate),
    audio: probe.audio.map((a) => ({
      key: `a${a.index}`,
      language: languageName(a.language, fmt.lang),
      title: a.title,
      detail: [formatCodec(a.codec), a.channels ? formatChannels(a.channels) : undefined]
        .filter(Boolean)
        .join(" "),
      flags: a.default ? ["default"] : [],
    })),
    subtitles: probe.subtitles.map((s) => {
      const flags: TrackFlag[] = [];
      if (s.default) flags.push("default");
      if (s.forced) flags.push("forced");
      if (s.hearingImpaired) flags.push("hearingImpaired");
      return {
        key: `s${s.index}`,
        language: languageName(s.language, fmt.lang),
        title: s.title,
        detail: formatCodec(s.codec),
        flags,
      };
    }),
  };
}

// ---- Age --------------------------------------------------------------------------------

/** "5 minutes ago", "hace 2 dias": the largest whole unit between `then` and `now`. */
export function formatAge(then: string, now: Date, lang: string): string {
  const seconds = Math.round((new Date(then).getTime() - now.getTime()) / 1000);
  if (Number.isNaN(seconds)) return "";
  const units: [Intl.RelativeTimeFormatUnit, number][] = [
    ["day", 86400],
    ["hour", 3600],
    ["minute", 60],
  ];
  const rtf = new Intl.RelativeTimeFormat(lang, { numeric: "auto" });
  for (const [unit, size] of units) {
    if (Math.abs(seconds) >= size) return rtf.format(Math.trunc(seconds / size), unit);
  }
  return rtf.format(0, "minute");
}
