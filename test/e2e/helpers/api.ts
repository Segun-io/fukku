import { readFile } from "node:fs/promises";
import path from "node:path";
import { baseURL, type TestUser } from "./env";

// A client of the job API (#11) and a minimal tus client (#10) for tests that need no browser.
// It goes through the test proxy as one of its users (the cookie the proxy reads) and sends the
// Origin header a browser would, because every state change is checked against it.

export interface JobFile {
  id: number;
  path: string;
  name: string;
  kind: string;
  size: number;
  status: string;
  reason: string | null;
  from: string | null;
  probe: unknown;
  extraction: {
    refused?: string;
    files: { path: string; size: number }[];
    skipped: { name: string; reason: string }[];
  } | null;
}

export interface Job {
  id: string;
  owner: string;
  state: string;
  error: { code: string; message: string } | null;
  fileCount: number;
  files: JobFile[];
  manualImport: { app: string; folder: string; url: string | null }[];
  actions: string[];
  history: { at: string; from: string | null; to: string; actor: string; note: string }[];
  /** The chosen title (#37), and the import and its result (#40); null until they apply. */
  target: { app: string; titleId: number; title: string; year: number } | null;
  import: { status: string; rows: { id: string; name: string; result: string }[] } | null;
  result: {
    titleLink: string | null;
    monitored: boolean | null;
    unmonitorReason: string;
    postProcessing: { step: string; status: string; detail: string }[];
  } | null;
}

/** A row of the proposal (#38), as GET /api/jobs/{id}/proposal shows it. */
export interface ProposalRow {
  id: string;
  fileId: number;
  name: string;
  durationMs: number;
  skip: boolean;
  seasonNumber?: number;
  episodes?: { id: number; number: number; title: string; hasFile: boolean }[];
  quality: { id: number; name: string } | null;
  languages: { id: number; name: string }[];
  customFormats: string[];
  customFormatScore: number;
  rejections: { reason: string; permanent: boolean }[];
  warnings: { code: string; customFormats?: string[]; score?: number; minFormatScore?: number }[];
  importable: boolean;
}

export interface Proposal {
  state: "pending" | "ready";
  app: string;
  target: { titleId: number; title: string; year: number } | null;
  rows: ProposalRow[];
  hints: { code: string }[];
  episodes?: { id: number; seasonNumber: number; number: number; title: string }[];
}

/** What a request answered, for the calls whose refusal a test looks at. */
export interface Answer<T> {
  status: number;
  body: T;
}

/** States a job passes through by itself after submit; anything else is where it came to rest. */
const chunkSize = 1 << 20;
const moving = new Set(["uploaded", "probing"]);

export class Api {
  constructor(private readonly user: TestUser) {}

  headers(extra: Record<string, string> = {}): Record<string, string> {
    return { Cookie: `fukku_test_user=${this.user}`, Origin: baseURL, ...extra };
  }

  async json<T>(method: string, url: string, expected: number): Promise<T> {
    const res = await fetch(baseURL + url, { method, headers: this.headers() });
    const text = await res.text();
    if (res.status !== expected) throw new Error(`${method} ${url}: ${res.status} ${text}`);
    return text ? (JSON.parse(text) as T) : (undefined as T);
  }

  /** Any request with a JSON body; the answer whatever its status. */
  async send<T>(method: string, url: string, body?: unknown): Promise<Answer<T>> {
    const res = await fetch(baseURL + url, {
      method,
      headers: this.headers(body === undefined ? {} : { "Content-Type": "application/json" }),
      body: body === undefined ? undefined : JSON.stringify(body),
    });
    const text = await res.text();
    let parsed: unknown = text;
    try {
      parsed = text ? JSON.parse(text) : undefined;
    } catch {
      // not JSON: kept as text, for the message of a failed test
    }
    return { status: res.status, body: parsed as T };
  }

  async expect<T>(method: string, url: string, expected: number, body?: unknown): Promise<T> {
    const a = await this.send<T>(method, url, body);
    if (a.status !== expected) {
      throw new Error(`${method} ${url}: ${a.status} ${JSON.stringify(a.body)}`);
    }
    return a.body;
  }

  /** POST /api/jobs/{id}/match (#37): the title, with the settings' defaults for the rest. */
  match(id: string, body: { app: "radarr"; tmdbId: number } | { app: "sonarr"; tvdbId: number }) {
    return this.send<Job>("POST", `/api/jobs/${id}/match`, body);
  }

  proposal(id: string): Promise<Proposal> {
    return this.expect("GET", `/api/jobs/${id}/proposal`, 200);
  }

