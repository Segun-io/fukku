import { act, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { HttpResponse, http } from "msw";
import { afterEach, describe, expect, it, vi } from "vitest";
import type { Job, JobState } from "@/api/types";
import { jobDetail, jobs } from "@/mocks/fixtures";
import { setMockAdmin } from "@/mocks/handlers";
import { server } from "@/mocks/server";
import { renderApp } from "@/test/render";

const id = (n: number) => n.toString(16).padStart(32, "0");
const open = (n: number) => renderApp(`/jobs/${id(n)}`);
const fixture = (n: number): Job => {
  const job = jobs.find((j) => j.id === id(n));
  if (!job) throw new Error(`no fixture job ${n}`);
  return job;
};

afterEach(() => vi.useRealTimers());

describe("a probed job", () => {
  it("shows the files, the refused one with its reason, and the archive's skipped entries", async () => {
    open(5);
    expect(
      await screen.findByRole("heading", { name: "Example Show S01.zip", level: 1 }),
    ).toBeInTheDocument();

    const files = screen.getByRole("list", { name: "Files in this upload" });
    const refused = within(files).getByText("Example Show S01E02.mkv").closest("li");
    expect(refused).toHaveAttribute("data-status", "refused");
    expect(refused).toHaveTextContent(
      "No video was found inside this file. It may be damaged or renamed.",
    );
    expect(within(files).getByText("Example Show S01E01.mkv").closest("li")).toHaveAttribute(
      "data-status",
      "ok",
    );
    expect(within(files).getAllByText("From Example Show S01.zip")).toHaveLength(2);

    expect(screen.getByRole("heading", { name: "Left out of Example Show S01.zip" })).toBeVisible();
    expect(screen.getByText("../outside.mkv: its name points outside the folder")).toBeVisible();
    expect(screen.getByText("Extras/readme.exe: this kind of file is not accepted")).toBeVisible();
    // A reason the UI does not know falls back to a sentence with the code.
    expect(screen.getByText("Odd.mkv: skipped (code: something_new)")).toBeVisible();
  });

  it("shows the probe of a video: duration, resolution, codec, tracks", async () => {
    open(2);
    const probe = await screen.findByRole("region", {
      name: "What was found in Another Film (1999).mkv",
    });
    expect(within(probe).getByText("1h 30m")).toBeInTheDocument();
    expect(within(probe).getByText("1920×1080, H264, 23.976 fps")).toBeInTheDocument();
    expect(within(probe).getAllByText("AAC 2.0")).toHaveLength(2);
    expect(within(probe).getAllByText("English").length).toBe(2);
    expect(within(probe).getByText(/Spanish/)).toBeInTheDocument();
    expect(within(probe).getByText(/Español/)).toBeInTheDocument();
    expect(within(probe).getByText("SUBRIP")).toBeInTheDocument();
  });

  it("explains the manual import: folder with a copy button, the link, two sentences", async () => {
    const user = userEvent.setup();
    open(2);
    const section = (await screen.findByRole("heading", { name: "Import it by hand" })).closest(
      "section",
    );
    expect(section).not.toBeNull();
    if (!section) return;
    expect(within(section).getByText(`/data/intake/jobs/${id(2)}/files`)).toBeInTheDocument();
    expect(within(section).getByRole("link", { name: "Open Radarr" })).toHaveAttribute(
      "href",
      "https://radarr.example.com/wanted/missing",
    );
    expect(within(section).getByText(/Manual Import/)).toBeInTheDocument();

    await user.click(within(section).getByRole("button", { name: "Copy the Radarr folder" }));
    expect(await within(section).findByText("Copied")).toBeInTheDocument();
    expect(await navigator.clipboard.readText()).toBe(`/data/intake/jobs/${id(2)}/files`);
  });

  it("shows no link when the server has no public address, only the folder", async () => {
    server.use(
      http.get("/api/jobs/:jobId", () =>
        HttpResponse.json({
          ...jobDetail(fixture(2)),
          manualImport: [{ app: "sonarr", folder: "/downloads/intake/jobs/x/files", url: null }],
        }),
      ),
    );
    open(2);
    expect(await screen.findByText("/downloads/intake/jobs/x/files")).toBeInTheDocument();
    expect(screen.queryByRole("link", { name: /Open/ })).not.toBeInTheDocument();
    expect(screen.getByRole("button", { name: "Copy the Sonarr folder" })).toBeInTheDocument();
  });

  it("is written in Spanish when the language is Spanish", async () => {
    localStorage.setItem("fukku.lang", "es");
    open(5);
    expect(await screen.findByRole("heading", { name: "Impórtalo a mano" })).toBeVisible();
    expect(screen.getByText("Odd.mkv: omitido (código: something_new)")).toBeVisible();
    expect(
      screen.getByText(
        "No se encontró video dentro de este archivo. Puede estar dañado o tener otro nombre.",
      ),
    ).toBeVisible();
  });

  it("offers only cancel", async () => {
    open(2);
    await screen.findByRole("heading", { name: "Another Film (1999).mkv", level: 1 });
    expect(screen.getByRole("button", { name: "Cancel upload" })).toBeVisible();
    expect(screen.queryByRole("button", { name: "Try again" })).not.toBeInTheDocument();
    expect(screen.queryByRole("button", { name: "Delete" })).not.toBeInTheDocument();
  });
});

describe("a failed job", () => {
  it("explains the error code in plain words and offers retry, cancel and delete", async () => {
    open(3);
    const alert = await screen.findByRole("alert");
    expect(alert).toHaveTextContent("Checking the files failed. Try again");
    expect(screen.getByRole("button", { name: "Try again" })).toBeVisible();
    expect(screen.getByRole("button", { name: "Cancel upload" })).toBeVisible();
    expect(screen.getByRole("button", { name: "Delete" })).toBeVisible();
  });

  it("retry asks first, then moves the job on", async () => {
    const user = userEvent.setup();
    open(3);
    await user.click(await screen.findByRole("button", { name: "Try again" }));
    expect(screen.getByRole("heading", { name: "Try this upload again?" })).toBeVisible();
    // Nothing has been sent yet.
    expect(screen.queryByText("Uploaded")).not.toBeInTheDocument();
    const confirm = screen.getByRole("form", { name: "Try this upload again?" });
    await user.click(within(confirm).getByRole("button", { name: "Try again" }));
    await waitFor(() => expect(screen.queryByRole("form")).not.toBeInTheDocument());
    expect(await screen.findByText("Uploaded")).toBeVisible();
    expect(screen.queryByRole("button", { name: "Try again" })).not.toBeInTheDocument();
  });

  it("choosing to keep it closes the confirmation and changes nothing", async () => {
    const user = userEvent.setup();
    open(3);
    await user.click(await screen.findByRole("button", { name: "Delete" }));
    await user.click(screen.getByRole("button", { name: "Keep it as it is" }));
    expect(screen.getByRole("button", { name: "Delete" })).toBeVisible();
  });

  it("delete needs the warning ticked, then leaves for the list", async () => {
    const user = userEvent.setup();
    const { router } = open(3);
    await user.click(await screen.findByRole("button", { name: "Delete" }));
    const form = screen.getByRole("form", { name: "Delete this upload?" });
    expect(form).toHaveTextContent("The upload and its files are deleted for good.");

    await user.click(within(form).getByRole("button", { name: "Delete" }));
    expect(within(form).getByText("Tick the box above to continue.")).toBeVisible();
    expect(router.state.location.pathname).toBe(`/jobs/${id(3)}`);

    await user.click(within(form).getByRole("checkbox"));
    await user.click(within(form).getByRole("button", { name: "Delete" }));
    await waitFor(() => expect(router.state.location.pathname).toBe("/jobs"));
    expect(await screen.findByText("4 uploads")).toBeVisible();
  });

  it("cancel shows its warning and cancels", async () => {
    const user = userEvent.setup();
    open(3);
    await user.click(await screen.findByRole("button", { name: "Cancel upload" }));
    const form = screen.getByRole("form", { name: "Cancel this upload?" });
    await user.click(within(form).getByRole("checkbox"));
    await user.click(within(form).getByRole("button", { name: "Cancel upload" }));
    expect(await screen.findByText("Cancelled. The files were deleted.")).toBeVisible();
    // Only delete is left.
    expect(screen.getByRole("button", { name: "Delete" })).toBeVisible();
    expect(screen.queryByRole("button", { name: "Cancel upload" })).not.toBeInTheDocument();
  });

  it("a refusal from the server is shown in words", async () => {
    server.use(
      http.post("/api/jobs/:jobId/retry", () =>
        HttpResponse.json({ error: { code: "wrong_state", message: "no" } }, { status: 409 }),
      ),
    );
    const user = userEvent.setup();
    open(3);
    await user.click(await screen.findByRole("button", { name: "Try again" }));
    await user.click(within(screen.getByRole("form")).getByRole("button", { name: "Try again" }));
    expect(
      await screen.findByText("That is not possible while the upload is in this state."),
    ).toBeVisible();
  });

  it("an error code the UI does not know gets a generic sentence with the code", async () => {
    server.use(
      http.get("/api/jobs/:jobId", () =>
        HttpResponse.json({
          ...jobDetail(fixture(3)),
          error: { code: "from_the_future", message: "x" },
        }),
      ),
    );
    open(3);
    expect(await screen.findByRole("alert")).toHaveTextContent(
      "Something went wrong with this upload (code: from_the_future)",
    );
  });
});

describe("a stalled job", () => {
  it("explains free_space as something that resolves by itself", async () => {
    setMockAdmin(true);
    open(6);
    const status = await screen.findByText(/not enough free space on the server/);
    expect(status).toHaveTextContent("continues by itself when there is room");
    // The admin sees whose upload it is.
    expect(screen.getByText("Uploaded by user2")).toBeVisible();
    expect(screen.queryByRole("button", { name: "Try again" })).not.toBeInTheDocument();
  });

  it("stalled while importing: cancelling says the app may still finish the import", async () => {
    setMockAdmin(true);
    const detail = jobDetail(fixture(6));
    const at = "2026-01-01T07:09:00Z";
    server.use(
      http.get("/api/jobs/:jobId", () =>
        HttpResponse.json({
          ...detail,
          error: { code: "arr_unavailable", message: "x" },
          history: [
            ...detail.history.slice(0, -1),
            { at, from: "awaiting-confirmation", to: "importing", actor: "user2", note: "" },
            { at, from: "importing", to: "stalled", actor: "fukku", note: "" },
          ],
        }),
      ),
    );
    const user = userEvent.setup();
    open(6);
    await user.click(await screen.findByRole("button", { name: "Cancel upload" }));
    const form = screen.getByRole("form", { name: "Cancel this upload?" });
    expect(form).toHaveTextContent("The files of this upload are deleted from the server.");
    expect(form).toHaveTextContent(
      "The import may still be running in the app: it can still finish and put the files in the library.",
    );
  });

  it("stalled before the import: cancelling has no import warning", async () => {
    setMockAdmin(true);
    const user = userEvent.setup();
    open(6);
    await user.click(await screen.findByRole("button", { name: "Cancel upload" }));
    const form = screen.getByRole("form", { name: "Cancel this upload?" });
    expect(form).toHaveTextContent("The files of this upload are deleted from the server.");
    expect(form).not.toHaveTextContent("The import may still be running");
  });

  it("explains worker_unavailable", async () => {
    setMockAdmin(true);
    server.use(
      http.get("/api/jobs/:jobId", () =>
        HttpResponse.json({
          ...jobDetail(fixture(6)),
          error: { code: "worker_unavailable", message: "x" },
        }),
      ),
    );
    open(6);
    expect(await screen.findByText(/is not answering/)).toBeVisible();
  });
});

describe("an uploading job", () => {
  it("lists unfinished uploads and links back to /upload", async () => {
    const user = userEvent.setup();
    const { router } = open(1);
    expect(await screen.findByRole("heading", { name: "Unfinished uploads" })).toBeVisible();
    expect(screen.getByText("Extra.mkv")).toBeVisible();
    expect(screen.getByText(/40\s?%/)).toBeVisible();
    await user.click(screen.getByRole("link", { name: "Continue uploading" }));
    expect(router.state.location.pathname).toBe("/upload");
  });
});

describe("somebody else's job", () => {
  it("is shown as not found, like a job that does not exist", async () => {
    open(6); // user2's job, signed in as user1: the API answers 404
    expect(await screen.findByRole("heading", { name: "This upload was not found" })).toBeVisible();
    expect(screen.queryByText("Other Show S02.zip")).not.toBeInTheDocument();
    expect(screen.getByRole("link", { name: "Back to your uploads" })).toBeVisible();
  });
});

describe("polling", () => {
  function countRequests(state: JobState) {
    const counter = { n: 0 };
    server.use(
      http.get("/api/jobs/:jobId", () => {
        counter.n += 1;
        return HttpResponse.json(jobDetail({ ...fixture(1), state, actions: ["cancel"] }));
      }),
    );
    return counter;
  }

  async function advance(ms: number) {
    await act(async () => {
      await vi.advanceTimersByTimeAsync(ms);
    });
  }

  it.each(["uploading", "uploaded", "probing", "stalled"] as const)(
    "asks again every 2 s while the job is %s",
    async (state) => {
      vi.useFakeTimers({ shouldAdvanceTime: true });
      const counter = countRequests(state);
      open(1);
      await screen.findByRole("heading", { level: 1 });
      const first = counter.n;
      await advance(6100);
      expect(counter.n).toBeGreaterThanOrEqual(first + 3);
    },
  );

  it.each(["probed", "done", "cancelled", "failed"] as const)(
    "does not ask again once the job is %s",
    async (state) => {
      vi.useFakeTimers({ shouldAdvanceTime: true });
      const counter = countRequests(state);
      open(1);
      await screen.findByRole("heading", { level: 1 });
      const first = counter.n;
      await advance(60_000);
      expect(counter.n).toBe(first);
    },
  );

  it("stops when the job reaches probed", async () => {
    vi.useFakeTimers({ shouldAdvanceTime: true });
    let state: JobState = "probing";
    let n = 0;
    server.use(
      http.get("/api/jobs/:jobId", () => {
        n += 1;
        return HttpResponse.json(jobDetail({ ...fixture(2), state }));
      }),
    );
    open(2);
    expect(await screen.findByText("Checking")).toBeVisible();
    state = "probed";
    await advance(2100);
    expect(await screen.findByText("Checked")).toBeVisible();
    const settled = n;
    await advance(60_000);
    expect(n).toBe(settled);
  });

  it("does not ask while the tab is hidden", async () => {
    vi.useFakeTimers({ shouldAdvanceTime: true });
    const counter = countRequests("probing");
    open(1);
    await screen.findByRole("heading", { level: 1 });
    const visibility = vi.spyOn(document, "visibilityState", "get").mockReturnValue("hidden");
    document.dispatchEvent(new Event("visibilitychange"));
    const first = counter.n;
    await advance(10_000);
    expect(counter.n).toBe(first);
    visibility.mockRestore();
  });
});
