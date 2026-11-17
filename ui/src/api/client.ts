// The one way to call the Fukku API: same origin, JSON, errors as `ApiError`.
// The reverse proxy in front of Fukku handles sign-in, so a 401 or a redirect (to the proxy's
// sign-in page, possibly on another origin) means the session ended: code `signed_out`.

import type { ApiErrorBody } from "./types";

export class ApiError extends Error {
  readonly status: number;
  readonly code: string;
  /** The rows a refusal is about (`needs_override`, `shared_file`...); empty for the others. */
  readonly rows: string[];

  constructor(status: number, code: string, message: string, rows: string[] = []) {
    super(message);
    this.rows = rows;
    this.name = "ApiError";
    this.status = status;
    this.code = code;
  }
}

function isErrorBody(body: unknown): body is ApiErrorBody {
  if (typeof body !== "object" || body === null || !("error" in body)) return false;
  const e = body.error;
  return (
    typeof e === "object" &&
    e !== null &&
    "code" in e &&
    typeof e.code === "string" &&
    "message" in e &&
    typeof e.message === "string"
  );
}

async function readJson(res: Response): Promise<unknown> {
  const text = await res.text();
  if (text === "") return undefined;
  try {
    return JSON.parse(text);
  } catch {
    return undefined;
  }
}

/** True for an answer a retry will not change (4xx, signed out), false for outages. */
export function isPermanent(err: unknown): boolean {
  return err instanceof ApiError && err.status >= 300 && err.status < 500;
}

/**
 * Calls `path` (an absolute path on this origin) and returns the parsed JSON, or `undefined`
 * for an empty answer (204). Anything but a 2xx answer throws an `ApiError`.
 */
export async function apiFetch<T>(path: string, init: RequestInit = {}): Promise<T> {
  if (!path.startsWith("/") || path.startsWith("//")) {
    throw new Error(`apiFetch needs a path on this origin, got ${path}`);
  }
  const headers = new Headers(init.headers);
  if (!headers.has("Accept")) headers.set("Accept", "application/json");
  if (typeof init.body === "string" && !headers.has("Content-Type")) {
    headers.set("Content-Type", "application/json");
  }

  let res: Response;
  try {
    res = await fetch(new URL(path, window.location.origin), {
      ...init,
      headers,
      credentials: "same-origin",
      redirect: "manual",
    });
  } catch (err) {
    if (init.signal?.aborted) throw err;
    throw new ApiError(0, "network", "The server could not be reached.");
  }

  // With redirect "manual" a browser reports any redirect as an opaque response (status 0);
  // other runtimes give the 3xx itself. Either way the target is not ours to follow.
  if (res.type === "opaqueredirect" || (res.status >= 300 && res.status < 400)) {
    throw new ApiError(res.status, "signed_out", "Sign in again.");
  }
  if (res.status === 401) {
    throw new ApiError(401, "signed_out", "Sign in again.");
  }

  const body = await readJson(res);
  if (!res.ok) {
    if (isErrorBody(body)) {
      const rows = (body.error as { rows?: unknown }).rows;
      throw new ApiError(
        res.status,
        body.error.code,
        body.error.message,
        Array.isArray(rows) ? rows.filter((r): r is string => typeof r === "string") : [],
      );
    }
    throw new ApiError(res.status, "internal", `The server answered ${res.status}.`);
  }
  if (res.status !== 204 && body === undefined) {
    throw new ApiError(res.status, "bad_response", "The server sent an answer that is not JSON.");
  }
  return body as T;
}
