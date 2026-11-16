// MSW handlers that answer the contracts of #5 (session, errors) and #11 (jobs). #12 adds the
// upload endpoint. State changes are kept in memory; `resetMocks()` puts the fixtures back.

import { HttpResponse, http } from "msw";
import type { Job, JobAction, JobState } from "@/api/types";
import { jobs as fixtureJobs, jobDetail, session } from "./fixtures";

let jobs: Job[] = structuredClone(fixtureJobs);

export function resetMocks() {
  jobs = structuredClone(fixtureJobs);
}

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

  http.get("/api/session", () => HttpResponse.json(session)),

  http.get("/api/jobs", ({ request }) => {
    const q = new URL(request.url).searchParams;
    const states = q.getAll("state");
    const offset = Number(q.get("offset") ?? 0);
    const limit = Number(q.get("limit") ?? 50);
    const list = jobs
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
    const job = jobs.find((j) => j.id === params.id);
    return job ? HttpResponse.json(jobDetail(job)) : notFound();
  }),

  http.post("/api/jobs/:id/submit", ({ params }) => {
    const job = jobs.find((j) => j.id === params.id);
    if (!job) return notFound();
    if (job.state !== "uploading") return failure(409, "wrong_state", "The job is not uploading.");
    if (job.fileCount === 0) return failure(409, "no_files", "The job has no files.");
    return HttpResponse.json(move(job, "uploaded"));
  }),

  http.post("/api/jobs/:id/cancel", ({ params }) => {
    const job = jobs.find((j) => j.id === params.id);
    if (!job) return notFound();
    if (!job.actions.includes("cancel")) {
      return failure(409, "wrong_state", "The job cannot be cancelled now.");
    }
    return HttpResponse.json(move(job, "cancelled"));
  }),

  http.post("/api/jobs/:id/retry", ({ params }) => {
    const job = jobs.find((j) => j.id === params.id);
    if (!job) return notFound();
    if (job.state !== "failed") return failure(409, "wrong_state", "The job has not failed.");
    job.error = null;
    job.attempt += 1;
    return HttpResponse.json(move(job, "uploaded"));
  }),

  http.delete("/api/jobs/:id", ({ params }) => {
    const job = jobs.find((j) => j.id === params.id);
    if (!job) return notFound();
    if (!job.actions.includes("delete")) return failure(409, "job_active", "The job is active.");
    jobs = jobs.filter((j) => j !== job);
    return new HttpResponse(null, { status: 204 });
  }),
];
