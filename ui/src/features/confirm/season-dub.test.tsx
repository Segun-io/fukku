import { screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { HttpResponse, http } from "msw";
import { afterEach, describe, expect, it } from "vitest";
import type { Proposal, ProposalRow, SeasonDub } from "@/api/proposal";
import type { ConfirmRequest, JobDetail } from "@/api/types";
import { jobDetail } from "@/mocks/fixtures";
import { applyScenarios } from "@/mocks/handlers";
import { mappingJobId } from "@/mocks/mapping-fixtures";
import { mappingJobs } from "@/mocks/scenarios";
import { server } from "@/mocks/server";
import { renderApp } from "@/test/render";

afterEach(() => server.events.removeAllListeners());

const english = { id: 1, name: "English" };
const quality = { id: 3, name: "WEBDL-1080p" };
const episode = (id: number, number: number, hasFile: boolean) => ({
  id,
  number,
  title: `Episode ${number}`,
  runtimeMs: 1_320_000,
  hasFile,
});

/** Episode 7 already has a file: the server leaves its row out until a policy takes it. */
const existing: ProposalRow = {
  id: "r1",
  fileId: 301,
  name: "Lighthouse.Letters.S01E07.Spanish.mkv",
  durationMs: 1_320_000,
  skip: true,
  seasonNumber: 1,
  episodes: [episode(107, 7, true)],
  quality,
  languages: [english],
  customFormats: [],
  customFormatScore: 0,
  rejections: [{ reason: "Not an upgrade for existing episode file(s)", permanent: true }],
  warnings: [{ code: "existing_file" }],
  importable: false,
};

const fresh: ProposalRow = {
  id: "r2",
  fileId: 302,
  name: "Lighthouse.Letters.S01E08.Spanish.mkv",
  durationMs: 1_320_000,
  skip: false,
  seasonNumber: 1,
  episodes: [episode(108, 8, false)],
  quality,
  languages: [english],
  customFormats: [],
  customFormatScore: 0,
  rejections: [],
  warnings: [],
  importable: true,
};

const asking: SeasonDub = {
  policy: "ask",
  ask: true,
  offers: ["replace", "subtitlesOnly"],
  seasons: [{ season: 1, episodes: 2, existing: 1, new: 1 }],
};

function serve(rows: ProposalRow[], dub: SeasonDub | undefined) {
  const proposal: Proposal = {
    state: "ready",
    app: "sonarr",
    target: { titleId: 12, title: "Lighthouse Letters", year: 2019 },
    rows,
    hints: [],
    seasonDub: dub,
    qualities: [quality],
    languages: [english],
  };
  const base = mappingJobs.find((j) => j.id === mappingJobId);
  const detail: JobDetail = {
    ...jobDetail(base as NonNullable<typeof base>),
    target: { app: "sonarr", titleId: 12, title: "Lighthouse Letters", year: 2019 },
  };
  server.use(
    http.get("/api/jobs/:id", () => HttpResponse.json(detail)),
    http.get("/api/jobs/:id/proposal", () => HttpResponse.json(proposal)),
    // The server's own answer to a confirmation it accepts.
    http.post("/api/jobs/:id/confirm", () =>
      HttpResponse.json({ ...detail, state: "importing" }, { status: 202 }),
    ),
  );
}

function recordConfirm(): ConfirmRequest[] {
  const bodies: ConfirmRequest[] = [];
  server.events.on("request:start", async ({ request }) => {
    if (request.method === "POST" && request.url.endsWith("/confirm")) {
      bodies.push((await request.clone().json()) as ConfirmRequest);
    }
  });
  return bodies;
}

const open = () => {
  applyScenarios(["mapping"]);
  return renderApp(`/jobs/${mappingJobId}/confirm`);
};
const importButton = () => screen.findByRole("button", { name: "Import" });

describe("the season-dub choice", () => {
  it("lists the seasons and each option with what it does", async () => {
    serve([existing, fresh], asking);
    open();
    const heading = await screen.findByRole("heading", {
      name: "Episodes that already have a file",
    });
    const section = heading.closest("section") as HTMLElement;
    expect(
      within(section).getByText("Season 1: files already exist for 1 of 2 episodes."),
    ).toBeVisible();
    const group = within(section).getByRole("group", { name: "What should happen to them?" });
    expect(within(group).getByRole("radio", { name: /Replace the files/ })).not.toBeChecked();
    expect(group).toHaveTextContent("left unmonitored so automatic upgrades do not replace them");
    expect(group).toHaveTextContent("Search button of an episode in Sonarr still can");
    expect(within(group).getByRole("radio", { name: /Only add subtitles/ })).toBeVisible();
    expect(group).toHaveTextContent("Nothing is imported.");
  });

  it("does not import until a choice is made", async () => {
    const bodies = recordConfirm();
    const user = userEvent.setup();
    serve([existing, fresh], asking);
    open();
    await user.click(await importButton());
    const alert = await screen.findByText(
      "Choose what to do with the episodes that already have a file.",
    );
    expect(alert).toBeVisible();
    expect(screen.getByRole("radio", { name: /Replace the files/ })).toHaveFocus();
    expect(bodies).toEqual([]);
  });

  it("replaces: the row is in, the choice answers its warning, and the request carries it", async () => {
    const bodies = recordConfirm();
    const user = userEvent.setup();
    serve([existing, fresh], asking);
    const { router } = open();
    await screen.findByText("1 file will be imported");
    await user.click(await screen.findByRole("radio", { name: /Replace the files/ }));
    expect(await screen.findByText("2 files will be imported")).toBeVisible();
    // Nothing to tick for the row the choice covers.
    expect(screen.queryByRole("checkbox", { name: "Import anyway" })).not.toBeInTheDocument();
    await user.click(await importButton());
    await waitFor(() => expect(router.state.location.pathname).toBe(`/jobs/${mappingJobId}`));
    expect(bodies).toEqual([{ overrides: [], subtitles: [], seasonDub: "replace" }]);
  });

  it("still asks for any other problem of a row that is replaced", async () => {
    const bodies = recordConfirm();
    const user = userEvent.setup();
    serve(
      [{ ...existing, warnings: [...existing.warnings, { code: "runtime_mismatch" }] }, fresh],
      asking,
    );
    open();
    await user.click(await screen.findByRole("radio", { name: /Replace the files/ }));
    await user.click(await importButton());
    expect(
      await screen.findByText(/Tick “Import anyway” for each file with a problem/),
    ).toBeVisible();
    expect(bodies).toEqual([]);
    await user.click(screen.getByRole("checkbox", { name: "Import anyway" }));
    await user.click(await importButton());
    await waitFor(() => expect(bodies).toHaveLength(1));
    expect(bodies[0]).toMatchObject({ overrides: ["r1"], seasonDub: "replace" });
  });

  it("adds only the subtitles when nothing else is left to import", async () => {
    const bodies = recordConfirm();
    const user = userEvent.setup();
    serve([existing], asking);
    const { router } = open();
    expect(
      await screen.findByText("Every file is left out, so there is nothing to import."),
    ).toBeVisible();
    expect(await importButton()).toBeDisabled();
    await user.click(await screen.findByRole("radio", { name: /Only add subtitles/ }));
    expect(
      screen.getByText("Nothing will be imported. The subtitles go to the subtitle manager."),
    ).toBeVisible();
    await user.click(await importButton());
    await waitFor(() => expect(router.state.location.pathname).toBe(`/jobs/${mappingJobId}`));
    expect(bodies[0]).toMatchObject({ seasonDub: "subtitlesOnly" });
  });

  it("only tells what the server's own policy does, and sends no choice", async () => {
    const bodies = recordConfirm();
    const user = userEvent.setup();
    serve([existing, fresh], {
      policy: "replace",
      ask: false,
      decided: "replace",
      offers: ["replace"],
      seasons: asking.seasons,
    });
    open();
    expect(
      await screen.findByText(/This server settles it by itself: Replace the files\./),
    ).toBeVisible();
    expect(screen.queryByRole("radio")).not.toBeInTheDocument();
    await user.click(await importButton());
    await waitFor(() => expect(bodies).toHaveLength(1));
    expect(bodies[0]).not.toHaveProperty("seasonDub");
    expect(bodies[0]?.overrides).toEqual([]);
  });

  it("says the episodes are left out when the server offers nothing for them", async () => {
    serve([existing, fresh], { policy: "ask", ask: false, offers: [], seasons: asking.seasons });
    open();
    expect(
      await screen.findByText(
        /Nothing is offered for these episodes on this server, so they are left out\./,
      ),
    ).toBeVisible();
    expect(screen.queryByRole("radio")).not.toBeInTheDocument();
  });

  // `version` (#58, #94) is built in #97: until then the page does not offer it.
  it("does not offer the version policy even when the server lists it", async () => {
    serve([existing, fresh], { ...asking, offers: ["replace", "subtitlesOnly", "version"] });
    open();
    expect(await screen.findByRole("radio", { name: /Replace the files/ })).toBeVisible();
    expect(screen.queryByRole("radio", { name: /second versions/ })).not.toBeInTheDocument();
    expect(screen.getAllByRole("radio")).toHaveLength(2);
  });

  it.each([
    ["series_folder_unusable", /folder name for the series cannot be part of a file name/],
    ["season_folder_unavailable", /season folder cannot take the versions/],
    ["media_server_too_old", /older than 12\.4/],
    ["something_new", /not possible here \(code: something_new\)/],
  ])("says why the version policy is not offered: %s", async (reason, text) => {
    serve([existing, fresh], { ...asking, notOffered: [{ offer: "version", reason }] });
    open();
    expect(await screen.findByText(/Second versions|Keep as second versions/)).toBeVisible();
    expect(screen.getByText(text)).toBeVisible();
  });

  it("asks for no choice when the only option is one it cannot offer", async () => {
    const bodies = recordConfirm();
    const user = userEvent.setup();
    serve([existing, fresh], { ...asking, offers: ["version"] });
    open();
    expect(await screen.findByText(/Nothing is offered for these episodes/)).toBeVisible();
    expect(screen.queryByRole("radio")).not.toBeInTheDocument();
    await user.click(await importButton());
    await waitFor(() => expect(bodies).toHaveLength(1));
    expect(bodies[0]).not.toHaveProperty("seasonDub");
  });

  it("names an option it does not know instead of hiding it", async () => {
    serve([existing, fresh], { ...asking, offers: ["merge", "replace"] });
    open();
    expect(await screen.findByRole("radio", { name: /Another option \(merge\)/ })).toBeVisible();
  });

  it("asks before replacing a file that holds episodes outside the upload", async () => {
    const bodies = recordConfirm();
    const user = userEvent.setup();
    serve([existing, fresh], asking);
    open();
    await user.click(await screen.findByRole("radio", { name: /Replace the files/ }));
    server.use(
      http.post("/api/jobs/:id/confirm", async ({ request }) => {
        const body = (await request.json()) as ConfirmRequest;
        bodies.push(body);
        return body.overrides.includes("r1")
          ? HttpResponse.json({ error: { code: "internal", message: "stop" } }, { status: 500 })
          : HttpResponse.json(
              { error: { code: "shared_file", message: "x", rows: ["r1"] } },
              { status: 409 },
            );
      }),
    );
    await user.click(await importButton());
    expect(
      await screen.findByText("Replacing these files would remove other episodes’ files"),
    ).toBeVisible();
    expect(
      screen.getByText(/Replacing some of these files would also remove the file/),
    ).toBeVisible();
    await user.click(screen.getByRole("checkbox", { name: `Replace ${existing.name} anyway` }));
    await user.click(await importButton());
    await waitFor(() => expect(bodies.at(-1)?.overrides).toEqual(["r1"]));
  });

  it("explains a missing choice the server refused", async () => {
    const user = userEvent.setup();
    serve([existing, fresh], asking);
    open();
    await user.click(await screen.findByRole("radio", { name: /Only add subtitles/ }));
    server.use(
      http.post("/api/jobs/:id/confirm", () =>
        HttpResponse.json(
          { error: { code: "season_dub_choice", message: "x", rows: ["r1"] } },
          { status: 409 },
        ),
      ),
    );
    await user.click(await importButton());
    expect(
      await screen.findByText("Some episodes already have a file. Choose what to do with them."),
    ).toBeVisible();
  });

  it("shows nothing for an upload without such episodes", async () => {
    serve([fresh], undefined);
    open();
    expect(await importButton()).toBeEnabled();
    expect(
      screen.queryByRole("heading", { name: "Episodes that already have a file" }),
    ).not.toBeInTheDocument();
  });
});
