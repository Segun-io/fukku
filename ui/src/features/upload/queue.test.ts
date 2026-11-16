import { describe, expect, it } from "vitest";
import { session } from "@/mocks/fixtures";
import {
  type Action,
  allDone,
  backoffMs,
  checkFile,
  classify,
  displayStatus,
  type FileInfo,
  initialState,
  maxServerAttempts,
  type QueueState,
  type Rules,
  reduce,
  secondsLeft,
  showLargeHint,
  toStart,
} from "./queue";
import { clearJob, loadJob, saveJob, storageKey } from "./storage";

const rules: Rules = { ...session.upload, maxParallelPerUser: 2 };

const file = (name: string, size = 1000, lastModified = 1_700_000_000_000): FileInfo => ({
  key: `k-${name}`,
  name,
  size,
  lastModified,
});

function run(state: QueueState, ...actions: Action[]): QueueState {
  return actions.reduce(reduce, state);
}

/** A job with the named files queued, as after "Start upload". */
function uploading(names: string[], r: Rules = rules): QueueState {
  return run(
    initialState(r),
    { type: "add", files: names.map((n) => file(n)) },
    { type: "jobCreated", jobId: "job1" },
  );
}

const entry = (s: QueueState, name: string) => {
  const e = s.entries.find((x) => x.name === name);
  if (!e) throw new Error(`no entry ${name}`);
  return e;
};

describe("checks before anything is sent", () => {
  it("accepts video, subtitle and archive extensions, in any case", () => {
    for (const name of ["a.mkv", "a.MP4", "a.srt", "a.ASS", "a.zip"]) {
      expect(checkFile({ name, size: 1 }, rules, []), name).toBeNull();
    }
  });

  it("refuses an extension that is not in the lists", () => {
    expect(checkFile({ name: "a.exe", size: 1 }, rules, [])).toBe("extension_not_allowed");
    expect(checkFile({ name: "mkv", size: 1 }, rules, [])).toBe("extension_not_allowed");
  });

  it("refuses a file above maxFileSize", () => {
    expect(checkFile({ name: "a.mkv", size: rules.maxFileSize }, rules, [])).toBeNull();
    expect(checkFile({ name: "a.mkv", size: rules.maxFileSize + 1 }, rules, [])).toBe(
      "file_too_large",
    );
  });

  it("refuses a name that is already in the job, ignoring case", () => {
    expect(checkFile({ name: "A.mkv", size: 1 }, rules, ["a.MKV"])).toBe("duplicate_name");
  });

  it("refuses names the server would refuse", () => {
    expect(checkFile({ name: ".hidden.mkv", size: 1 }, rules, [])).toBe("bad_name");
    expect(checkFile({ name: "a/b.mkv", size: 1 }, rules, [])).toBe("bad_name");
  });

  it("add keeps the good files and lists the refused ones with their code", () => {
    const s = run(initialState(rules), {
      type: "add",
      files: [file("a.mkv"), file("a.MKV"), file("notes.txt"), file("b.srt")],
    });
    expect(s.entries.map((e) => e.name)).toEqual(["a.mkv", "b.srt"]);
    expect(s.rejected).toEqual([
      { name: "a.MKV", code: "duplicate_name" },
      { name: "notes.txt", code: "extension_not_allowed" },
    ]);
    expect(run(s, { type: "dismissRejected" }).rejected).toEqual([]);
  });

  it("shows the large-file hint above the threshold, and never when it is 0", () => {
    const big = { ...rules, largeFileHintThreshold: 500 };
    const s = run(initialState(big), { type: "add", files: [file("a.mkv", 1000)] });
    expect(showLargeHint(s)).toBe(true);
    expect(showLargeHint({ ...s, rules: { ...big, largeFileHintThreshold: 1000 } })).toBe(false);
    expect(showLargeHint({ ...s, rules: { ...big, largeFileHintThreshold: 0 } })).toBe(false);
  });
});

