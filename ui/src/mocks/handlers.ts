// MSW handlers that answer the contracts of #5 (session, errors), #11 (jobs) and #10 (uploads, in
// `uploads.ts`). State changes are kept in memory; `resetMocks()` puts the fixtures back.

import { HttpResponse, http } from "msw";
import type { Job, JobAction, JobState } from "@/api/types";
import { jobs as fixtureJobs, jobDetail, session } from "./fixtures";
import { type Scenario, seasonFiles, seasonJob } from "./scenarios";
import { filesOf, pendingOf, resetUploads, uploadHandlers } from "./uploads";

let jobs: Job[] = structuredClone(fixtureJobs);
let admin = false;

export function resetMocks() {
  jobs = structuredClone(fixtureJobs);
  admin = false;
  resetUploads();
}

/** Makes the mock user an administrator: sees every job, `mine=true` limits the list. */
export function setMockAdmin(value: boolean) {
  admin = value;
}

/** `npm run dev:mock` only: starts from other data than the fixtures (see `scenarios.ts`). */
export function applyScenarios(scenarios: readonly Scenario[]) {
  if (scenarios.includes("empty")) jobs = [];
  if (scenarios.includes("season")) jobs.push(structuredClone(seasonJob));
  if (scenarios.includes("admin")) admin = true;
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
];
