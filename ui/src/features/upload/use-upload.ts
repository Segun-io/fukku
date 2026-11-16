// Runs the effects around the queue: creating the job, one tus upload per file when its turn
// comes, the retry timers, the session check after a failed request, the connection, the
// sign-in tab, storing the job for a reload, and submitting the job when every file is on the
// server. All decisions are in `queue.ts`.

import { useCallback, useEffect, useReducer, useRef, useState } from "react";
import { ApiError } from "@/api/client";
import { cancelJob, createJob, getJob, submitJob } from "@/api/jobs";
import { getSession } from "@/api/session";
import type { Session } from "@/api/types";
import {
  allDone,
  classify,
  hasUnfinished,
  initialState,
  type QueueState,
  reduce,
  toStart,
} from "./queue";
import { clearJob, loadJob, saveJob } from "./storage";
import { startTus, type TusFailure, terminateUrl } from "./tus";

type Running = { abort: (terminate: boolean) => void; alive: boolean };

async function sessionEnded(): Promise<boolean> {
  try {
    await getSession();
    return false;
  } catch (err) {
    return err instanceof ApiError && err.code === "signed_out";
  }
}

function codeOf(err: unknown): string {
  return err instanceof ApiError ? err.code : "internal";
}

export type UseUpload = ReturnType<typeof useUpload>;