  /** The proposal once it is ready (the matched step has asked the app). */
  async readyProposal(id: string, timeoutMs = 120_000): Promise<Proposal> {
    const deadline = Date.now() + timeoutMs;
    for (;;) {
      const job = await this.job(id);
      if (job.state === "awaiting-confirmation") return this.proposal(id);
      if (job.state !== "matched" || Date.now() > deadline) {
        throw new Error(
          `job ${id} is ${job.state} (${JSON.stringify(job.error)}), not awaiting confirmation`,
        );
      }
      await new Promise((r) => setTimeout(r, 500));
    }
  }

  /** POST /api/jobs/{id}/confirm (#40); the answer as it is, for the refusals. */
  confirm(
    id: string,
    body: {
      overrides?: string[];
      subtitles?: { fileId: number; language: string; forced: boolean; hearingImpaired: boolean }[];
    },
  ) {
    return this.send<Job & { error?: { code: string; rows?: string[] } }>(
      "POST",
      `/api/jobs/${id}/confirm`,
      body,
    );
  }

  /** Poll the job until `done(job)` says so; fails on a state in `stop`. */
  async until(
    id: string,
    what: string,
    done: (job: Job) => boolean,
    {
      timeoutMs = 180_000,
      stop = ["failed", "cancelled"],
    }: { timeoutMs?: number; stop?: string[] } = {},
  ): Promise<Job> {
    const deadline = Date.now() + timeoutMs;
    let last: Job | null = null;
    for (;;) {
      try {
        last = await this.job(id);
      } catch (err) {
        // fukku-web may be restarting: try again until the deadline
        if (Date.now() > deadline) throw err;
        await new Promise((r) => setTimeout(r, 500));
        continue;
      }
      if (done(last)) return last;
      if (stop.includes(last.state)) {
        throw new Error(
          `job ${id} is ${last.state} (${JSON.stringify(last.error)}) while waiting for ${what}`,
        );
      }
      if (Date.now() > deadline) {
        throw new Error(
          `job ${id} is still ${last.state} after ${timeoutMs} ms, waiting for ${what}`,
        );
      }
      await new Promise((r) => setTimeout(r, 250));
    }
  }

  createJob(): Promise<Job> {
    return this.json("POST", "/api/jobs", 201);
  }

  job(id: string): Promise<Job> {
    return this.json("GET", `/api/jobs/${id}`, 200);
  }

  submit(id: string): Promise<Job> {
    return this.json("POST", `/api/jobs/${id}/submit`, 200);
  }

  cancel(id: string): Promise<Job> {
    return this.json("POST", `/api/jobs/${id}/cancel`, 200);
  }

  /** The tus creation request, for a test that expects it to be refused or accepted as is. */
  createUpload(jobId: string, name: string, size: number): Promise<Response> {
    const b64 = (s: string) => Buffer.from(s).toString("base64");
    return fetch(`${baseURL}/api/uploads/`, {
      method: "POST",
      headers: this.headers({
        "Tus-Resumable": "1.0.0",
        "Upload-Length": String(size),
        "Upload-Metadata": `jobId ${b64(jobId)},filename ${b64(name)}`,
      }),
    });
  }

  /** Create an upload for the file and send all of it in one PATCH. */
  async upload(jobId: string, file: string): Promise<void> {
    const data = await readFile(file);
    const created = await this.createUpload(jobId, path.basename(file), data.length);
    const detail = await created.text();
    if (created.status !== 201) throw new Error(`create upload: ${created.status} ${detail}`);
    const location = created.headers.get("Location");
    if (!location) throw new Error("create upload: no Location");
    // One request may carry at most upload.chunkSize (1 MiB in the test stack).
    for (let offset = 0; offset < data.length; offset += chunkSize) {
      const patch = await fetch(new URL(location, baseURL), {
        method: "PATCH",
        headers: this.headers({
          "Tus-Resumable": "1.0.0",
          "Content-Type": "application/offset+octet-stream",
          "Upload-Offset": String(offset),
        }),
        body: data.subarray(offset, offset + chunkSize),
      });
      if (patch.status !== 204) {
        throw new Error(`upload data at ${offset}: ${patch.status} ${await patch.text()}`);
      }
    }
  }

  /** Poll the job until it is no longer on its way somewhere (probed, failed, ...). */
  async settled(id: string, timeoutMs = 90_000): Promise<Job> {
    const deadline = Date.now() + timeoutMs;
    for (;;) {
      const job = await this.job(id);
      if (!moving.has(job.state)) return job;
      if (Date.now() > deadline) {
        throw new Error(`job ${id} still ${job.state} after ${timeoutMs} ms`);
      }
      await new Promise((r) => setTimeout(r, 250));
    }
  }

  /** create → upload into → submit → wait for the final state. More files go into one job. */
  async run(file: string, ...more: string[]): Promise<Job> {
    const { id } = await this.createJob();
    for (const f of [file, ...more]) await this.upload(id, f);
    await this.submit(id);
    return this.settled(id);
  }
}
