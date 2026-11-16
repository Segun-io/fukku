// The one place that talks tus. Each upload is created when it starts (never in advance), with
// the file name and the job as metadata (#10); the chunk size and endpoint come from the
// session. tus-js-client's own retries are off: the queue decides what a failure means.

import { Upload } from "tus-js-client";

export type TusFailure = { status: number; code: string | undefined };

export type TusOptions = {
  file: File;
  jobId: string;
  endpoint: string;
  chunkSize: number;
  /** A path on this origin of an upload created earlier: it continues from the server's offset. */
  uploadUrl?: string;
  onUrl: (path: string) => void;
  onProgress: (sent: number, total: number) => void;
  onDone: () => void;
  onError: (failure: TusFailure) => void;
};

export type TusHandle = {
  /** Stop sending. With `terminate`, also delete what the server has of it. */
  abort: (terminate: boolean) => void;
};

const absolute = (path: string) => new URL(path, window.location.origin).toString();

/** The path and query of a URL on this origin, so a stored upload survives a change of host. */
export function toPath(url: string): string {
  const u = new URL(url, window.location.origin);
  return u.pathname + u.search;
}

type TusError = {
  originalRequest?: { getURL(): string } | null;
  originalResponse?: {
    getStatus(): number;
    getBody(): string;
    getUnderlyingObject?(): unknown;
  } | null;
};

/**
 * Whether the answer came from another address than the one asked: the browser followed a
 * redirect, which for an upload request means the proxy in front of Fukku sent it to its
 * sign-in page. A sign-in page on this origin answers the redirected request itself (often
 * 404 or 405 to a PATCH), and that answer must not be taken for the upload's.
 */
function redirected(e: TusError): boolean {
  const asked = e.originalRequest?.getURL();
  const xhr = e.originalResponse?.getUnderlyingObject?.() as { responseURL?: unknown } | undefined;
  const answered = xhr?.responseURL;
  if (!asked || typeof answered !== "string" || answered === "") return false;
  return new URL(answered).href !== new URL(asked, window.location.origin).href;
}

export function failureOf(err: unknown): TusFailure {
  const e = (err ?? {}) as TusError;
  const res = e.originalResponse;
  if (!res) return { status: 0, code: undefined };
  if (redirected(e)) return { status: 0, code: "signed_out" };
  let code: string | undefined;
  try {
    const body: unknown = JSON.parse(res.getBody());
    const e = (body as { error?: { code?: unknown } } | null)?.error;
    if (typeof e?.code === "string") code = e.code;
  } catch {
    // Not the API's error shape (a proxy's page, for instance).
  }
  return { status: res.getStatus(), code };
}

export function startTus(o: TusOptions): TusHandle {
  const upload: Upload = new Upload(o.file, {
    // Continuing an upload: no endpoint. When the first request (HEAD) fails, tus-js-client
    // would otherwise create a new upload at once, for any answer but 423; the server then
    // refuses it as a second file of the same name. Without an endpoint the failure comes
    // here with the HEAD's answer, and the queue decides (a 404 starts over, a 5xx waits).
    endpoint: o.uploadUrl ? null : absolute(o.endpoint),
    uploadUrl: o.uploadUrl ? absolute(o.uploadUrl) : null,
    chunkSize: o.chunkSize,
    retryDelays: null,
    metadata: { filename: o.file.name, jobId: o.jobId },
    storeFingerprintForResuming: false,
    removeFingerprintOnSuccess: false,
    onUploadUrlAvailable: () => {
      if (upload.url) o.onUrl(toPath(upload.url));
    },
    onProgress: o.onProgress,
    onSuccess: () => o.onDone(),
    onError: (err) => o.onError(failureOf(err)),
  });
  upload.start();
  return {
    abort: (terminate) => {
      void upload.abort(terminate).catch(() => {});
    },
  };
}

/** Deletes an upload that no running tus upload owns (best effort: it expires on its own). */
export async function terminateUrl(path: string): Promise<void> {
  try {
    await fetch(absolute(path), {
      method: "DELETE",
      headers: { "Tus-Resumable": "1.0.0" },
      credentials: "same-origin",
      redirect: "manual",
    });
  } catch {
    // Nothing to do: the server removes idle uploads.
  }
}