describe("the parallel limit", () => {
  it("nothing starts before the job exists", () => {
    const s = run(initialState(rules), { type: "add", files: [file("a.mkv")] });
    expect(toStart(s)).toEqual([]);
  });

  it("starts at most maxParallelPerUser files, the rest wait in order", () => {
    let s = uploading(["a.mkv", "b.mkv", "c.mkv", "d.mkv"]);
    expect(toStart(s)).toEqual(["k-a.mkv", "k-b.mkv"]);
    s = run(
      s,
      { type: "start", key: "k-a.mkv", now: 0 },
      { type: "start", key: "k-b.mkv", now: 0 },
    );
    expect(toStart(s)).toEqual([]);
    expect(entry(s, "c.mkv").status).toBe("queued");

    s = run(s, { type: "done", key: "k-a.mkv" });
    expect(toStart(s)).toEqual(["k-c.mkv"]);
  });

  it("a file waiting out its backoff does not hold a slot", () => {
    let s = uploading(["a.mkv", "b.mkv", "c.mkv"]);
    s = run(
      s,
      { type: "start", key: "k-a.mkv", now: 0 },
      { type: "start", key: "k-b.mkv", now: 0 },
      { type: "error", key: "k-a.mkv", kind: "network", code: "network", now: 0 },
    );
    expect(toStart(s)).toEqual(["k-c.mkv"]);
  });

  it("starts one at a time if the server's limit is 1 or less", () => {
    const s = uploading(["a.mkv", "b.mkv"], { ...rules, maxParallelPerUser: 0 });
    expect(toStart(s)).toEqual(["k-a.mkv"]);
  });
});

describe("pause and resume", () => {
  it("a paused file leaves its slot and is not started again until resumed", () => {
    let s = uploading(["a.mkv", "b.mkv", "c.mkv"]);
    s = run(
      s,
      { type: "start", key: "k-a.mkv", now: 0 },
      { type: "start", key: "k-b.mkv", now: 0 },
      { type: "pause", key: "k-a.mkv" },
    );
    expect(entry(s, "a.mkv").status).toBe("paused");
    expect(toStart(s)).toEqual(["k-c.mkv"]);

    s = run(s, { type: "start", key: "k-c.mkv", now: 0 });
    s = run(s, { type: "done", key: "k-b.mkv" }, { type: "resume", key: "k-a.mkv" });
    expect(entry(s, "a.mkv").status).toBe("queued");
    expect(toStart(s)).toEqual(["k-a.mkv"]);
  });

  it("resume only acts on a paused file, retry only on a failed one", () => {
    const s = uploading(["a.mkv"]);
    expect(run(s, { type: "resume", key: "k-a.mkv" })).toEqual(s);
    const s2 = run(s, { type: "retry", key: "k-a.mkv" });
    expect(entry(s2, "a.mkv").status).toBe("queued");
  });

  it("remove drops the file and frees its place", () => {
    const s = run(uploading(["a.mkv", "b.mkv"]), { type: "remove", key: "k-a.mkv" });
    expect(s.entries.map((e) => e.name)).toEqual(["b.mkv"]);
  });
});

describe("progress", () => {
  it("the first figure after a start is a baseline, then speed and time left follow", () => {
    let s = uploading(["a.mkv"]);
    s = run(
      s,
      { type: "start", key: "k-a.mkv", now: 0 },
      { type: "progress", key: "k-a.mkv", sent: 400, now: 100 },
    );
    let e = entry(s, "a.mkv");
    expect(e.speed).toBeUndefined();
    expect(e.resumedFrom).toBe(400);

    s = run(s, { type: "progress", key: "k-a.mkv", sent: 500, now: 1100 });
    e = entry(s, "a.mkv");
    expect(e.speed).toBe(100);
    expect(secondsLeft(e)).toBe(5);
  });

  it("a file that starts from zero is not reported as resumed", () => {
    const s = run(
      uploading(["a.mkv"]),
      { type: "start", key: "k-a.mkv", now: 0 },
      { type: "progress", key: "k-a.mkv", sent: 0, now: 10 },
    );
    expect(entry(s, "a.mkv").resumedFrom).toBeUndefined();
  });
});

