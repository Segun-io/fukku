import { screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { delay, HttpResponse, http } from "msw";
import { afterEach, describe, expect, it, vi } from "vitest";
import { failNextEdit, mappingFilmJobId, mappingJobId } from "@/mocks/mapping-fixtures";
import { server } from "@/mocks/server";
import { renderApp } from "@/test/render";

afterEach(() => {
  server.events.removeAllListeners();
  vi.unstubAllGlobals();
  localStorage.removeItem("fukku.lang");
});

// The files of the fixture season (mapping-fixtures.ts).
const f = (n: number) => `Lighthouse.Letters.S01E${String(n).padStart(2, "0")}.1080p.WEB-DL.mkv`;
const e01 = f(1); // a double: as long as two episodes, read as E01 only
const e03 = f(3);
const e05 = f(5); // named E05, really E04
const e06 = f(6); // a Spanish dub
const e07 = f(7); // the episode already has a file

const open = (id = mappingJobId) => renderApp(`/jobs/${id}/mapping`);
const user = () => userEvent.setup();

type Sent = { method: string; path: string; body: unknown };
/** Records every call of the proposal API with its body. */
function record() {
  const sent: Sent[] = [];
  server.events.on("request:start", async ({ request }) => {
    const path = new URL(request.url).pathname;
    if (!path.includes("/proposal")) return;
    const body = request.method === "GET" ? undefined : await request.clone().json();
    sent.push({ method: request.method, path, body });
  });
  return sent;
}
const edits = (sent: Sent[]) => sent.filter((s) => s.method !== "GET");

const rowOf = (name: string) => {
  const tr = screen.getByText(name).closest("tr");
  if (!tr) throw new Error(`no row for ${name}`);
  return within(tr);
};
const loaded = () => screen.findByRole("heading", { name: "Check the files" });
const summary = () => within(screen.getByRole("region", { name: "Summary" }));

describe("the mapping editor", () => {
  it("shows one row per file with the duration against the runtime, and the verdicts", async () => {
    open();
    await loaded();
    expect(screen.getByText("Lighthouse Letters (2019) in Sonarr")).toBeVisible();
    expect(screen.getAllByRole("row")).toHaveLength(6); // header and five files

    const row = rowOf(e01);
    expect(row.getByText("44m 40s, expected 22m")).toBeVisible();
    expect(row.getByRole("combobox", { name: `Season of ${e01}` })).toHaveDisplayValue("Season 1");
    expect(row.getByRole("combobox", { name: `Episode of ${e01}` })).toHaveDisplayValue(
      "1. The Keeper",
    );
    expect(row.getByRole("combobox", { name: `Quality of ${e01}` })).toHaveDisplayValue(
      "WEBDL-1080p",
    );
    expect(row.getByText(/about twice as long as an episode/)).toBeVisible();
    expect(rowOf(e03).getByText("No problems found.")).toBeVisible();
  });

  it("says that the season and the episodes come from the file names", async () => {
    open();
    await loaded();
    expect(screen.getByText(/come from the file names, or from Sonarr's best guess/)).toBeVisible();
  });

  it("summarises the rows and filters the ones that need attention", async () => {
    const u = user();
    open();
    await loaded();
    expect(summary().getByText("2 to import")).toBeVisible();
    expect(summary().getByText("1 skipped")).toBeVisible();
    expect(summary().getByText("2 need attention")).toBeVisible();

    await u.click(screen.getByRole("checkbox", { name: "Only files that need attention" }));
    expect(screen.getAllByRole("row")).toHaveLength(3); // header, E01 and E06
    expect(screen.getByText(e01)).toBeVisible();
    expect(screen.getByText(e06)).toBeVisible();
    expect(screen.queryByText(e03)).not.toBeInTheDocument();

    await u.click(screen.getByRole("checkbox", { name: "Only files that need attention" }));
    expect(screen.getAllByRole("row")).toHaveLength(6);
  });

  it("shows the language warning by its code with the facts the row carries", async () => {
    open();
    await loaded();
    const row = rowOf(e06);
    expect(
      row.getByText(
        /Languages: Spanish\. Score: -10,000; the quality profile asks for at least 0\./,
      ),
    ).toBeVisible();
    expect(row.getByText(/Rules that matched: Not original language\./)).toBeVisible();
  });

  it("skips a row that already has a file, with the reason", async () => {
    open();
    await loaded();
    const row = rowOf(e07);
    expect(row.getByRole("checkbox", { name: `Skip ${e07}` })).toBeChecked();
    expect(row.getByText(/already has a file in Sonarr/)).toBeVisible();
  });

  it("explains the dvd_order hint", async () => {
    server.use(
      http.get("/api/jobs/:id/proposal", () =>
        HttpResponse.json({
          state: "ready",
          app: "sonarr",
          target: { titleId: 1, title: "Some Show", year: 2010 },
          rows: [],
          hints: [{ code: "dvd_order" }],
          episodes: [],
          qualities: [],
          languages: [],
        }),
      ),
    );
    open();
    expect(await screen.findByText("The episode order may be different")).toBeVisible();
    expect(screen.getByText(/another order \(DVD order\)/)).toBeVisible();
  });
});

describe("correcting the awkward season", () => {
  it("makes a double: two episodes for one file, one PATCH, then the fresh verdict", async () => {
    const u = user();
    const sent = record();
    open();
    await loaded();
    const row = rowOf(e01);
    await u.selectOptions(
      row.getByRole("combobox", { name: `Second episode of ${e01}` }),
      "2. Low Tide",
    );
    await waitFor(() => expect(edits(sent)).toHaveLength(1));
    expect(edits(sent)[0]).toMatchObject({
      method: "PATCH",
      path: `/api/jobs/${mappingJobId}/proposal/rows/r1`,
      body: { episodeIds: [101, 102] },
    });
    await waitFor(() =>
      expect(rowOf(e01).queryByText(/about twice as long as an episode/)).not.toBeInTheDocument(),
    );
    expect(rowOf(e01).getByText("No problems found.")).toBeVisible();
    expect(rowOf(e01).getByText("44m 40s, expected 44m")).toBeVisible();
    await waitFor(() => expect(summary().getByText("3 to import")).toBeVisible());
  });

  it("makes a double with one click on the hint", async () => {
    const u = user();
    const sent = record();
    open();
    await loaded();
    await u.click(rowOf(e01).getByRole("button", { name: "Make it episodes 1 and 2" }));
    await waitFor(() => expect(edits(sent)).toHaveLength(1));
    expect(edits(sent)[0]?.body).toEqual({ episodeIds: [101, 102] });
    await waitFor(() =>
      expect(
        rowOf(e01).getByRole("combobox", { name: `Second episode of ${e01}` }),
      ).toHaveDisplayValue("2. Low Tide"),
    );
  });

  it("fixes the misnumbered file by choosing the right episode", async () => {
    const u = user();
    const sent = record();
    open();
    await loaded();
    await u.selectOptions(
      rowOf(e05).getByRole("combobox", { name: `Episode of ${e05}` }),
      "4. Fog Signals",
    );
    await waitFor(() => expect(edits(sent)).toHaveLength(1));
    expect(edits(sent)[0]?.body).toEqual({ episodeIds: [104] });
    // It is shown at once, and stays after the app has checked it.
    expect(rowOf(e05).getByRole("combobox", { name: `Episode of ${e05}` })).toHaveDisplayValue(
      "4. Fog Signals",
    );
    await waitFor(() => expect(rowOf(e05).queryByText("Checking…")).not.toBeInTheDocument());
    expect(rowOf(e05).getByRole("combobox", { name: `Episode of ${e05}` })).toHaveDisplayValue(
      "4. Fog Signals",
    );
  });

  it("skips a row and puts it back", async () => {
    const u = user();
    const sent = record();
    open();
    await loaded();
    await u.click(rowOf(e03).getByRole("checkbox", { name: `Skip ${e03}` }));
    await waitFor(() => expect(summary().getByText("2 skipped")).toBeVisible());
    expect(summary().getByText("1 to import")).toBeVisible();
    await u.click(rowOf(e03).getByRole("checkbox", { name: `Skip ${e03}` }));
    await waitFor(() => expect(summary().getByText("1 skipped")).toBeVisible());
    expect(edits(sent).map((s) => s.body)).toEqual([{ skip: true }, { skip: false }]);
  });

  it("shifts the selected rows by N", async () => {
    const u = user();
    const sent = record();
    open();
    await loaded();
    await u.click(screen.getByRole("checkbox", { name: `Select ${e03}` }));
    await u.click(screen.getByRole("checkbox", { name: `Select ${e05}` }));
    expect(screen.getByText("2 files selected")).toBeVisible();
    const input = screen.getByRole("spinbutton", { name: "Move episodes by" });
    await u.clear(input);
    await u.type(input, "-1");
    await u.click(screen.getByRole("button", { name: "Move episodes" }));
    await waitFor(() => expect(edits(sent)).toHaveLength(1));
    expect(edits(sent)[0]).toMatchObject({
      method: "POST",
      path: `/api/jobs/${mappingJobId}/proposal/bulk`,
      body: { rowIds: ["r2", "r3"], shift: -1 },
    });
    await waitFor(() =>
      expect(rowOf(e05).getByRole("combobox", { name: `Episode of ${e05}` })).toHaveDisplayValue(
        "4. Fog Signals",
      ),
    );
    expect(rowOf(e03).getByRole("combobox", { name: `Episode of ${e03}` })).toHaveDisplayValue(
      "2. Low Tide",
    );
    // E03 is now E02, which the double also claims once it is a double.
  });

  it("sets the season of the selected rows", async () => {
    const u = user();
    const sent = record();
    open();
    await loaded();
    await u.click(screen.getByRole("checkbox", { name: `Select ${e03}` }));
    await u.selectOptions(screen.getByRole("combobox", { name: "Season" }), "Season 2");
    await u.click(screen.getByRole("button", { name: "Set the season" }));
    await waitFor(() => expect(edits(sent)).toHaveLength(1));
    expect(edits(sent)[0]?.body).toEqual({ rowIds: ["r2"], seasonNumber: 2 });
    await waitFor(() =>
      expect(rowOf(e03).getByRole("combobox", { name: `Season of ${e03}` })).toHaveDisplayValue(
        "Season 2",
      ),
    );
    expect(rowOf(e03).getByRole("combobox", { name: `Episode of ${e03}` })).toHaveDisplayValue(
      "3. Paper Boats",
    );
  });

  it("can be corrected and continued: the whole season", async () => {
    const u = user();
    open();
    await loaded();
    await u.selectOptions(
      rowOf(e01).getByRole("combobox", { name: `Second episode of ${e01}` }),
      "2. Low Tide",
    );
    await u.selectOptions(
      rowOf(e05).getByRole("combobox", { name: `Episode of ${e05}` }),
      "4. Fog Signals",
    );
    await waitFor(() => expect(summary().getByText("3 to import")).toBeVisible());
    // The dub still needs the user's word at the confirmation; the rest is ready.
    expect(summary().getByText("1 skipped")).toBeVisible();
    expect(summary().getByText("1 needs attention")).toBeVisible();
    expect(screen.getByText(/confirmed one by one on the next step/)).toBeVisible();
    expect(screen.getByRole("link", { name: "Continue" })).toHaveAttribute(
      "href",
      `/jobs/${mappingJobId}`,
    );
  });
});

describe("edits that overlap, fail or meet a changed job", () => {
  it("sends the edits of one row one at a time and merges the ones made meanwhile", async () => {
    const u = user();
    const sent = record();
    let running = 0;
    let most = 0;
    server.use(
      http.patch("/api/jobs/:id/proposal/rows/:rowId", async () => {
        running++;
        most = Math.max(most, running);
        await delay(150);
        running--;
        return undefined;
      }),
    );
    open();
    await loaded();
    const row = rowOf(e03);
    await u.selectOptions(row.getByRole("combobox", { name: `Season of ${e03}` }), "Season 2");
    // The first check is still running.
    expect(row.getByText("Checking…")).toBeVisible();
    await u.selectOptions(row.getByRole("combobox", { name: `Quality of ${e03}` }), "Bluray-1080p");
    await u.click(row.getByRole("checkbox", { name: `Skip ${e03}` }));
    // Both are shown at once.
    expect(row.getByRole("combobox", { name: `Quality of ${e03}` })).toHaveDisplayValue(
      "Bluray-1080p",
    );
    await waitFor(() => expect(edits(sent)).toHaveLength(2), { timeout: 3000 });
    await waitFor(() => expect(rowOf(e03).queryByText("Checking…")).not.toBeInTheDocument(), {
      timeout: 3000,
    });
    expect(edits(sent).map((s) => s.body)).toEqual([
      { seasonNumber: 2 },
      { qualityId: 7, skip: true },
    ]);
    expect(most).toBe(1);
    expect(rowOf(e03).getByRole("combobox", { name: `Season of ${e03}` })).toHaveDisplayValue(
      "Season 2",
    );
    expect(rowOf(e03).getByRole("combobox", { name: `Quality of ${e03}` })).toHaveDisplayValue(
      "Bluray-1080p",
    );
    expect(rowOf(e03).getByRole("checkbox", { name: `Skip ${e03}` })).toBeChecked();
  });

  it("keeps a failed edit visible and retries it", async () => {
    const u = user();
    const sent = record();
    failNextEdit(mappingJobId, 502, "arr_unavailable");
    open();
    await loaded();
    await u.selectOptions(
      rowOf(e03).getByRole("combobox", { name: `Quality of ${e03}` }),
      "Bluray-1080p",
    );
    const alert = await screen.findByText("The change was not checked.");
    expect(alert).toBeVisible();
    expect(rowOf(e03).getByRole("combobox", { name: `Quality of ${e03}` })).toHaveDisplayValue(
      "Bluray-1080p",
    );
    expect(screen.getByText(/Wait for the checks to finish/)).toBeVisible();
    await u.click(rowOf(e03).getByRole("button", { name: "Try again" }));
    await waitFor(() =>
      expect(screen.queryByText("The change was not checked.")).not.toBeInTheDocument(),
    );
    expect(edits(sent).map((s) => s.body)).toEqual([{ qualityId: 7 }, { qualityId: 7 }]);
    expect(rowOf(e03).getByRole("combobox", { name: `Quality of ${e03}` })).toHaveDisplayValue(
      "Bluray-1080p",
    );
  });

  it("goes back to the app's verdict when the user undoes a failed edit", async () => {
    const u = user();
    failNextEdit(mappingJobId, 502, "arr_unavailable");
    open();
    await loaded();
    await u.selectOptions(
      rowOf(e03).getByRole("combobox", { name: `Quality of ${e03}` }),
      "Bluray-1080p",
    );
    await u.click(await screen.findByRole("button", { name: "Undo my change" }));
    expect(rowOf(e03).getByRole("combobox", { name: `Quality of ${e03}` })).toHaveDisplayValue(
      "WEBDL-1080p",
    );
    expect(screen.getByRole("link", { name: "Continue" })).toBeVisible();
  });

  it("reloads the rows on a 409 and lets the user try the edit again", async () => {
    const u = user();
    const sent = record();
    failNextEdit(mappingJobId, 409, "wrong_state");
    open();
    await loaded();
    await u.click(rowOf(e03).getByRole("checkbox", { name: `Skip ${e03}` }));
    expect(
      await screen.findByText(/The files changed while you were editing, so the list was reloaded/),
    ).toBeVisible();
    // The proposal was read again, and the edit is still shown.
    await waitFor(() => expect(sent.filter((s) => s.method === "GET").length).toBeGreaterThan(1));
    expect(rowOf(e03).getByRole("checkbox", { name: `Skip ${e03}` })).toBeChecked();
    await u.click(rowOf(e03).getByRole("button", { name: "Try again" }));
    await waitFor(() => expect(summary().getByText("2 skipped")).toBeVisible());
    expect(edits(sent).map((s) => s.body)).toEqual([{ skip: true }, { skip: true }]);
  });

  it("says when a bulk edit failed and retries it", async () => {
    const u = user();
    const sent = record();
    failNextEdit(mappingJobId, 502, "arr_unavailable");
    open();
    await loaded();
    await u.click(screen.getByRole("checkbox", { name: `Select ${e03}` }));
    await u.click(screen.getByRole("button", { name: "Move episodes" }));
    expect(
      await screen.findByText("The change to the selected files was not checked."),
    ).toBeVisible();
    await u.click(screen.getAllByRole("button", { name: "Try again" })[0] as HTMLElement);
    await waitFor(() =>
      expect(
        screen.queryByText("The change to the selected files was not checked."),
      ).not.toBeInTheDocument(),
    );
    expect(edits(sent).map((s) => s.body)).toEqual([
      { rowIds: ["r2"], shift: 1 },
      { rowIds: ["r2"], shift: 1 },
    ]);
    expect(rowOf(e03).getByRole("combobox", { name: `Episode of ${e03}` })).toHaveDisplayValue(
      "4. Fog Signals",
    );
  });

  it("does not accept a shift of nothing", async () => {
    const u = user();
    open();
    await loaded();
    await u.click(screen.getByRole("checkbox", { name: `Select ${e03}` }));
    await u.clear(screen.getByRole("spinbutton", { name: "Move episodes by" }));
    expect(screen.getByRole("button", { name: "Move episodes" })).toBeDisabled();
  });
});

describe("selection", () => {
  it("selects all the files shown and clears the selection", async () => {
    const u = user();
    open();
    await loaded();
    await u.click(screen.getByRole("checkbox", { name: "Select all the files shown" }));
    expect(screen.getByText("5 files selected")).toBeVisible();
    await u.click(screen.getByRole("button", { name: "Clear selection" }));
    expect(screen.getByText(/Select files to move their episodes/)).toBeVisible();
    expect(screen.getByRole("checkbox", { name: `Select ${e03}` })).not.toBeChecked();
  });

  it("selects only the rows the filter shows", async () => {
    const u = user();
    open();
    await loaded();
    await u.click(screen.getByRole("checkbox", { name: "Only files that need attention" }));
    await u.click(screen.getByRole("checkbox", { name: "Select all the files shown" }));
    expect(screen.getByText("2 files selected")).toBeVisible();
  });
});

describe("keyboard and small screens", () => {
  it("reaches every control of a row with Tab, in reading order, and keeps the focus while it checks", async () => {
    const u = user();
    const sent = record();
    open();
    await loaded();
    const order = [
      screen.getByRole("checkbox", { name: `Select ${e03}` }),
      screen.getByRole("combobox", { name: `Season of ${e03}` }),
      screen.getByRole("combobox", { name: `Episode of ${e03}` }),
      screen.getByRole("combobox", { name: `Second episode of ${e03}` }),
      screen.getByRole("combobox", { name: `Quality of ${e03}` }),
      screen.getByRole("checkbox", { name: `Skip ${e03}` }),
    ];
    order[0]?.focus();
    for (const control of order) {
      expect(control).toHaveFocus();
      await u.tab();
    }
    // A checkbox changes with the space bar; the table does not remount it on the answer.
    const skip = screen.getByRole("checkbox", { name: `Skip ${e03}` });
    skip.focus();
    await u.keyboard(" ");
    await waitFor(() => expect(edits(sent)).toHaveLength(1));
    expect(edits(sent)[0]?.body).toEqual({ skip: true });
    await waitFor(() => expect(rowOf(e03).queryByText("Checking…")).not.toBeInTheDocument());
    expect(screen.getByRole("checkbox", { name: `Skip ${e03}` })).toHaveFocus();

    // The bulk controls and the filter are native controls in the same order of the page.
    for (const name of ["Only files that need attention", "Select all the files shown"]) {
      screen.getByRole("checkbox", { name }).focus();
      await u.keyboard(" ");
    }
    expect(screen.getByText("2 files selected")).toBeVisible();
  });

  it("is a list of cards on a narrow screen, with the same controls", async () => {
    vi.stubGlobal("matchMedia", (query: string) => ({
      matches: false,
      media: query,
      addEventListener: () => {},
      removeEventListener: () => {},
    }));
    const u = user();
    const sent = record();
    open();
    await loaded();
    expect(screen.queryByRole("table")).not.toBeInTheDocument();
    const list = screen.getByRole("list", { name: "Files and where they will go" });
    const cards = Array.from(list.children) as HTMLElement[];
    expect(cards).toHaveLength(5);
    const card = within(cards[2] as HTMLElement);
    expect(card.getByText(e05)).toBeVisible();
    await u.selectOptions(
      card.getByRole("combobox", { name: `Episode of ${e05}` }),
      "4. Fog Signals",
    );
    await waitFor(() => expect(edits(sent)).toHaveLength(1));
    expect(edits(sent)[0]?.body).toEqual({ episodeIds: [104] });
    expect(screen.getByRole("link", { name: "Continue" })).toBeVisible();
  });

  it("is available in Spanish", async () => {
    localStorage.setItem("fukku.lang", "es");
    open();
    expect(await screen.findByRole("heading", { name: "Revisar los archivos" })).toBeVisible();
    expect(screen.getByText("2 para importar")).toBeVisible();
    expect(screen.getByText("1 omitido")).toBeVisible();
    expect(screen.getByText("2 necesitan atención")).toBeVisible();
    expect(
      screen.getByRole("checkbox", { name: "Solo los archivos que necesitan atención" }),
    ).toBeVisible();
    expect(screen.getByRole("combobox", { name: `Temporada de ${e01}` })).toBeVisible();
    expect(screen.getByRole("link", { name: "Continuar" })).toBeVisible();
  });
});

describe("a film", () => {
  it("explains duplicate_film, and shows a language warning from the facts it carries", async () => {
    server.use(
      http.get("/api/jobs/:id/proposal", () =>
        HttpResponse.json({
          state: "ready",
          app: "radarr",
          target: { titleId: 20, title: "Sintel", year: 2010 },
          rows: ["a", "b"].map((n, i) => ({
            id: `r${i}`,
            fileId: i,
            name: `Sintel.${n}.mkv`,
            durationMs: 888_000,
            skip: false,
            quality: null,
            languages: [],
            customFormats: [],
            customFormatScore: 0,
            rejections: [],
            warnings: [
              { code: "duplicate_film" },
              { code: "language", customFormats: ["Dub"], score: -50, minFormatScore: 10 },
            ],
            importable: false,
          })),
          hints: [],
          qualities: [],
          languages: [],
        }),
      ),
    );
    open(mappingFilmJobId);
    await loaded();
    expect(screen.getAllByText(/A film has one file/)).toHaveLength(2);
    expect(
      screen.getAllByText(/Score: -50; the quality profile asks for at least 10/),
    ).toHaveLength(2);
    expect(screen.getAllByText(/Rules that matched: Dub\./)).toHaveLength(2);
  });

  it("shows its file with the quality and the skip, and nothing about seasons", async () => {
    const u = user();
    const sent = record();
    open(mappingFilmJobId);
    await loaded();
    const file = "Sintel.2010.1080p.mkv";
    expect(screen.queryByRole("combobox", { name: /Season/ })).not.toBeInTheDocument();
    expect(screen.queryByText(/come from the file names/)).not.toBeInTheDocument();
    expect(screen.getByText("14m 48s")).toBeVisible();
    await u.selectOptions(
      screen.getByRole("combobox", { name: `Quality of ${file}` }),
      "Bluray-1080p",
    );
    await waitFor(() => expect(edits(sent)).toHaveLength(1));
    expect(edits(sent)[0]?.body).toEqual({ qualityId: 7 });
    expect(summary().getByText("1 to import")).toBeVisible();
  });
});

describe("the page", () => {
  it("waits while the app is looking at the files", async () => {
    let calls = 0;
    server.use(
      http.get("/api/jobs/:id/proposal", () => {
        calls++;
        if (calls === 1) {
          return HttpResponse.json({
            state: "pending",
            app: "sonarr",
            target: { titleId: 12, title: "Lighthouse Letters", year: 2019 },
            rows: [],
            hints: [],
            qualities: [],
            languages: [],
          });
        }
        return undefined;
      }),
    );
    open();
    expect(
      await screen.findByRole("heading", { name: "Sonarr is looking at the files" }),
    ).toBeVisible();
    expect(
      await screen.findByRole("heading", { name: "Check the files" }, { timeout: 5000 }),
    ).toBeVisible();
    expect(screen.getByText(e01)).toBeVisible();
  });

  it("says so when the job is not waiting for its files to be checked", async () => {
    server.use(
      http.get("/api/jobs/:id/proposal", () =>
        HttpResponse.json({ error: { code: "wrong_state", message: "x" } }, { status: 409 }),
      ),
    );
    open();
    expect(
      await screen.findByRole("heading", { name: "The files cannot be checked now" }),
    ).toBeVisible();
    expect(screen.getByRole("link", { name: "Open the upload" })).toBeVisible();
  });

  it("says so when the job is not found", async () => {
    open("0".repeat(32));
    expect(await screen.findByRole("heading", { name: "This upload was not found" })).toBeVisible();
  });

  it("says when the app does not answer, and tries again", async () => {
    const u = user();
    server.use(
      http.get("/api/jobs/:id/proposal", () =>
        HttpResponse.json({ error: { code: "arr_unavailable", message: "x" } }, { status: 502 }),
      ),
    );
    open();
    expect(
      await screen.findByText(/Radarr or Sonarr cannot be reached/, {}, { timeout: 4000 }),
    ).toBeVisible();
    server.resetHandlers();
    await u.click(screen.getByRole("button", { name: "Try again" }));
    expect(await loaded()).toBeVisible();
  });
});