export function useUpload(config: Session["upload"], onSubmitted: (jobId: string) => void) {
  const [state, dispatch] = useReducer(reduce, undefined, () =>
    initialState(config, navigator.onLine),
  );
  const [restoring, setRestoring] = useState(() => loadJob() !== null);
  const [creating, setCreating] = useState(false);
  const [startError, setStartError] = useState<Error | null>(null);

  const stateRef = useRef<QueueState>(state);
  stateRef.current = state;
  const files = useRef(new Map<string, File>());
  const running = useRef(new Map<string, Running>());
  const configRef = useRef(config);
  configRef.current = config;
  const submitted = useRef(onSubmitted);
  submitted.current = onSubmitted;
  const stored = useRef("");

  // After a reload: the job that was being uploaded, asked of the server.
  useEffect(() => {
    const job = loadJob();
    if (!job) {
      setRestoring(false);
      return;
    }
    const ctl = new AbortController();
    getJob(job.jobId, ctl.signal)
      .then((detail) => {
        if (detail.state !== "uploading") {
          clearJob();
          return;
        }
        const onServer = detail.files.map((f) => f.name);
        if (job.files.length === 0 && onServer.length === 0) {
          clearJob();
          return;
        }
        dispatch({ type: "restore", jobId: job.jobId, files: job.files, onServer });
      })
      .catch((err: unknown) => {
        if (ctl.signal.aborted) return;
        if (err instanceof ApiError && err.status === 404) {
          clearJob();
          return;
        }
        // The server could not be asked: keep the job, each file will say where it stands.
        dispatch({ type: "restore", jobId: job.jobId, files: job.files, onServer: [] });
        if (err instanceof ApiError && err.code === "signed_out") dispatch({ type: "signedOut" });
      })
      .finally(() => {
        if (!ctl.signal.aborted) setRestoring(false);
      });
    return () => ctl.abort();
  }, []);

  // Keep the job for a reload, when what identifies it changed.
  useEffect(() => {
    if (restoring) return;
    const sig = JSON.stringify([
      state.phase,
      state.jobId,
      state.jobError,
      state.entries.map((e) => [e.name, e.size, e.lastModified, e.uploadUrl]),
    ]);
    if (sig === stored.current) return;
    stored.current = sig;
    saveJob(state);
  }, [state, restoring]);

  const failed = useCallback((key: string, run: Running, failure: TusFailure) => {
    running.current.delete(key);
    void (async () => {
      let kind = classify(failure.status, failure.code);
      if (kind === "network" && (await sessionEnded())) kind = "auth";
      if (!run.alive) return;
      dispatch({
        type: "error",
        key,
        kind,
        code: failure.code ?? (failure.status === 0 ? "network" : "internal"),
        now: Date.now(),
      });
    })();
  }, []);

  // Files that stopped being `uploading` (paused, removed, signed out) stop sending. This
  // effect comes before the one that starts files, so a file just started is not stopped.
  useEffect(() => {
    for (const [key, run] of running.current) {
      const entry = state.entries.find((e) => e.key === key);
      if (entry?.status !== "uploading") {
        run.alive = false;
        run.abort(false);
        running.current.delete(key);
      }
    }
  }, [state.entries]);

  // Start the next files while there are free slots.
  useEffect(() => {
    const jobId = state.jobId;
    if (jobId === null) return;
    for (const key of toStart(state)) {
      const file = files.current.get(key);
      const entry = state.entries.find((e) => e.key === key);
      if (!file || !entry || running.current.has(key)) continue;
      dispatch({ type: "start", key, now: Date.now() });
      const run: Running = { abort: () => {}, alive: true };
      running.current.set(key, run);
      const handle = startTus({
        file,
        jobId,
        endpoint: configRef.current.endpoint,
        chunkSize: configRef.current.chunkSize,
        uploadUrl: entry.uploadUrl,
        onUrl: (url) => run.alive && dispatch({ type: "uploadUrl", key, url }),
        onProgress: (sent) =>
          run.alive && dispatch({ type: "progress", key, sent, now: Date.now() }),
        onDone: () => {
          if (!run.alive) return;
          running.current.delete(key);
          dispatch({ type: "done", key });
        },
        onError: (failure) => run.alive && failed(key, run, failure),
      });
      run.abort = handle.abort;
    }
  }, [state, failed]);

  // A file waiting out its backoff starts again when the time comes.
  useEffect(() => {
    const timers = state.entries.flatMap((e) =>
      e.status === "reconnecting" && e.retryAt !== undefined
        ? [
            setTimeout(
              () => dispatch({ type: "retryDue", key: e.key, now: Date.now() }),
              Math.max(0, e.retryAt - Date.now()),
            ),
          ]
        : [],
    );
    return () => {
      for (const t of timers) clearTimeout(t);
    };
  }, [state.entries]);

  // The connection.
  useEffect(() => {
    const on = () => dispatch({ type: "online", online: true });
    const off = () => dispatch({ type: "online", online: false });
    window.addEventListener("online", on);
    window.addEventListener("offline", off);
    return () => {
      window.removeEventListener("online", on);
      window.removeEventListener("offline", off);
    };
  }, []);

  // Signed out: sign-in happens in another tab. When this one is looked at again, ask the
  // session; once it answers, everything continues.
  const signedOut = state.signedOut;
  useEffect(() => {
    if (!signedOut) return;
    let busy = false;
    const ask = () => {
      if (document.visibilityState === "hidden" || busy) return;
      busy = true;
      getSession()
        .then(() => dispatch({ type: "signedIn" }))
        .catch(() => {})
        .finally(() => {
          busy = false;
        });
    };
    document.addEventListener("visibilitychange", ask);
    window.addEventListener("focus", ask);
    return () => {
      document.removeEventListener("visibilitychange", ask);
      window.removeEventListener("focus", ask);
    };
  }, [signedOut]);

  // Leaving the page would stop the uploads.
  const unfinished = hasUnfinished(state);
  useEffect(() => {
    if (!unfinished) return;
    const warn = (e: BeforeUnloadEvent) => {
      e.preventDefault();
      e.returnValue = "";
    };
    window.addEventListener("beforeunload", warn);
    return () => window.removeEventListener("beforeunload", warn);
  }, [unfinished]);

  const submit = useCallback(async () => {
    const jobId = stateRef.current.jobId;
    if (jobId === null) return;
    dispatch({ type: "submitting" });
    try {
      await submitJob(jobId);
    } catch (err) {
      if (err instanceof ApiError && err.code === "signed_out") dispatch({ type: "signedOut" });
      dispatch({ type: "submitFailed", code: codeOf(err) });
      return;
    }
    clearJob();
    dispatch({ type: "submitted" });
    submitted.current(jobId);
  }, []);

  // Every file is on the server: send the job on.
  const ready = allDone(state);
  useEffect(() => {
    if (ready) void submit();
  }, [ready, submit]);

  const addFiles = useCallback((list: File[]) => {
    const infos = list.map((file) => {
      const key = crypto.randomUUID();
      files.current.set(key, file);
      return { key, name: file.name, size: file.size, lastModified: file.lastModified };
    });
    dispatch({ type: "add", files: infos });
  }, []);

  const start = useCallback(async () => {
    setCreating(true);
    setStartError(null);
    try {
      const job = await createJob();
      dispatch({ type: "jobCreated", jobId: job.id });
    } catch (err) {
      if (err instanceof ApiError && err.code === "signed_out") dispatch({ type: "signedOut" });
      setStartError(err instanceof Error ? err : new Error("internal"));
    } finally {
      setCreating(false);
    }
  }, []);

  const remove = useCallback((key: string) => {
    const entry = stateRef.current.entries.find((e) => e.key === key);
    const run = running.current.get(key);
    if (run) {
      run.alive = false;
      run.abort(true);
      running.current.delete(key);
    } else if (entry?.uploadUrl && entry.status !== "done") {
      void terminateUrl(entry.uploadUrl);
    }
    files.current.delete(key);
    dispatch({ type: "remove", key });
  }, []);

  const discard = useCallback(async () => {
    const { jobId } = stateRef.current;
    for (const run of running.current.values()) {
      run.alive = false;
      run.abort(true);
    }
    running.current.clear();
    files.current.clear();
    clearJob();
    stored.current = "";
    dispatch({ type: "reset" });
    if (jobId !== null) {
      try {
        await cancelJob(jobId);
      } catch {
        // The job is already gone, or expires by itself.
      }
    }
  }, []);

  return {
    state,
    restoring,
    creating,
    startError,
    addFiles,
    start,
    discard,
    submit,
    remove,
    pause: (key: string) => dispatch({ type: "pause", key }),
    resume: (key: string) => dispatch({ type: "resume", key }),
    retry: (key: string) => dispatch({ type: "retry", key }),
    dismissRejected: () => dispatch({ type: "dismissRejected" }),
  };
}
