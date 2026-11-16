import { screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { delay, HttpResponse, http } from "msw";
import { afterEach, describe, expect, it } from "vitest";
import type { Proposal, ProposalRow } from "@/api/proposal";
import type { ConfirmRequest, JobDetail, JobFile } from "@/api/types";
import { jobDetail, jobs, session } from "@/mocks/fixtures";
import { applyScenarios } from "@/mocks/handlers";
import { mappingFilmJobId, mappingJobId } from "@/mocks/mapping-fixtures";
import { mappingJobs } from "@/mocks/scenarios";
import { server } from "@/mocks/server";
import { renderApp } from "@/test/render";

afterEach(() => server.events.removeAllListeners());

const film = mappingFilmJobId;
const target = { app: "radarr", titleId: 20, title: "Sintel", year: 2010 } as const;
const quality = () => screen.getByRole("combobox", { name: "Quality" });
const importButton = () => screen.getByRole("button", { name: "Import" });

const subtitle = (id: number, name: string): JobFile => ({
  id,
  path: `files/${name}`,
  name,
  kind: "subtitle",
  size: 1700,
  status: "ok",
  reason: null,
  from: null,
  probe: null,
  extraction: null,
});

/** The film job (awaiting confirmation) with whatever the test changes. */
function detail(over: Partial<JobDetail> = {}): JobDetail {
  const base = mappingJobs.find((j) => j.id === film);
  return { ...jobDetail(base as NonNullable<typeof base>), target, ...over };
}

function serveJob(over: Partial<JobDetail>) {
  server.use(http.get("/api/jobs/:id", () => HttpResponse.json(detail(over))));
}

const english = { id: 1, name: "English" };
const spanish = { id: 3, name: "Spanish" };

function row(over: Partial<ProposalRow> = {}): ProposalRow {
  return {
    id: "r1",
    fileId: 301,
    name: "Sintel.2010.1080p.mkv",
    durationMs: 888_000,
    skip: false,
    quality: { id: 3, name: "WEBDL-1080p" },
    languages: [english],
    customFormats: [],
    customFormatScore: 0,
    rejections: [],
    warnings: [],
    importable: true,
    ...over,
  };
}

const rejected = row({
  quality: { id: 1, name: "SDTV" },
  rejections: [{ reason: "Quality SDTV is not wanted in the profile HD-1080p", permanent: false }],
  importable: false,
});

/** A proposal the test controls: `PATCH` runs `edit` on the row after a short wait. */
function serveRows(rows: ProposalRow[], edit?: (r: ProposalRow, body: unknown) => ProposalRow) {
  const proposal = (): Proposal => ({
    state: "ready",
    app: "radarr",
    target: { titleId: 20, title: "Sintel", year: 2010 },
    rows,
    hints: [],
    qualities: [
      { id: 1, name: "SDTV" },
      { id: 3, name: "WEBDL-1080p" },
    ],
    languages: [english, spanish],
  });
  server.use(
    http.get("/api/jobs/:id/proposal", () => HttpResponse.json(proposal())),
    http.patch("/api/jobs/:id/proposal/rows/:rowId", async ({ params, request }) => {
      await delay(150);
      const at = rows.findIndex((r) => r.id === params.rowId);
      const next = edit ? edit(rows[at] as ProposalRow, await request.json()) : rows[at];
      rows[at] = next as ProposalRow;
      return HttpResponse.json(next);
    }),
  );
}

/** Records the body of every confirm request. */
function recordConfirm(): ConfirmRequest[] {
  const bodies: ConfirmRequest[] = [];
  server.events.on("request:start", async ({ request }) => {
    if (request.method === "POST" && request.url.endsWith("/confirm")) {
      bodies.push((await request.clone().json()) as ConfirmRequest);
    }
  });
  return bodies;
}

const open = (id: string = film) => {
  applyScenarios(["mapping"]);
  return renderApp(`/jobs/${id}/confirm`);
};

describe("the confirmation page", () => {
  it("shows a clean row next to its film and imports it", async () => {
    const bodies = recordConfirm();
    const user = userEvent.setup();
    const { router } = open();

    expect(await screen.findByRole("heading", { name: "Confirm the import" })).toBeVisible();
    const card = (await screen.findByRole("heading", { name: "Sintel.2010.1080p.mkv" })).closest(
      "li",
    ) as HTMLElement;
    expect(within(card).getByText("Sintel (2010)")).toBeVisible();
    expect(within(card).getByText("14m 48s")).toBeVisible();
    expect(quality()).toHaveValue("3");
    expect(within(card).getByRole("button", { name: "Remove English" })).toBeVisible();
    // Nothing is against it: no warning box, no tick.
    expect(screen.queryByText("Read this before you import")).not.toBeInTheDocument();
    expect(screen.queryByRole("checkbox", { name: "Import anyway" })).not.toBeInTheDocument();

    await user.click(importButton());
    await waitFor(() => expect(router.state.location.pathname).toBe(`/jobs/${film}`), {
      timeout: 3000,
    });
    expect(bodies).toHaveLength(1);
    expect(bodies[0]?.overrides).toEqual([]);
  });

  it("waits for the app while the proposal is pending", async () => {
    server.use(
      http.get("/api/jobs/:id/proposal", () =>
        HttpResponse.json({
          state: "pending",
          app: "radarr",
          target: null,
          rows: [],
          hints: [],
          qualities: [],
          languages: [],
        }),
      ),
    );
    open();
    expect(
      await screen.findByText("Radarr is looking at the files. This takes a moment."),
    ).toBeVisible();
    expect(screen.queryByRole("button", { name: "Import" })).not.toBeInTheDocument();
  });

  it("needs the tick for a rejected row, and lists the row in the overrides", async () => {
    serveRows([rejected]);
    const bodies = recordConfirm();
    const user = userEvent.setup();
    const { router } = open();

    expect(await screen.findByText("Read this before you import")).toBeVisible();
    expect(screen.getByText(/Quality SDTV is not wanted in the profile/)).toBeVisible();

    // Without the tick the button does not send anything and says why.
    await user.click(importButton());
    expect(await screen.findByRole("alert")).toHaveTextContent("Tick “Import anyway”");
    expect(screen.getByRole("checkbox", { name: "Import anyway" })).toHaveFocus();
    expect(bodies).toHaveLength(0);

    await user.click(screen.getByRole("checkbox", { name: "Import anyway" }));
    await user.click(importButton());
    await waitFor(() => expect(router.state.location.pathname).toBe(`/jobs/${film}`), {
      timeout: 3000,
    });
    expect(bodies).toHaveLength(1);
    expect(bodies[0]?.overrides).toEqual(["r1"]);
  });

  it("explains the language warning with its facts, and it needs the tick too", async () => {
    // The dub of the mock season: the profile asks for 0, the file scores -10000.
    open(mappingJobId);
    const name = "Lighthouse.Letters.S01E06.1080p.WEB-DL.mkv";
    const card = (await screen.findByRole("heading", { name })).closest("li") as HTMLElement;
    expect(within(card).getByText(/for example a dub where the original is wanted/)).toBeVisible();
    expect(
      within(card).getByText(
        "Custom formats: Not original language. Score -10,000; the profile asks for at least 0.",
      ),
    ).toBeVisible();
    expect(within(card).getByRole("checkbox", { name: "Import anyway" })).not.toBeChecked();
  });

  it("clears the rejection after a corrected quality is re-checked", async () => {
    serveRows([rejected], (r, body) => ({
      ...r,
      quality: { id: (body as { qualityId: number }).qualityId, name: "WEBDL-1080p" },
      rejections: [],
      importable: true,
    }));
    const bodies = recordConfirm();
    const user = userEvent.setup();
    open();
    await screen.findByText(/Quality SDTV is not wanted/);

    await user.selectOptions(quality(), "WEBDL-1080p");
    expect(await screen.findByText("Radarr is checking your change…")).toBeVisible();
    expect(quality()).toBeDisabled();

    await waitFor(() =>
      expect(screen.queryByText(/Quality SDTV is not wanted/)).not.toBeInTheDocument(),
    );
    expect(screen.queryByText("Radarr is checking your change…")).not.toBeInTheDocument();
    expect(screen.queryByRole("checkbox", { name: "Import anyway" })).not.toBeInTheDocument();

    await user.click(importButton());
    await waitFor(() => expect(bodies).toHaveLength(1));
    expect(bodies[0]?.overrides).toEqual([]);
  });

  it("changes the languages of a row through the same check", async () => {
    const edits: unknown[] = [];
    server.events.on("request:start", async ({ request }) => {
      if (request.method === "PATCH") edits.push(await request.clone().json());
    });
    const user = userEvent.setup();
    open();
    await user.selectOptions(
      await screen.findByRole("combobox", { name: "Add a language" }),
      "Spanish",
    );
    await waitFor(() => expect(edits).toEqual([{ languageIds: [1, 3] }]));
    await screen.findByRole("button", { name: "Remove Spanish" });
    await waitFor(() =>
      expect(screen.getByRole("button", { name: "Remove English" })).toBeEnabled(),
    );
    await user.click(screen.getByRole("button", { name: "Remove English" }));
    await waitFor(() => expect(edits).toContainEqual({ languageIds: [3] }));
  });

  it("asks again when the server says a row needs an override", async () => {
    server.use(
      http.post("/api/jobs/:id/confirm", () =>
        HttpResponse.json(
          { error: { code: "needs_override", message: "x", rows: ["r1"] } },
          { status: 409 },
        ),
      ),
    );
    const user = userEvent.setup();
    const { router } = open();
    await user.click(await screen.findByRole("button", { name: "Import" }));
    expect(await screen.findByRole("alert")).toHaveTextContent("Some files have problems");
    expect(router.state.location.pathname).toBe(`/jobs/${film}/confirm`);
  });

  it("explains rows that have no episode (incomplete_rows)", async () => {
    server.use(
      http.post("/api/jobs/:id/confirm", () =>
        HttpResponse.json(
          { error: { code: "incomplete_rows", message: "x", rows: ["r1"] } },
          { status: 409 },
        ),
      ),
    );
    const user = userEvent.setup();
    open();
    await user.click(await screen.findByRole("button", { name: "Import" }));
    expect(await screen.findByRole("alert")).toHaveTextContent("no film or episode to go to");
  });

  it("shows when the app does not answer on confirm (502)", async () => {
    server.use(
      http.post("/api/jobs/:id/confirm", () =>
        HttpResponse.json({ error: { code: "arr_unavailable", message: "x" } }, { status: 502 }),
      ),
    );
    const user = userEvent.setup();
    open();
    await user.click(await screen.findByRole("button", { name: "Import" }));
    expect(await screen.findByRole("alert")).toHaveTextContent("cannot be reached");
  });

  it("leaves a file out and then has nothing to import", async () => {
    const user = userEvent.setup();
    open();
    await user.click(await screen.findByRole("button", { name: "Leave this file out" }));
    expect(
      await screen.findByText("This file is left out and will not be imported."),
    ).toBeVisible();
    expect(screen.getByText(/there is nothing to import/)).toBeVisible();
    expect(importButton()).toBeDisabled();
  });

  it("guesses each subtitle file from its name, lets the user edit it, and sends it", async () => {
    serveJob({ files: [subtitle(13, "Sintel.es.srt"), subtitle(14, "Sintel.eng.forced.srt")] });
    const bodies = recordConfirm();
    const user = userEvent.setup();
    open();
    const [first, second] = await screen.findAllByRole("textbox", { name: /^Language of Sintel/ });
    expect(first).toHaveValue("es");
    // Three letters in the name, two in the request (ISO 639-1).
    expect(second).toHaveValue("en");
    const forced = screen.getAllByRole("checkbox", { name: /is forced$/ });
    expect(forced[0]).not.toBeChecked();
    expect(forced[1]).toBeChecked();

    await user.clear(first as HTMLElement);
    await user.type(first as HTMLElement, "fr");
    await user.click(
      screen.getAllByRole("checkbox", { name: /for the hard of hearing$/ })[0] as HTMLElement,
    );
    await user.click(importButton());
    await waitFor(() => expect(bodies).toHaveLength(1));
    expect(bodies[0]?.subtitles).toEqual([
      { fileId: 13, language: "fr", forced: false, hearingImpaired: true },
      { fileId: 14, language: "en", forced: true, hearingImpaired: false },
    ]);
  });

  it("does not send a subtitle file whose language is not two letters", async () => {
    serveJob({ files: [subtitle(13, "Sintel.srt")] });
    const bodies = recordConfirm();
    const user = userEvent.setup();
    open();
    const language = await screen.findByRole("textbox", { name: /^Language of Sintel/ });
    expect(language).toHaveValue("");
    await user.click(importButton());
    expect(await screen.findByRole("alert")).toHaveTextContent("two letters");
    expect(bodies).toHaveLength(0);
    await user.type(language, "es");
    await user.click(importButton());
    await waitFor(() => expect(bodies).toHaveLength(1));
  });

  it("shows no subtitle files when no subtitle manager is configured", async () => {
    serveJob({ files: [subtitle(13, "Sintel.es.srt")] });
    server.use(
      http.get("/api/session", () => HttpResponse.json({ ...session, subtitleManager: false })),
    );
    const bodies = recordConfirm();
    const user = userEvent.setup();
    open();
    await screen.findByRole("heading", { name: "Sintel.2010.1080p.mkv" });
    expect(screen.queryByRole("heading", { name: "Subtitle files" })).not.toBeInTheDocument();
    await user.click(importButton());
    await waitFor(() => expect(bodies).toHaveLength(1));
    expect(bodies[0]).not.toHaveProperty("subtitles");
  });

  it("is in Spanish", async () => {
    localStorage.setItem("fukku.lang", "es");
    open();
    expect(await screen.findByRole("button", { name: "Importar" })).toBeVisible();
    expect(screen.getByRole("heading", { name: "Confirmar la importación" })).toBeVisible();
  });

  it("explains a job that cannot be confirmed", async () => {
    renderApp(`/jobs/${(jobs[1] as { id: string }).id}/confirm`);
    expect(
      await screen.findByRole("heading", { name: "This upload cannot be confirmed now" }),
    ).toBeVisible();
  });

  it("leads a matched job to the confirmation from the job page", async () => {
    serveJob({ state: "matched" });
    renderApp(`/jobs/${film}`);
    expect(await screen.findByRole("link", { name: "Review and import" })).toHaveAttribute(
      "href",
      `/jobs/${film}/confirm`,
    );
  });
});

const imported = (names = ["Sintel.2010.1080p.mkv"]) => ({
  status: "done",
  rows: names.map((name, i) => ({ id: `r${i + 1}`, name, result: "imported" })),
});

describe("progress and result on the job page", () => {
  it("shows each file by name while importing", async () => {
    serveJob({
      state: "importing",
      import: {
        status: "running",
        rows: [
          { id: "r1", name: "Sintel.S01E01.mkv", result: "imported" },
          { id: "r2", name: "Sintel.S01E02.mkv", result: "" },
        ],
      },
    });
    renderApp(`/jobs/${film}`);
    expect(await screen.findByRole("heading", { name: "Importing" })).toBeVisible();
    const list = screen.getByRole("list", { name: "Files" });
    expect(within(list).getByText("Sintel.S01E01.mkv").nextElementSibling).toHaveTextContent(
      "Imported",
    );
    expect(within(list).getByText("Sintel.S01E02.mkv").nextElementSibling).toHaveTextContent(
      "Waiting",
    );
  });

  it("shows the steps after the import while post-processing", async () => {
    serveJob({
      state: "post-processing",
      import: imported(),
      result: {
        titleLink: null,
        monitored: true,
        unmonitorReason: "",
        postProcessing: [
          { step: "mediaServerRefresh", status: "ok", detail: "" },
          { step: "subtitles", status: "pending", detail: "" },
        ],
      },
    });
    renderApp(`/jobs/${film}`);
    const steps = await screen.findByRole("list", { name: "Steps after the import" });
    expect(within(steps).getByText("Tell the media server about the new files")).toBeVisible();
    expect(within(steps).getByText("Working")).toBeVisible();
  });

  it("links to the title in the media server when it is done", async () => {
    serveJob({
      state: "done",
      actions: ["delete"],
      import: imported(),
      result: {
        titleLink: "https://media.example.com/details?id=1",
        monitored: true,
        unmonitorReason: "",
        postProcessing: [{ step: "mediaServerRefresh", status: "ok", detail: "" }],
      },
    });
    renderApp(`/jobs/${film}`);
    expect(await screen.findByText("Sintel is in the library")).toBeVisible();
    const link = screen.getByRole("link", { name: /Open the title in the media server/ });
    expect(link).toHaveAttribute("href", "https://media.example.com/details?id=1");
    expect(link).toHaveAttribute("rel", expect.stringContaining("noopener"));
    expect(screen.queryByText(/There is no link/)).not.toBeInTheDocument();
    expect(screen.queryByText("This title is not monitored")).not.toBeInTheDocument();
  });

  it("says so when there is no link", async () => {
    serveJob({
      state: "done",
      actions: ["delete"],
      import: imported(),
      result: { titleLink: null, monitored: null, unmonitorReason: "", postProcessing: [] },
    });
    renderApp(`/jobs/${film}`);
    expect(await screen.findByText("Sintel is in the library")).toBeVisible();
    expect(screen.queryByRole("link", { name: /media server/ })).not.toBeInTheDocument();
    expect(screen.getByText("There is no link to the media server for this title.")).toBeVisible();
  });

  it("warns after a dub-only import that the title's own search can still replace it", async () => {
    serveJob({
      state: "done",
      actions: ["delete"],
      import: imported(),
      result: {
        titleLink: null,
        monitored: false,
        unmonitorReason: "dub_only",
        postProcessing: [],
      },
    });
    renderApp(`/jobs/${film}`);
    expect(await screen.findByText("This title is not monitored")).toBeVisible();
    expect(screen.getByText(/Only a dub was imported/)).toBeVisible();
    expect(screen.getByText(/Search button in Radarr still can/)).toBeVisible();
  });

  it("says the same when the user chose not to monitor the title", async () => {
    serveJob({
      state: "done",
      actions: ["delete"],
      import: imported(),
      result: { titleLink: null, monitored: false, unmonitorReason: "chosen", postProcessing: [] },
    });
    renderApp(`/jobs/${film}`);
    expect(await screen.findByText(/You chose not to replace this file/)).toBeVisible();
    expect(screen.getByText(/Search button in Radarr still can/)).toBeVisible();
  });

  it("says what to do about a step that failed, and the job is still done", async () => {
    serveJob({
      state: "done",
      actions: ["delete"],
      import: imported(),
      result: {
        titleLink: null,
        monitored: true,
        unmonitorReason: "",
        postProcessing: [
          { step: "mediaServerRefresh", status: "ok", detail: "" },
          { step: "subtitles", status: "failed", detail: "es.srt was not written" },
        ],
      },
    });
    renderApp(`/jobs/${film}`);
    expect(await screen.findByText("Sintel is in the library")).toBeVisible();
    expect(screen.getByText("es.srt was not written")).toBeVisible();
    expect(screen.getByText(/Add them in the subtitle manager by hand/)).toBeVisible();
    expect(screen.getByText(/One of the steps after it failed/)).toBeVisible();
  });

  it("shows a skipped step with the reason as the server sends it", async () => {
    serveJob({
      state: "done",
      actions: ["delete"],
      import: imported(),
      result: {
        titleLink: null,
        monitored: true,
        unmonitorReason: "",
        postProcessing: [{ step: "subtitles", status: "skipped", detail: "provider is none" }],
      },
    });
    renderApp(`/jobs/${film}`);
    expect(await screen.findByText("provider is none")).toBeVisible();
    expect(screen.getByText("Not used")).toBeVisible();
  });

  it("explains a stalled import: waiting for Radarr, nothing to do", async () => {
    serveJob({
      state: "stalled",
      error: { code: "arr_unavailable", message: "x" },
      actions: [],
      import: { status: "running", rows: [{ id: "r1", name: "Sintel.mkv", result: "" }] },
    });
    renderApp(`/jobs/${film}`);
    expect(await screen.findByText(/Waiting for Radarr, which is not answering/)).toBeVisible();
    expect(screen.getByText(/There is nothing for you to do/)).toBeVisible();
    // Only an administrator may cancel a job stalled in the import: the server leaves it out.
    expect(screen.queryByRole("button", { name: "Cancel upload" })).not.toBeInTheDocument();
  });

  it("offers an administrator the cancel of an import-stalled job", async () => {
    serveJob({
      state: "stalled",
      error: { code: "arr_unavailable", message: "x" },
      actions: ["cancel"],
      import: { status: "running", rows: [{ id: "r1", name: "Sintel.mkv", result: "" }] },
    });
    renderApp(`/jobs/${film}`);
    expect(await screen.findByRole("button", { name: "Cancel upload" })).toBeVisible();
  });

  it("explains an import that failed with part of it imported", async () => {
    serveJob({
      state: "failed",
      error: { code: "import_failed", message: "x" },
      actions: ["delete"],
      import: {
        status: "failed",
        rows: [
          { id: "r1", name: "Sintel.S01E01.mkv", result: "imported" },
          { id: "r2", name: "Sintel.S01E02.mkv", result: "failed" },
        ],
      },
    });
    renderApp(`/jobs/${film}`);
    expect(await screen.findByText(/could not import every file/)).toBeVisible();
    expect(screen.getByText("Not imported")).toBeVisible();
  });

  it("runs the whole mock import from the confirmation to the result", async () => {
    const user = userEvent.setup();
    open();
    await user.click(await screen.findByRole("button", { name: "Import" }));
    expect(await screen.findByRole("heading", { name: "Importing" })).toBeVisible();
  });
});