describe("backoff and reconnecting", () => {
  it("doubles from one second up to thirty", () => {
    expect([1, 2, 3, 4, 5, 6, 7, 20].map((n) => backoffMs(n))).toEqual([
      1000, 2000, 4000, 8000, 16000, 30000, 30000, 30000,
    ]);
    expect(backoffMs(1, "busy")).toBe(500);
    expect(backoffMs(50, "busy")).toBe(5000);
  });

  it("a network failure waits for the backoff, then the file is queued again", () => {
    let s = run(
      uploading(["a.mkv"]),
      { type: "start", key: "k-a.mkv", now: 0 },
      { type: "error", key: "k-a.mkv", kind: "network", code: "network", now: 10_000 },
    );
    let e = entry(s, "a.mkv");
    expect(e.status).toBe("reconnecting");
    expect(e.retryAt).toBe(11_000);
    expect(toStart(s)).toEqual([]);

    s = run(s, { type: "retryDue", key: "k-a.mkv", now: 10_500 });
    expect(entry(s, "a.mkv").status).toBe("reconnecting");
    s = run(s, { type: "retryDue", key: "k-a.mkv", now: 11_000 });
    expect(entry(s, "a.mkv").status).toBe("queued");

    // The next failure in a row waits twice as long.
    s = run(
      s,
      { type: "start", key: "k-a.mkv", now: 11_000 },
      { type: "error", key: "k-a.mkv", kind: "network", code: "network", now: 12_000 },
    );
    e = entry(s, "a.mkv");
    expect(e.attempt).toBe(2);
    expect(e.retryAt).toBe(14_000);
  });

  it("progress resets the count of failures in a row", () => {
    let s = run(
      uploading(["a.mkv"]),
      { type: "start", key: "k-a.mkv", now: 0 },
      { type: "error", key: "k-a.mkv", kind: "network", code: "network", now: 0 },
      { type: "retryDue", key: "k-a.mkv", now: 1000 },
      { type: "start", key: "k-a.mkv", now: 1000 },
      { type: "progress", key: "k-a.mkv", sent: 0, now: 1100 },
      { type: "progress", key: "k-a.mkv", sent: 100, now: 1200 },
    );
    expect(entry(s, "a.mkv").attempt).toBe(0);
    s = run(s, { type: "error", key: "k-a.mkv", kind: "network", code: "network", now: 1300 });
    expect(entry(s, "a.mkv").retryAt).toBe(2300);
  });

  it("when the connection comes back, waiting files do not wait out the backoff", () => {
    let s = run(
      uploading(["a.mkv"]),
      { type: "start", key: "k-a.mkv", now: 0 },
      { type: "error", key: "k-a.mkv", kind: "network", code: "network", now: 0 },
      { type: "online", online: false },
    );
    expect(toStart(s)).toEqual([]);
    s = run(s, { type: "online", online: true });
    expect(entry(s, "a.mkv").status).toBe("queued");
    expect(toStart(s)).toEqual(["k-a.mkv"]);
  });

  it("nothing starts while the browser is offline", () => {
    const s = run(uploading(["a.mkv"]), { type: "online", online: false });
    expect(toStart(s)).toEqual([]);
  });

  it("a 5xx is retried a few times, then the file fails", () => {
    let s = run(uploading(["a.mkv"]), { type: "start", key: "k-a.mkv", now: 0 });
    for (let i = 0; i < maxServerAttempts; i++) {
      s = run(s, { type: "error", key: "k-a.mkv", kind: "server", code: "internal", now: 0 });
      expect(entry(s, "a.mkv").status).toBe("reconnecting");
      s = run(
        s,
        { type: "retryDue", key: "k-a.mkv", now: 1e9 },
        { type: "start", key: "k-a.mkv", now: 1e9 },
      );
    }
    s = run(s, { type: "error", key: "k-a.mkv", kind: "server", code: "internal", now: 0 });
    expect(entry(s, "a.mkv")).toMatchObject({ status: "failed", code: "internal" });
  });
});

