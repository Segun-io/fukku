// MSW handlers for the tus endpoint of #10: create (POST), HEAD, PATCH, DELETE and the refusals.
// The bytes are not kept, only how many arrived. In `npm run dev:mock` and in tests, a file name
// with a code in square brackets is refused at creation with that code, e.g.
// `Film [insufficient_space].mkv`, so each refusal can be seen without a server. A name with
// `[signed_out]` is answered 401 once, a third of the way in: the page shows "sign in again",
// and continues when the tab is looked at again.

import { delay, HttpResponse, http } from "msw";
import type { Job, JobFile, JobUpload } from "@/api/types";

type MockUpload = {
  id: string;
  jobId: string;
  name: string;
  size: number;
  offset: number;
};

let uploads: MockUpload[] = [];
/** Uploads that already got their one 401 (`[signed_out]` in the name). */
let signedOutOnce = new Set<string>();

export function resetUploads() {
  uploads = [];
  signedOutOnce = new Set();
}

/** The uploads of a job, for a test to look at. */
export function uploadsOf(jobId: string): readonly MockUpload[] {
  return uploads.filter((u) => u.jobId === jobId);
}

/** The unfinished uploads of a job, as `GET /api/jobs/{id}` lists them. */
export function pendingOf(jobId: string): JobUpload[] {
  return uploads
    .filter((u) => u.jobId === jobId && u.offset < u.size)
    .map((u) => ({ id: u.id, name: u.name, size: u.size, offset: u.offset }));
}

/** The finished files of a job, as `GET /api/jobs/{id}` lists them. */
export function filesOf(jobId: string): JobFile[] {
  return uploads
    .filter((u) => u.jobId === jobId && u.offset === u.size)
    .map((u, i) => ({
      id: i + 1,
      path: `files/${u.name}`,
      name: u.name,
      kind: u.name.toLowerCase().endsWith(".zip") ? "archive" : "video",
      size: u.size,
      status: "pending",
      reason: null,
      from: null,
      probe: null,
      extraction: null,
    }));
}

const statusOf: Record<string, number> = {
  bad_name: 400,
  extension_not_allowed: 415,
  file_too_large: 413,
  duplicate_name: 409,
  job_not_uploading: 409,
  too_many_uploads: 429,
  in_flight_limit: 507,
  insufficient_space: 507,
  storage_unavailable: 503,
  internal: 500,
};

const tusHeaders = { "Tus-Resumable": "1.0.0", "Cache-Control": "no-store" };

function failure(status: number, code: string, message: string) {
  return HttpResponse.json(
    { error: { code, message } },
    { status, headers: { "Cache-Control": "no-store" } },
  );
}

function decodeMetadata(header: string | null): Record<string, string> {
  const out: Record<string, string> = {};
  for (const pair of (header ?? "").split(",")) {
    const [key, value] = pair.trim().split(" ");
    if (key && value)
      out[key] = new TextDecoder().decode(Uint8Array.from(atob(value), (c) => c.charCodeAt(0)));
  }
  return out;
}

/** `find` looks a job up by id in the mock API's job list. */
export function uploadHandlers(find: (id: string) => Job | undefined) {
  const slow = import.meta.env.MODE === "mock";
  const base = "/api/uploads/";

  return [
    http.post(base, ({ request }) => {
      const meta = decodeMetadata(request.headers.get("Upload-Metadata"));
      const job = find(meta.jobId ?? "");
      if (!job) return failure(404, "not_found", "No such job.");
      if (job.state !== "uploading") {
        return failure(409, "job_not_uploading", "The job takes no more uploads.");
      }
      const name = meta.filename ?? "";
      const size = Number(request.headers.get("Upload-Length"));
      if (!Number.isFinite(size) || size < 0) {
        return failure(400, "bad_request", "Upload-Length is missing or invalid.");
      }
      const refusal = /\[(\w+)\]/.exec(name)?.[1];
      if (refusal && statusOf[refusal]) {
        return failure(statusOf[refusal], refusal, "Refused by the mock server.");
      }
      if (uploads.some((u) => u.jobId === job.id && u.name.toLowerCase() === name.toLowerCase())) {
        return failure(409, "duplicate_name", "The job already has this file.");
      }
      const upload: MockUpload = {
        id: crypto.randomUUID().replaceAll("-", ""),
        jobId: job.id,
        name,
        size,
        offset: 0,
      };
      uploads.push(upload);
      return new HttpResponse(null, {
        status: 201,
        headers: { ...tusHeaders, Location: `${base}${upload.id}` },
      });
    }),

    http.head(`${base}:id`, ({ params }) => {
      const upload = uploads.find((u) => u.id === params.id);
      if (!upload) return new HttpResponse(null, { status: 404, headers: tusHeaders });
      return new HttpResponse(null, {
        status: 200,
        headers: {
          ...tusHeaders,
          "Upload-Offset": String(upload.offset),
          "Upload-Length": String(upload.size),
        },
      });
    }),

    http.patch(`${base}:id`, async ({ params, request }) => {
      const upload = uploads.find((u) => u.id === params.id);
      if (!upload) return failure(404, "not_found", "No such upload.");
      const job = find(upload.jobId);
      if (job?.state !== "uploading") {
        return failure(409, "job_not_uploading", "The job takes no more uploads.");
      }
      if (Number(request.headers.get("Upload-Offset")) !== upload.offset) {
        return failure(409, "offset_mismatch", "Upload-Offset does not match; send HEAD first.");
      }
      const endsSession = upload.name.includes("[signed_out]") && upload.offset >= upload.size / 3;
      if (endsSession && !signedOutOnce.has(upload.id)) {
        signedOutOnce.add(upload.id);
        return failure(401, "signed_out", "The session ended.");
      }
      const chunk = (await request.arrayBuffer()).byteLength;
      if (slow) await delay(400);
      upload.offset = Math.min(upload.size, upload.offset + chunk);
      if (upload.offset === upload.size) {
        job.fileCount += 1;
        job.totalSize += upload.size;
        job.name ??= upload.name;
      }
      return new HttpResponse(null, {
        status: 204,
        headers: { ...tusHeaders, "Upload-Offset": String(upload.offset) },
      });
    }),

    http.delete(`${base}:id`, ({ params }) => {
      const upload = uploads.find((u) => u.id === params.id);
      if (!upload) return failure(404, "not_found", "No such upload.");
      uploads = uploads.filter((u) => u !== upload);
      return new HttpResponse(null, { status: 204, headers: tusHeaders });
    }),
  ];
}
