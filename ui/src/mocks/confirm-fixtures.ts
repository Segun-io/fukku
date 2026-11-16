// Rules and data for the mock confirm endpoint (#40), as the real server answers it: the
// refusals with their rows inside the `error` object, and the import and result that
// `GET /api/jobs/{id}` shows while the import moves on. Names are made up.

import type { ProposalRow } from "@/api/proposal";
import type { ConfirmRequest, ImportInfo, ImportResult } from "@/api/types";

export type Refusal = { status: number; code: string; message: string; rows?: string[] };

const hasIssues = (r: ProposalRow) => r.rejections.length > 0 || r.warnings.length > 0;

/** Why the server would refuse this confirmation, or null. */
export function confirmRefusal(rows: ProposalRow[], body: ConfirmRequest): Refusal | null {
  const ids = new Set(rows.map((r) => r.id));
  if (body.overrides.some((id) => !ids.has(id))) {
    return { status: 400, code: "bad_request", message: "An override names no row." };
  }
  const going = rows.filter((r) => !r.skip);
  if (going.length === 0) {
    return { status: 409, code: "nothing_to_import", message: "Every row is skipped." };
  }
  const bad = (r: ProposalRow) => r.episodes !== undefined && r.episodes.length === 0;
  const incomplete = going.filter(bad).map((r) => r.id);
  if (incomplete.length > 0) {
    return {
      status: 409,
      code: "incomplete_rows",
      message: "Some rows have no episode.",
      rows: incomplete,
    };
  }
  const open = going.filter((r) => hasIssues(r) && !body.overrides.includes(r.id));
  if (open.length > 0) {
    return {
      status: 409,
      code: "needs_override",
      message: "Some rows need an override.",
      rows: open.map((r) => r.id),
    };
  }
  for (const s of body.subtitles ?? []) {
    if (!/^[a-z]{2}$/.test(s.language)) {
      return { status: 400, code: "bad_request", message: "Unknown language." };
    }
  }
  return null;
}

/** What a mock import has reached: each call of the job detail moves it one step on. */
export type ImportProgress = { rows: { id: string; name: string }[]; tick: number };

/** The import and result of a mock job at `tick`; `state` is the job state to move to. */
export function importAt(p: ImportProgress): {
  state: "importing" | "post-processing" | "done";
  import: ImportInfo;
  result: ImportResult | null;
} {
  const rows = (result: (i: number) => string) =>
    p.rows.map((r, i) => ({ id: r.id, name: r.name, result: result(i) }));
  if (p.tick === 0) {
    return {
      state: "importing",
      import: { status: "running", rows: rows(() => "") },
      result: null,
    };
  }
  if (p.tick === 1) {
    return {
      state: "importing",
      import: { status: "running", rows: rows((i) => (i === 0 ? "imported" : "")) },
      result: null,
    };
  }
  const done = { status: "done", rows: rows(() => "imported") };
  if (p.tick === 2) {
    return {
      state: "post-processing",
      import: done,
      result: {
        titleLink: null,
        monitored: true,
        unmonitorReason: "",
        postProcessing: [
          { step: "mediaServerRefresh", status: "pending", detail: "" },
          { step: "subtitles", status: "pending", detail: "" },
        ],
      },
    };
  }
  return {
    state: "done",
    import: done,
    result: {
      titleLink: "https://media.example.com/web/index.html#!/details?id=0a1b2c",
      monitored: true,
      unmonitorReason: "",
      postProcessing: [
        { step: "mediaServerRefresh", status: "ok", detail: "" },
        { step: "subtitles", status: "skipped", detail: "subtitle manager not configured" },
      ],
    },
  };
}