describe("signed out", () => {
  it("pauses everything, starts nothing, and continues when the session is back", () => {
    let s = uploading(["a.mkv", "b.mkv", "c.mkv"]);
    s = run(
      s,
      { type: "start", key: "k-a.mkv", now: 0 },
      { type: "start", key: "k-b.mkv", now: 0 },
      { type: "error", key: "k-a.mkv", kind: "auth", code: "signed_out", now: 0 },
    );
    expect(s.signedOut).toBe(true);
    expect(s.entries.map((e) => e.status)).toEqual(["queued", "queued", "queued"]);
    expect(toStart(s)).toEqual([]);

    s = run(s, { type: "signedIn" });
    expect(s.signedOut).toBe(false);
    expect(toStart(s)).toEqual(["k-a.mkv", "k-b.mkv"]);
  });

  it("keeps what the user paused paused", () => {
    const s = run(
      uploading(["a.mkv", "b.mkv"]),
      { type: "pause", key: "k-a.mkv" },
      { type: "signedOut" },
      { type: "signedIn" },
    );
    expect(entry(s, "a.mkv").status).toBe("paused");
    expect(toStart(s)).toEqual(["k-b.mkv"]);
  });
});

describe("rows while signed out", () => {
  it("queued and moving files show as paused by the sign-out, the rest as they are", () => {
    let s = uploading(["a.mkv", "b.mkv", "c.mkv", "d.mkv"]);
    s = run(
      s,
      { type: "start", key: "k-a.mkv", now: 0 },
      { type: "done", key: "k-b.mkv" },
      { type: "pause", key: "k-c.mkv" },
    );
    const show = (name: string) => displayStatus(s, entry(s, name));
    expect(show("a.mkv")).toEqual({ status: "uploading", pausedBySignOut: false });
    expect(show("d.mkv")).toEqual({ status: "queued", pausedBySignOut: false });

    s = run(s, { type: "signedOut" });
    expect(show("a.mkv")).toEqual({ status: "paused", pausedBySignOut: true });
    expect(show("d.mkv")).toEqual({ status: "paused", pausedBySignOut: true });
    expect(show("b.mkv")).toEqual({ status: "done", pausedBySignOut: false });
    expect(show("c.mkv")).toEqual({ status: "paused", pausedBySignOut: false });

    s = run(s, { type: "signedIn" });
    expect(show("d.mkv")).toEqual({ status: "queued", pausedBySignOut: false });
  });
});

