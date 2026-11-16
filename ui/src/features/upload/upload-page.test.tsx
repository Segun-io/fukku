import { screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { HttpResponse, http } from "msw";
import { afterEach, describe, expect, it, vi } from "vitest";
import { server } from "@/mocks/server";
import { renderApp } from "@/test/render";
import { loadJob, storageKey } from "./storage";

afterEach(() => {
  server.events.removeAllListeners();
  vi.useRealTimers();
  vi.unstubAllGlobals();
  vi.restoreAllMocks();
});

const MODIFIED = 1_700_000_000_000;
const origin = window.location.origin;

function video(name: string, size: number, lastModified = MODIFIED): File {
  // A file of a size that cannot be allocated in a test only has to claim it.
  const real = Math.min(size, 1_000_000);
  const file = new File([new Uint8Array(real)], name, { type: "video/x-matroska", lastModified });
  if (real !== size) Object.defineProperty(file, "size", { value: size });
  return file;
}

const picker = () => screen.findByLabelText("Choose files");

/** The requests the mock server saw, as "METHOD path". */
function record() {
  const seen: { method: string; path: string; offset: string | null }[] = [];
  server.events.on("request:start", ({ request }) => {
    const url = new URL(request.url);
    if (url.pathname.startsWith("/api/uploads/")) {
      seen.push({
        method: request.method,
        path: url.pathname,
        offset: request.headers.get("Upload-Offset"),
      });
    }
  });
  return seen;
}

describe("the upload page", () => {
  it("two files upload, the job is submitted, and the page goes to the job", async () => {
    const user = userEvent.setup();
    const seen = record();
    let submitted = "";
    server.events.on("request:start", ({ request }) => {
      const m = /\/api\/jobs\/(\w+)\/submit$/.exec(new URL(request.url).pathname);
      if (m?.[1]) submitted = m[1];
    });

    const { router } = renderApp("/upload");
    await user.upload(await picker(), [
      video("Example Film (2001).mkv", 3000),
      video("Example Film (2001).es.srt", 40),
    ]);
    expect(screen.getByText("Example Film (2001).mkv")).toBeInTheDocument();
    expect(screen.getAllByRole("progressbar")).toHaveLength(2);
    // Nothing is sent until the user starts.
    expect(seen).toEqual([]);

    await user.click(screen.getByRole("button", { name: "Start upload" }));

    await waitFor(() => expect(router.state.location.pathname).toMatch(/^\/jobs\/\w+$/), {
      timeout: 8000,
    });
    expect(router.state.location.pathname).toBe(`/jobs/${submitted}`);
    expect(submitted).not.toBe("");
    expect(seen.filter((r) => r.method === "POST")).toHaveLength(2);
    expect(seen.filter((r) => r.method === "PATCH")).toHaveLength(2);
    // The stored job is gone once it is submitted.
    expect(localStorage.getItem(storageKey)).toBeNull();
  });

  it("refuses files the server would refuse, before anything is sent, with a message", async () => {
    const user = userEvent.setup({ applyAccept: false });
    const seen = record();
    renderApp("/upload");
    await user.upload(await picker(), [
      video("notes.txt", 10),
      video("Huge.mkv", 85899345921),
      video("a.mkv", 10),
      video("A.MKV", 10),
    ]);
    const alert = await screen.findByRole("alert");
    expect(alert).toHaveTextContent("notes.txt");
    expect(alert).toHaveTextContent("This type of file is not accepted.");
    expect(alert).toHaveTextContent("The file is larger than the server allows.");
    expect(alert).toHaveTextContent("A file with this name was already added.");
    expect(screen.getAllByRole("progressbar")).toHaveLength(1);
    expect(seen).toEqual([]);
  });

  it("shows the large-file hint above the threshold", async () => {
    const user = userEvent.setup();
    renderApp("/upload");
    await user.upload(await picker(), [video("Big.mkv", 21474836481)]);
    expect(await screen.findByText("Large file")).toBeInTheDocument();
  });

  it("a refusal of one file stops that file, with its translated message, and not the others", async () => {
    const user = userEvent.setup();
    renderApp("/upload");
    await user.upload(await picker(), [
      video("Fine.mkv", 100),
      video("Film [insufficient_space].mkv", 100),
    ]);
    await user.click(screen.getByRole("button", { name: "Start upload" }));
    expect(
      await screen.findByText("The server does not have enough free space.", {}, { timeout: 8000 }),
    ).toBeInTheDocument();
    await waitFor(() => expect(screen.getByText("Uploaded")).toBeInTheDocument());
    // Not submitted: one file failed.
    expect(
      screen.getByRole("button", { name: "Try Film [insufficient_space].mkv again" }),
    ).toBeInTheDocument();
  });

  it("signed out: pauses, shows the sign-in notice, and continues when the user comes back", async () => {
    const user = userEvent.setup();
    let signedOut = false;
    server.use(
      http.patch("/api/uploads/:id", () => {
        if (signedOut) return new HttpResponse(null, { status: 401 });
      }),
      http.get("/api/session", () => {
        if (signedOut) return new HttpResponse(null, { status: 401 });
        return undefined;
      }),
    );
    const { router } = renderApp("/upload");
    const input = await picker();
    signedOut = true;
    await user.upload(input, [video("Example Film (2001).mkv", 3000)]);
    await user.click(screen.getByRole("button", { name: "Start upload" }));

    const notice = await screen.findAllByRole("link", { name: "Sign in again" }, { timeout: 8000 });
    expect(notice.length).toBeGreaterThan(0);
    expect(router.state.location.pathname).toBe("/upload");
    // The row is paused until the session is back, not "waiting its turn", and cannot be resumed by hand.
    expect(screen.getByText("Paused")).toBeInTheDocument();
    expect(screen.getByText(/Paused until you sign in again/)).toBeInTheDocument();
    expect(screen.queryByText("Waiting its turn")).not.toBeInTheDocument();
    expect(screen.queryByRole("button", { name: /^Resume/ })).not.toBeInTheDocument();

    signedOut = false;
    document.dispatchEvent(new Event("visibilitychange"));
    await waitFor(() => expect(router.state.location.pathname).toMatch(/^\/jobs\//), {
      timeout: 8000,
    });
  });

  it("a lost connection waits and retries by itself", async () => {
    const user = userEvent.setup();
    let failures = 1;
    server.use(
      http.patch("/api/uploads/:id", () => {
        if (failures-- > 0) return HttpResponse.error();
        return undefined;
      }),
    );
    const { router } = renderApp("/upload");
    await user.upload(await picker(), [video("Example Film (2001).mkv", 3000)]);
    await user.click(screen.getByRole("button", { name: "Start upload" }));
    expect(
      await screen.findByText("Waiting for the connection", {}, { timeout: 4000 }),
    ).toBeInTheDocument();
    await waitFor(() => expect(router.state.location.pathname).toMatch(/^\/jobs\//), {
      timeout: 8000,
    });
  });

  it("pause stops a file and resume continues it", async () => {
    const user = userEvent.setup();
    let release: () => void = () => {};
    const gate = new Promise<void>((r) => {
      release = r;
    });
    server.use(
      http.patch("/api/uploads/:id", async () => {
        await gate;
        return undefined;
      }),
    );
    renderApp("/upload");
    await user.upload(await picker(), [video("Example Film (2001).mkv", 3000)]);
    await user.click(screen.getByRole("button", { name: "Start upload" }));
    await user.click(await screen.findByRole("button", { name: "Pause Example Film (2001).mkv" }));
    expect(await screen.findByText("Paused")).toBeInTheDocument();
    release();
    await user.click(screen.getByRole("button", { name: "Resume Example Film (2001).mkv" }));
    await waitFor(() => expect(screen.queryByText("Paused")).not.toBeInTheDocument());
  });
});

describe("after a reload", () => {
  /** What a first visit left behind: a job with one file half uploaded, and its stored record. */
  async function interrupted(): Promise<{ jobId: string; url: string }> {
    const job = (await (await fetch(`${origin}/api/jobs`, { method: "POST" })).json()) as {
      id: string;
    };
    const meta = `filename ${btoa("Example Film (2001).mkv")},jobId ${btoa(job.id)}`;
    const created = await fetch(`${origin}/api/uploads/`, {
      method: "POST",
      headers: { "Upload-Length": "3000", "Upload-Metadata": meta, "Tus-Resumable": "1.0.0" },
    });
    const url = new URL(created.headers.get("Location") ?? "", "http://localhost").pathname;
    await fetch(`${origin}${url}`, {
      method: "PATCH",
      headers: { "Upload-Offset": "0", "Content-Type": "application/offset+octet-stream" },
      body: new Uint8Array(1200),
    });
    localStorage.setItem(
      storageKey,
      JSON.stringify({
        jobId: job.id,
        files: [
          { name: "Example Film (2001).mkv", size: 3000, lastModified: MODIFIED, uploadUrl: url },
        ],
      }),
    );
    return { jobId: job.id, url };
  }

  it("asks for the same file again, and the upload continues from the server's offset", async () => {
    const user = userEvent.setup();
    const { jobId, url } = await interrupted();
    const seen = record();
    const { router } = renderApp("/upload");

    expect(await screen.findByText("Your upload was interrupted")).toBeInTheDocument();
    expect(screen.getByText("Example Film (2001).mkv")).toBeInTheDocument();

    await user.upload(await picker(), [video("Example Film (2001).mkv", 3000)]);

    await waitFor(() => expect(router.state.location.pathname).toBe(`/jobs/${jobId}`), {
      timeout: 8000,
    });
    // It asked where the server is (HEAD), then sent only the rest: no new upload, no restart.
    expect(seen.map((r) => r.method)).toEqual(["HEAD", "PATCH"]);
    expect(seen[0]?.path).toBe(url);
    expect(seen[1]?.offset).toBe("1200");
  });

  // A restart of the server: the HEAD that continues the upload gets a 502 from the proxy. The
  // page must wait and ask again, never create a second upload of the same file.
  it("when the server is briefly down, it waits and continues the same upload", async () => {
    const user = userEvent.setup();
    const { jobId, url } = await interrupted();
    let down = 1;
    server.use(
      http.head("/api/uploads/:id", () => {
        if (down-- > 0) return new HttpResponse("Bad Gateway", { status: 502 });
        return undefined;
      }),
    );
    const seen = record();
    const { router } = renderApp("/upload");
    await screen.findByText("Your upload was interrupted");

    await user.upload(await picker(), [video("Example Film (2001).mkv", 3000)]);

    await waitFor(() => expect(router.state.location.pathname).toBe(`/jobs/${jobId}`), {
      timeout: 8000,
    });
    expect(seen.map((r) => r.method)).toEqual(["HEAD", "HEAD", "PATCH"]);
    expect(seen.every((r) => r.path === url)).toBe(true);
    expect(seen[2]?.offset).toBe("1200");
  });

  it("refuses a file that is not the one that was interrupted, and explains why", async () => {
    const user = userEvent.setup();
    await interrupted();
    const seen = record();
    renderApp("/upload");
    await screen.findByText("Your upload was interrupted");

    await user.upload(await picker(), [video("Example Film (2001).mkv", 3000, MODIFIED + 1)]);

    const alert = await screen.findByRole("alert");
    expect(within(alert).getByText(/not the file that was being uploaded/)).toBeInTheDocument();
    expect(seen).toEqual([]);
    expect(loadJob()?.files).toHaveLength(1);
  });

  it("forgets a job the server no longer has", async () => {
    localStorage.setItem(
      storageKey,
      JSON.stringify({
        jobId: "f".repeat(32),
        files: [{ name: "a.mkv", size: 1, lastModified: 1 }],
      }),
    );
    renderApp("/upload");
    expect(await screen.findByText("Choose your files")).toBeInTheDocument();
    expect(screen.queryByText("Your upload was interrupted")).not.toBeInTheDocument();
    await waitFor(() => expect(localStorage.getItem(storageKey)).toBeNull());
  });

  it("discarding the interrupted upload cancels the job and clears the stored record", async () => {
    const user = userEvent.setup();
    await interrupted();
    renderApp("/upload");
    await screen.findByText("Your upload was interrupted");
    // After a reload the page shows no sent bytes, and still asks first.
    const confirm = vi.spyOn(window, "confirm").mockReturnValue(false);
    await user.click(screen.getByRole("button", { name: "Discard this upload" }));
    expect(confirm).toHaveBeenCalledTimes(1);
    expect(screen.getByText("Your upload was interrupted")).toBeInTheDocument();
    expect(localStorage.getItem(storageKey)).not.toBeNull();

    confirm.mockReturnValue(true);
    await user.click(screen.getByRole("button", { name: "Discard this upload" }));
    expect(confirm).toHaveBeenCalledTimes(2);
    expect(await screen.findByText("Choose your files")).toBeInTheDocument();
    expect(localStorage.getItem(storageKey)).toBeNull();
  });
});

describe("the page in Spanish", () => {
  it("has every string translated", async () => {
    localStorage.setItem("fukku.lang", "es");
    renderApp("/upload");
    expect(await screen.findByLabelText("Elegir archivos")).toBeInTheDocument();
  });
});
