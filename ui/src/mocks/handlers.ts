// MSW handlers that answer the contracts of #5 (session, errors), #11 (jobs) and #10 (uploads, in
// `uploads.ts`). State changes are kept in memory; `resetMocks()` puts the fixtures back.

import { HttpResponse, http } from "msw";
import type { ConfirmRequest, Job, JobAction, JobState, MatchRequest, Target } from "@/api/types";
import { adminSettings, adminStatus } from "./admin-fixtures";
import { confirmRefusal, type ImportProgress, importAt } from "./confirm-fixtures";
import { jobs as fixtureJobs, jobDetail, session } from "./fixtures";
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
} from "./scenarios";
import { filesOf, pendingOf, resetUploads, uploadHandlers } from "./uploads";

let jobs: Job[] = structuredClone(fixtureJobs);
let targets: Record<string, Target> = {};
let admin = false;
let imports: Record<string, ImportProgress> = {};

export function resetMocks() {
  jobs = structuredClone(fixtureJobs);
  targets = {};
  imports = {};
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
    if (!info.rootFolders.some((f) => f.path === body.rootFolder)) {
      return failure(400, "bad_request", "That root folder is not one the app lists.");
    }
    if (body.app === "radarr") {
      const hit = movieHits.find((h) => h.tmdbId === body.tmdbId);
      if (!hit) return failure(404, "title_not_found", "The app does not know that title.");
      if (hit.hasFile) return failure(409, "already_in_library", "The film already has a file.");
      targets[job.id] = { app: "radarr", titleId: hit.tmdbId, title: hit.title, year: hit.year };
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
    return HttpResponse.json(move(job, "probed"));
  }),

  // ---- Confirm (#40) ----

  http.post("/api/jobs/:id/confirm", async ({ params, request }) => {
    const job = jobs.find((j) => j.id === params.id && j.owner === session.user.name);
    if (!job) return notFound();
    const proposal = currentProposal(job.id);
    if (job.state !== "awaiting-confirmation" || !proposal) {
      return failure(409, "wrong_state", "The job is not waiting for confirmation.");
    }
    const body = (await request.json()) as ConfirmRequest;
    const refusal = confirmRefusal(proposal.rows, body);
    if (refusal) {
      return HttpResponse.json(
        { error: { code: refusal.code, message: refusal.message, rows: refusal.rows } },
        { status: refusal.status },
      );
    }
    const going = proposal.rows.filter((r) => !r.skip);
    imports[job.id] = { rows: going.map((r) => ({ id: r.id, name: r.name })), tick: 0 };
    return HttpResponse.json(move(job, "importing"), { status: 202 });
  }),
];