describe("what each error code means", () => {
  const table: [number, string | undefined, ReturnType<typeof classify>][] = [
    [0, undefined, "network"],
    [200, undefined, "network"],
    [401, undefined, "auth"],
    [403, "forbidden", "refused"],
    [400, "bad_name", "refused"],
    [400, "bad_request", "refused"],
    [415, "extension_not_allowed", "refused"],
    [413, "file_too_large", "refused"],
    [413, "too_large", "refused"],
    [409, "duplicate_name", "refused"],
    [409, "job_not_uploading", "refused"],
    [507, "in_flight_limit", "refused"],
    [507, "insufficient_space", "refused"],
    [429, "too_many_uploads", "busy"],
    [409, "offset_mismatch", "busy"],
    [423, "upload_busy", "busy"],
    [503, "storage_unavailable", "server"],
    [500, "internal", "server"],
    [502, undefined, "server"],
    [404, "not_found", "gone"],
  ];

  it.each(table)("%i %s is %s", (status, code, kind) => {
    expect(classify(status, code)).toBe(kind);
  });

  it.each(table.filter(([, , kind]) => kind === "refused"))(
    "a refused file (%i %s) fails with its code and the others go on",
    (_status, code) => {
      const s = run(
        uploading(["a.mkv", "b.mkv"]),
        { type: "start", key: "k-a.mkv", now: 0 },
        { type: "error", key: "k-a.mkv", kind: "refused", code: code ?? "", now: 0 },
      );
      if (code === "job_not_uploading") {
        // The job itself is closed: nothing can continue.
        expect(s.jobError).toBe(code);
        expect(toStart(s)).toEqual([]);
        return;
      }
      expect(entry(s, "a.mkv")).toMatchObject({ status: "failed", code });
      expect(toStart(s)).toEqual(["k-b.mkv"]);
      // Try again puts it back in line.
      expect(entry(run(s, { type: "retry", key: "k-a.mkv" }), "a.mkv").status).toBe("queued");
    },
  );

  it("a busy answer (offset_mismatch, upload_busy) is retried soon", () => {
    const s = run(
      uploading(["a.mkv"]),
      { type: "start", key: "k-a.mkv", now: 0 },
      { type: "error", key: "k-a.mkv", kind: "busy", code: "offset_mismatch", now: 0 },
    );
    expect(entry(s, "a.mkv")).toMatchObject({ status: "reconnecting", retryAt: 500 });
  });

  it("an upload that is gone starts again from zero; a missing job stops everything", () => {
    let s = run(uploading(["a.mkv", "b.mkv"]), {
      type: "uploadUrl",
      key: "k-a.mkv",
      url: "/api/uploads/x",
    });
    s = run(
      s,
      { type: "start", key: "k-a.mkv", now: 0 },
      { type: "error", key: "k-a.mkv", kind: "gone", code: "not_found", now: 0 },
    );
    expect(entry(s, "a.mkv")).toMatchObject({ status: "queued", uploadUrl: undefined, sent: 0 });

    s = run(
      s,
      { type: "start", key: "k-a.mkv", now: 0 },
      { type: "error", key: "k-a.mkv", kind: "gone", code: "not_found", now: 0 },
    );
    expect(s.jobError).toBe("not_found");
    expect(toStart(s)).toEqual([]);
  });
});

describe("submitting", () => {
  it("is ready when every file is done, and not before", () => {
    let s = uploading(["a.mkv", "b.mkv"]);
    expect(allDone(s)).toBe(false);
    s = run(s, { type: "done", key: "k-a.mkv" });
    expect(allDone(s)).toBe(false);
    s = run(s, { type: "done", key: "k-b.mkv" });
    expect(allDone(s)).toBe(true);
    s = run(s, { type: "submitting" });
    expect(allDone(s)).toBe(false);
    expect(run(s, { type: "submitFailed", code: "internal" }).submitError).toBe("internal");
  });

  it("is not ready with no files, or with a failed one", () => {
    expect(allDone(uploading([]))).toBe(false);
    const s = run(uploading(["a.mkv"]), {
      type: "error",
      key: "k-a.mkv",
      kind: "refused",
      code: "insufficient_space",
      now: 0,
    });
    expect(allDone(s)).toBe(false);
  });
});

