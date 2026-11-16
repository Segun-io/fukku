import { readFile } from "node:fs/promises";
import path from "node:path";
import { baseURL, type TestUser } from "./env";

// A client of the job API (#11) and a minimal tus client (#10) for tests that need no browser.
// It goes through the test proxy as one of its users (the cookie the proxy reads) and sends the
// Origin header a browser would, because every state change is checked against it.

export interface JobFile {
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

  /** create → upload into → submit → wait for the final state. */
  async run(file: string): Promise<Job> {
    const { id } = await this.createJob();
    await this.upload(id, file);
    await this.submit(id);
    return this.settled(id);
  }
}
