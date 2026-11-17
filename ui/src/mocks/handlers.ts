// MSW handlers that answer the contracts of #5 (session, errors), #11 (jobs) and #10 (uploads, in
// `uploads.ts`). State changes are kept in memory; `resetMocks()` puts the fixtures back.

import { HttpResponse, http } from "msw";
import type {
  ConfirmRequest,
  Flow2,
  Flow2ConfirmRequest,
  Job,
  JobAction,
  JobState,
  MatchRequest,
  Preview,
  PreviewRequest,
  Target,
} from "@/api/types";
import { adminSettings, adminStatus } from "./admin-fixtures";
import { confirmRefusal, type ImportProgress, importAt } from "./confirm-fixtures";
import { jobs as fixtureJobs, jobDetail, session } from "./fixtures";
import {
  type Flow2Progress,
  flow2Fixture,
  flow2ImportAt,
  flow2Jobs,
  flow2Target,
  initialFlow2s,
  labelProblem,
  noPreview,
  previewRefusal,
  readyPreview,
} from "./flow2-fixtures";
import {
  applyMappingScenarios,
  currentProposal,
  mappingHandlers,
  resetMapping,
} from "./mapping-fixtures";
import { apps, movieHits, searchMovies, searchSeries, seriesHits } from "./match-fixtures";
import {
  dubJobs,
  importDetails,
  importingJobs,
  mappingJobs,
  type Scenario,
  seasonFiles,
  seasonJob,
  versionsJobs,
} from "./scenarios";
import { filesOf, pendingOf, resetUploads, uploadHandlers } from "./uploads";

let jobs: Job[] = structuredClone(fixtureJobs);
let targets: Record<string, Target> = {};
let admin = false;
let imports: Record<string, ImportProgress> = {};
// Flow 2: the comparison of each job that has one, its sync-check clips, and what a confirmed
// merge or version has reached. A preview is `pending` for one read, then `ready`.
let flow2s: Record<string, Flow2> = {};
let previews: Record<string, Preview> = {};
let flow2Progress: Record<string, Flow2Progress> = {};

export function resetMocks() {
  jobs = structuredClone(fixtureJobs);
  targets = {};
  imports = {};
  flow2s = {};
  previews = {};
  flow2Progress = {};
  admin = false;
  resetUploads();
  resetMapping();
}

/** Makes the mock user an administrator: sees every job, `mine=true` limits the list. */
export function setMockAdmin(value: boolean) {
  admin = value;
}

/** `npm run dev:mock` only: starts from other data than the fixtures (see `scenarios.ts`). */
export function applyScenarios(scenarios: readonly Scenario[]) {
  if (scenarios.includes("empty")) jobs = [];
  if (scenarios.includes("season")) jobs.push(structuredClone(seasonJob));
  if (scenarios.includes("mapping")) jobs.push(...structuredClone(mappingJobs));
  if (scenarios.includes("importing")) jobs.push(...structuredClone(importingJobs));
  if (scenarios.includes("dub")) jobs.push(...structuredClone(dubJobs));
  if (scenarios.includes("versions")) jobs.push(...structuredClone(versionsJobs));
  if (scenarios.includes("flow2")) {
    jobs.push(...structuredClone(flow2Jobs));
    Object.assign(flow2s, initialFlow2s());
    for (const j of flow2Jobs) targets[j.id] = { ...flow2Target };
  }
  if (scenarios.includes("admin")) admin = true;
  applyMappingScenarios(scenarios);
}

/** The mock API's rule (#11): a user sees their own jobs, an admin everyone's. */
const visible = (job: Job) => admin || job.owner === session.user.name;

function failure(status: number, code: string, message: string) {
  return HttpResponse.json({ error: { code, message } }, { status });
}

const notFound = () => failure(404, "not_found", "No such job.");

const actionsFor: Partial<Record<JobState, JobAction[]>> = {
  uploading: ["cancel", "submit"],
  failed: ["cancel", "retry", "delete"],
  cancelled: ["delete"],
  done: ["delete"],
};

function move(job: Job, state: JobState): Job {
  job.state = state;
  job.updatedAt = new Date().toISOString();
  job.actions = actionsFor[state] ?? ["cancel"];
  return job;
}