describe("resume after a reload", () => {
  const stored = [
    { name: "a.mkv", size: 1000, lastModified: 1_700_000_000_000, uploadUrl: "/api/uploads/u1" },
    { name: "b.mkv", size: 2000, lastModified: 1_700_000_000_001 },
    { name: "c.mkv", size: 3000, lastModified: 1_700_000_000_002 },
  ];
  const restored = () =>
    run(initialState(rules), {
      type: "restore",
      jobId: "job1",
      files: stored,
      onServer: ["C.mkv"],
    });

  it("shows the unfinished files as awaiting and the finished one as done", () => {
    const s = restored();
    expect(s.jobId).toBe("job1");
    expect(s.entries.map((e) => e.status)).toEqual(["awaiting", "awaiting", "done"]);
    expect(toStart(s)).toEqual([]);
  });

  it("the same file chosen again continues with its upload URL", () => {
    const s = run(restored(), {
      type: "add",
      files: [file("a.mkv", 1000, 1_700_000_000_000)],
    });
    const e = entry(s, "a.mkv");
    expect(e).toMatchObject({ status: "queued", key: "k-a.mkv", uploadUrl: "/api/uploads/u1" });
    expect(s.rejected).toEqual([]);
    expect(toStart(s)).toEqual(["k-a.mkv"]);
  });

  it("a file with the same name but another size or date is refused with an explanation", () => {
    for (const other of [file("a.mkv", 999, 1_700_000_000_000), file("a.mkv", 1000, 5)]) {
      const s = run(restored(), { type: "add", files: [other] });
      expect(s.rejected).toEqual([{ name: "a.mkv", code: "file_mismatch" }]);
      expect(entry(s, "a.mkv").status).toBe("awaiting");
      expect(toStart(s)).toEqual([]);
    }
  });

  it("a finished file chosen again is a duplicate, not a second upload", () => {
    const s = run(restored(), { type: "add", files: [file("c.mkv", 3000, 1_700_000_000_002)] });
    expect(s.rejected).toEqual([{ name: "c.mkv", code: "duplicate_name" }]);
  });

  it("once every file is chosen again and uploaded, the job is ready to submit", () => {
    let s = run(restored(), {
      type: "add",
      files: [file("a.mkv", 1000, 1_700_000_000_000), file("b.mkv", 2000, 1_700_000_000_001)],
    });
    s = run(s, { type: "done", key: "k-a.mkv" }, { type: "done", key: "k-b.mkv" });
    expect(allDone(s)).toBe(true);
  });

  it("a restored job whose files are all on the server is ready at once", () => {
    const s = run(initialState(rules), {
      type: "restore",
      jobId: "job1",
      files: stored,
      onServer: ["a.mkv", "b.mkv", "c.mkv"],
    });
    expect(allDone(s)).toBe(true);
  });
});

describe("what is kept for a reload", () => {
  it("stores the job id and each file's name, size, date and upload URL", () => {
    let s = uploading(["a.mkv", "b.mkv"]);
    s = run(s, { type: "uploadUrl", key: "k-a.mkv", url: "/api/uploads/u1" });
    saveJob(s);
    expect(JSON.parse(localStorage.getItem(storageKey) ?? "null")).toEqual({
      jobId: "job1",
      files: [
        {
          name: "a.mkv",
          size: 1000,
          lastModified: 1_700_000_000_000,
          uploadUrl: "/api/uploads/u1",
        },
        { name: "b.mkv", size: 1000, lastModified: 1_700_000_000_000 },
      ],
    });
    expect(loadJob()?.files).toHaveLength(2);
  });

  it("is removed once the job is submitted, or when the job cannot continue", () => {
    const s = uploading(["a.mkv"]);
    saveJob(s);
    saveJob(run(s, { type: "submitted" }));
    expect(loadJob()).toBeNull();

    saveJob(s);
    saveJob({ ...s, jobError: "not_found" });
    expect(loadJob()).toBeNull();
  });

  it("ignores what is not a stored job", () => {
    localStorage.setItem(storageKey, "{not json");
    expect(loadJob()).toBeNull();
    localStorage.setItem(storageKey, JSON.stringify({ jobId: 1, files: [] }));
    expect(loadJob()).toBeNull();
    localStorage.setItem(storageKey, JSON.stringify({ jobId: "j", files: [{ name: 1 }, null] }));
    expect(loadJob()).toEqual({ jobId: "j", files: [] });
    clearJob();
    expect(localStorage.getItem(storageKey)).toBeNull();
  });
});