export const handlers = [
  http.get("/healthz", () => HttpResponse.json({ status: "ok", version: session.version })),

  http.get("/api/session", () =>
    HttpResponse.json({ ...session, user: { ...session.user, admin } }),
  ),

  // Admin endpoints (#69): a non-admin gets the answer of a path that does not exist.
  http.get("/api/admin/status", () =>
    admin ? HttpResponse.json(adminStatus) : failure(404, "not_found", "No such API endpoint."),
  ),
  http.get("/api/admin/settings", () =>
    admin ? HttpResponse.json(adminSettings) : failure(404, "not_found", "No such API endpoint."),
  ),

  // Uploads belong to their owner alone, an admin included (#10).
  ...uploadHandlers((id) => jobs.find((j) => j.id === id && j.owner === session.user.name)),

  http.get("/api/jobs", ({ request }) => {
    const q = new URL(request.url).searchParams;
    const states = q.getAll("state");
    const offset = Number(q.get("offset") ?? 0);
    const limit = Number(q.get("limit") ?? 50);
    const mine = q.get("mine") === "true";
    const list = jobs
      .filter((j) => visible(j) && (!mine || j.owner === session.user.name))
      .filter((j) => states.length === 0 || states.includes(j.state))
      .sort((a, b) => b.createdAt.localeCompare(a.createdAt))
      .slice(offset, offset + limit);
    return HttpResponse.json({ jobs: list });
  }),

  http.post("/api/jobs", () => {
    const now = new Date().toISOString();
    const job: Job = {
      id: crypto.randomUUID().replaceAll("-", ""),
      owner: session.user.name,
      state: "uploading",
      attempt: 1,
      error: null,
      name: null,
      fileCount: 0,
      totalSize: 0,
      createdAt: now,
      updatedAt: now,
      expiresAt: null,
      actions: ["cancel"],
    };
    jobs.push(job);
    return HttpResponse.json(job, { status: 201 });
  }),

  http.get("/api/jobs/:id", ({ params }) => {
    const job = jobs.find((j) => j.id === params.id && visible(j));
    if (!job) return notFound();
    const detail = jobDetail(job);
    if (job.id === seasonJob.id) detail.files = seasonFiles;
    Object.assign(detail, structuredClone(importDetails[job.id] ?? {}));
    const target = targets[job.id];
    if (target) detail.target = target;
    const flow2 = flow2s[job.id];
    if (flow2) detail.flow2 = flow2;
    const moved = flow2Progress[job.id];
    if (moved) {
      const at = flow2ImportAt(job.state === "done" ? { ...moved, tick: 99 } : moved);
      if (job.state !== "done") moved.tick += 1;
      move(job, at.state);
      detail.state = job.state;
      detail.actions = job.actions;
      detail.import = at.import;
      detail.result = at.result;
    }
    const progress = imports[job.id];
    if (progress && !["done", "failed", "cancelled"].includes(job.state)) {
      const at = importAt(progress);
      progress.tick += 1;
      move(job, at.state);
      detail.state = job.state;
      detail.actions = job.actions;
      detail.import = at.import;
      detail.result = at.result;
    } else if (progress) {
      const at = importAt({ ...progress, tick: 3 });
      detail.import = at.import;
      detail.result = at.result;
    }
    if (job.state === "uploading") {
      // The files and unfinished uploads the mock tus endpoint holds for this job.
      detail.files = [...detail.files, ...filesOf(job.id)];
      const pending = pendingOf(job.id);
      if (pending.length > 0) detail.uploads = pending;
    }
    return HttpResponse.json(detail);
  }),

  http.post("/api/jobs/:id/submit", ({ params }) => {
    const job = jobs.find((j) => j.id === params.id && visible(j));
    if (!job) return notFound();
    if (job.state !== "uploading") return failure(409, "wrong_state", "The job is not uploading.");
    if (job.fileCount === 0) return failure(409, "no_files", "The job has no files.");
    return HttpResponse.json(move(job, "uploaded"));
  }),

  http.post("/api/jobs/:id/cancel", ({ params }) => {
    const job = jobs.find((j) => j.id === params.id && visible(j));
    if (!job) return notFound();
    if (!job.actions.includes("cancel")) {
      return failure(409, "wrong_state", "The job cannot be cancelled now.");
    }
    return HttpResponse.json(move(job, "cancelled"));
  }),

  http.post("/api/jobs/:id/retry", ({ params }) => {
    const job = jobs.find((j) => j.id === params.id && visible(j));
    if (!job) return notFound();
    if (job.state !== "failed") return failure(409, "wrong_state", "The job has not failed.");
    job.error = null;
    job.attempt += 1;
    return HttpResponse.json(move(job, "uploaded"));
  }),

  http.delete("/api/jobs/:id", ({ params }) => {
    const job = jobs.find((j) => j.id === params.id && visible(j));
    if (!job) return notFound();
    if (!job.actions.includes("delete")) return failure(409, "job_active", "The job is active.");
    jobs = jobs.filter((j) => j !== job);
    return new HttpResponse(null, { status: 204 });
  }),

  // ---- Proposal (#38) ----

  ...mappingHandlers,

  // ---- Match (#37) ----

  http.get("/api/apps", () => HttpResponse.json(apps)),

  http.get("/api/lookup/movies", ({ request }) =>
    HttpResponse.json({
      results: searchMovies(new URL(request.url).searchParams.get("term") ?? ""),
    }),
  ),

  http.get("/api/lookup/series", ({ request }) =>
    HttpResponse.json({
      results: searchSeries(new URL(request.url).searchParams.get("term") ?? ""),
    }),
  ),

  http.post("/api/jobs/:id/match", async ({ params, request }) => {
    const job = jobs.find((j) => j.id === params.id && j.owner === session.user.name);
    if (!job) return notFound();
    if (!["probed", "matched", "awaiting-confirmation"].includes(job.state)) {
      return failure(409, "wrong_state", "The job cannot be matched now.");
    }
    const body = (await request.json()) as MatchRequest;
    const info = apps[body.app];
    if (!info) return failure(404, "app_not_configured", "That app is not configured.");
    // The server fills in a missing folder from the settings, and uses it only to add a title.
    if (
      body.rootFolder !== undefined &&
      !info.rootFolders.some((f) => f.path === body.rootFolder)
    ) {
      return failure(400, "bad_request", "That root folder is not one the app lists.");
    }
    if (body.app === "radarr") {
      const hit = movieHits.find((h) => h.tmdbId === body.tmdbId);
      if (!hit) return failure(404, "title_not_found", "The app does not know that title.");
      targets[job.id] = { app: "radarr", titleId: hit.tmdbId, title: hit.title, year: hit.year };
      if (hit.hasFile) {
        // The film has a file: the job is flow 2, and goes straight to the comparison.
        flow2s[job.id] = flow2Fixture("match");
        return HttpResponse.json(move(job, "awaiting-confirmation"));
      }
    } else {
      const hit = seriesHits.find((h) => h.tvdbId === body.tvdbId);
      if (!hit) return failure(404, "title_not_found", "The app does not know that title.");
      targets[job.id] = { app: "sonarr", titleId: hit.tvdbId, title: hit.title, year: hit.year };
    }
    return HttpResponse.json(move(job, "matched"));
  }),

  http.delete("/api/jobs/:id/match", ({ params }) => {
    const job = jobs.find((j) => j.id === params.id && j.owner === session.user.name);
    if (!job) return notFound();
    if (job.state !== "matched" && job.state !== "awaiting-confirmation") {
      return failure(409, "wrong_state", "The job has no title chosen.");
    }
    delete targets[job.id];
    delete flow2s[job.id];
    delete previews[job.id];
    return HttpResponse.json(move(job, "probed"));
  }),

  // ---- Flow 2: the sync check (#53) ----

  http.get("/api/jobs/:id/preview", ({ params }) => {
    const job = jobs.find((j) => j.id === params.id && j.owner === session.user.name);
    if (!job) return notFound();
    if (!flow2s[job.id]) return failure(409, "not_flow2", "The job is not flow 2.");
    const current = previews[job.id] ?? noPreview;
    if (current.state !== "pending") return HttpResponse.json(current);
    // The worker is done by the next read.
    const ready = readyPreview(
      job.id,
      { offsetMs: current.offsetMs ?? 0, audioTrack: current.audioTrack ?? 0 },
      current.requestedAt ?? "",
    );
    previews[job.id] = ready;
    return HttpResponse.json(current);
  }),

  http.post("/api/jobs/:id/preview", async ({ params, request }) => {
    const job = jobs.find((j) => j.id === params.id && j.owner === session.user.name);
    if (!job) return notFound();
    const flow2 = flow2s[job.id];
    if (!flow2) return failure(409, "not_flow2", "The job is not flow 2.");
    if (job.state !== "awaiting-confirmation") {
      return failure(409, "wrong_state", "The job is not awaiting confirmation.");
    }
    const text = await request.text();
    const body = (text.trim() === "" ? {} : JSON.parse(text)) as PreviewRequest;
    const refusal = previewRefusal(flow2, body);
    if (refusal)
      return failure(refusal.code === "bad_request" ? 400 : 409, refusal.code, "Refused.");
    const track =
      body.audioTrack ??
      (flow2.upload?.audio.find((a) => a.default) ?? flow2.upload?.audio[0])?.index ??
      0;
    const pending: Preview = {
      ...noPreview,
      state: "pending",
      offsetMs: body.offsetMs ?? 0,
      audioTrack: track,
      requestedAt: new Date().toISOString(),
      requestedBy: session.user.name,
    };
    previews[job.id] = pending;
    return HttpResponse.json(pending, { status: 202 });
  }),

  http.get("/api/jobs/:id/preview/:n", ({ params }) => {
    const ready = previews[String(params.id)];
    if (ready?.state !== "ready" || !ready.clips.some((c) => String(c.n) === params.n)) {
      return failure(404, "not_found", "No such clip.");
    }
    return new HttpResponse(new Uint8Array(), { headers: { "Content-Type": "video/mp4" } });
  }),

  // ---- Confirm (#40) ----

  http.post("/api/jobs/:id/confirm", async ({ params, request }) => {
    const job = jobs.find((j) => j.id === params.id && j.owner === session.user.name);
    if (!job) return notFound();
    const flow2 = flow2s[job.id];
    if (flow2) {
      if (job.state !== "awaiting-confirmation") {
        return failure(409, "wrong_state", "The job is not waiting for confirmation.");
      }
      const body = (await request.json()) as Flow2ConfirmRequest | ConfirmRequest;
      if (!("flow2" in body)) {
        return failure(409, "flow2", "The film has a file: merge it or keep a second version.");
      }
      if (!flow2.offers.includes(body.flow2)) {
        return body.flow2 === "merge"
          ? failure(409, "merge_not_offered", "Not offered.")
          : failure(409, "version_unavailable", "Not offered.");
      }
      if (body.flow2 === "version") {
        const label = body.version.label?.trim() || flow2.version?.label || "";
        const problem = labelProblem(label);
        if (problem) {
          return HttpResponse.json(
            {
              error: {
                code: "version_label",
                message: "The label is refused.",
                reason: problem,
                ...(problem === "label_forbidden"
                  ? { token: label.match(/1080p|720p|web-?dl|bluray/i)?.[0].toLowerCase() }
                  : {}),
              },
            },
            { status: 409 },
          );
        }
        flow2Progress[job.id] = {
          kind: "version",
          label,
          name: `Sintel (2010) - ${label}.mkv`,
          tick: 0,
        };
      } else {
        const preview = previews[job.id];
        const merge = body.merge;
        const track = merge.audioTracks?.[0];
        if (
          preview?.state !== "ready" ||
          (merge.offsetMs ?? preview.offsetMs) !== preview.offsetMs ||
          (track ?? preview.audioTrack) !== preview.audioTrack
        ) {
          return failure(409, "preview_required", "The sync check is missing.");
        }
        flow2Progress[job.id] = {
          kind: "merge",
          offsetMs: preview.offsetMs ?? 0,
          audioTracks: [preview.audioTrack ?? 0],
          subtitleTracks: merge.subtitleTracks ?? [],
          languageTag: merge.languageTag ?? "es-419",
          trackName: merge.trackName ?? "Español (Latino)",
          keepOriginalDefault: merge.keepOriginalDefault ?? true,
          tick: 0,
        };
      }
      return HttpResponse.json(move(job, "importing"), { status: 202 });
    }
    const proposal = currentProposal(job.id);
    if (job.state !== "awaiting-confirmation" || !proposal) {
      return failure(409, "wrong_state", "The job is not waiting for confirmation.");
    }
    const body = (await request.json()) as ConfirmRequest;
    const refusal = confirmRefusal(proposal.rows, body, proposal.seasonDub?.decided);
    if (refusal) {
      return HttpResponse.json(
        { error: { code: refusal.code, message: refusal.message, rows: refusal.rows } },
        { status: refusal.status },
      );
    }
    const going = proposal.rows.filter((r) => !r.skip);
    const view = proposal.seasonDub?.version;
    const keeps = (body.seasonDub || proposal.seasonDub?.decided) === "version" && view;
    const label = body.version?.label || view?.label || "";
    imports[job.id] = {
      rows: going.map((r) => ({ id: r.id, name: r.name })),
      ...(keeps && view
        ? {
            label,
            versions: view.rows.map((v) => ({
              id: v.row,
              name: proposal.rows.find((r) => r.id === v.row)?.name ?? v.row,
              season: v.seasonNumber,
              episode: v.episodeNumber,
              version: v.name.replace(/ - [^-]*(\.\w+)$/, ` - ${label}$1`),
            })),
          }
        : {}),
      tick: 0,
    };
    return HttpResponse.json(move(job, "importing"), { status: 202 });
  }),
];
