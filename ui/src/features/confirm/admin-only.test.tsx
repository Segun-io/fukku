import { screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { HttpResponse, http } from "msw";
import { afterEach, describe, expect, it } from "vitest";
import type { Proposal, ProposalRow, SeasonDub } from "@/api/proposal";
import type { ConfirmRequest, JobDetail } from "@/api/types";
import { jobDetail } from "@/mocks/fixtures";
import { flow2Fixture, flow2JobId, flow2Jobs, flow2Target } from "@/mocks/flow2-fixtures";
import { applyScenarios } from "@/mocks/handlers";
import { mappingFilmJobId } from "@/mocks/mapping-fixtures";
import { mappingJobs } from "@/mocks/scenarios";
import { server } from "@/mocks/server";
import { renderApp } from "@/test/render";

// Changes to the library are admin-only by default (#116): the proposal and the job say what
// this user may not confirm (`admin_required`), and the pages leave it out and say why.

afterEach(() => server.events.removeAllListeners());

const english = { id: 1, name: "English" };
const quality = { id: 3, name: "WEBDL-1080p" };
const adminOnly = [{ offer: "override", reason: "admin_required" }];

const rejected: ProposalRow = {
  id: "r1",
  fileId: 301,
  name: "Sintel.2010.1080p.mkv",
  durationMs: 888_000,
  skip: false,
  quality: { id: 1, name: "SDTV" },
  languages: [english],
  customFormats: [],
  customFormatScore: 0,
  rejections: [{ reason: "Quality SDTV is not wanted in the profile HD-1080p", permanent: false }],
  warnings: [],
  importable: false,
};

function serve(over: Partial<Proposal>, app: "radarr" | "sonarr" = "radarr") {
  const proposal: Proposal = {
    state: "ready",
    app,
    target: { titleId: 20, title: "Sintel", year: 2010 },
    rows: [rejected],
    hints: [],
    qualities: [{ id: 1, name: "SDTV" }, quality],
    languages: [english],
    ...over,
  };
  const base = mappingJobs.find((j) => j.id === mappingFilmJobId);
  const detail: JobDetail = {
    ...jobDetail(base as NonNullable<typeof base>),
    target: { app, titleId: 20, title: "Sintel", year: 2010 },
  };
  server.use(
    http.get("/api/jobs/:id", () => HttpResponse.json(detail)),
    http.get("/api/jobs/:id/proposal", () => HttpResponse.json(proposal)),
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
  return renderApp(`/jobs/${mappingFilmJobId}/confirm`);
};

describe("overrides only an admin may confirm", () => {
  it("has no “Import anyway” tick, says why, and points at leaving the file out", async () => {
    serve({ notOffered: adminOnly });
    const bodies = recordConfirm();
    const user = userEvent.setup();
    open();

    expect(await screen.findByText("Read this before you import")).toBeVisible();
    expect(screen.queryByRole("checkbox", { name: "Import anyway" })).not.toBeInTheDocument();
    expect(
      screen.getByText(/^Radarr refused this file, or it would replace a file in the library/),
    ).toBeVisible();

    await user.click(screen.getByRole("button", { name: "Import" }));
    expect(await screen.findByRole("alert")).toHaveTextContent(
      "Only an admin can import files that Radarr refused or that would replace a file in the library. Leave them out to import the rest",
    );
    expect(screen.getByRole("button", { name: "Leave this file out" })).toHaveFocus();
    expect(bodies).toHaveLength(0);
  });

  it("keeps the tick for Fukku's own warnings on a new file", async () => {
    serve({
      notOffered: adminOnly,
      rows: [
        {
          ...rejected,
          quality,
          rejections: [],
          warnings: [{ code: "language", customFormats: [], score: -10, minFormatScore: 0 }],
        },
      ],
    });
    const bodies = recordConfirm();
    const user = userEvent.setup();
    open();
    await user.click(await screen.findByRole("checkbox", { name: "Import anyway" }));
    expect(screen.queryByText(/so only an admin can import it anyway/)).not.toBeInTheDocument();
    await user.click(screen.getByRole("button", { name: "Import" }));
    await waitFor(() => expect(bodies).toHaveLength(1));
    expect(bodies[0]?.overrides).toEqual(["r1"]);
  });

  it("offers to leave out the rows of a 403 admin_required", async () => {
    serve({ notOffered: [] });
    let skipped = "";
    server.use(
      http.post("/api/jobs/:id/confirm", () =>
        HttpResponse.json(
          { error: { code: "admin_required", message: "m", rows: ["r1"] } },
          { status: 403 },
        ),
      ),
      http.patch("/api/jobs/:id/proposal/rows/:rowId", async ({ params, request }) => {
        skipped = `${params.rowId} ${JSON.stringify(await request.json())}`;
        return HttpResponse.json({ ...rejected, skip: true, skippedBy: "user" });
      }),
    );
    const user = userEvent.setup();
    open();
    await user.click(await screen.findByRole("checkbox", { name: "Import anyway" }));
    await user.click(screen.getByRole("button", { name: "Import" }));
    expect(
      await screen.findByText(/You can leave this file out and import the rest/),
    ).toBeVisible();
    await user.click(screen.getByRole("button", { name: "Leave out 1 file" }));
    await waitFor(() => expect(skipped).toBe('r1 {"skip":true}'));
    expect(
      await screen.findByText("1 file left out. Press Import to confirm again."),
    ).toBeVisible();
  });

  it("keeps the tick for an admin", async () => {
    serve({ notOffered: [] });
    open();
    expect(await screen.findByRole("checkbox", { name: "Import anyway" })).toBeVisible();
    expect(screen.queryByText(/Only an admin can import/)).not.toBeInTheDocument();
  });

  it("shows the server's admin_required refusal in words", async () => {
    serve({ notOffered: [] });
    server.use(
      http.post("/api/jobs/:id/confirm", () =>
        HttpResponse.json(
          { error: { code: "admin_required", message: "m", rows: ["r1"] } },
          { status: 403 },
        ),
      ),
    );
    const user = userEvent.setup();
    open();
    await user.click(await screen.findByRole("checkbox", { name: "Import anyway" }));
    await user.click(screen.getByRole("button", { name: "Import" }));
    await waitFor(() =>
      expect(
        screen.getByText(/Only an admin can make this change to the library on this server/),
      ).toBeVisible(),
    );
  });
});

describe("season-dub policies only an admin may choose", () => {
  const existing: ProposalRow = {
    ...rejected,
    id: "r2",
    name: "Lighthouse.Letters.S01E07.Spanish.mkv",
    skip: true,
    skippedBy: "server",
    seasonNumber: 1,
    episodes: [{ id: 107, number: 7, title: "Episode 7", runtimeMs: 1_320_000, hasFile: true }],
    quality,
    rejections: [{ reason: "Not an upgrade for existing episode file(s)", permanent: true }],
    warnings: [{ code: "existing_file" }],
  };
  const fresh: ProposalRow = {
    ...existing,
    id: "r3",
    name: "Lighthouse.Letters.S01E08.Spanish.mkv",
    skip: false,
    skippedBy: undefined,
    episodes: [{ id: 108, number: 8, title: "Episode 8", runtimeMs: 1_320_000, hasFile: false }],
    rejections: [],
    warnings: [],
    importable: true,
  };
  const dub = (over: Partial<SeasonDub>): SeasonDub => ({
    policy: "ask",
    ask: true,
    offers: ["subtitlesOnly"],
    seasons: [{ season: 1, episodes: 2, existing: 1, new: 1 }],
    notOffered: [
      { offer: "replace", reason: "admin_required" },
      { offer: "version", reason: "admin_required" },
    ],
    ...over,
  });

  it("offers subtitles only, and says that replacing needs an admin", async () => {
    serve({ rows: [existing, fresh], seasonDub: dub({}), notOffered: adminOnly }, "sonarr");
    open();
    const heading = await screen.findByRole("heading", {
      name: "Episodes that already have a file",
    });
    const section = heading.closest("section") as HTMLElement;
    expect(within(section).queryByRole("radio", { name: /Replace the files/ })).toBeNull();
    expect(within(section).getByRole("radio", { name: /Only add subtitles/ })).toBeVisible();
    expect(
      within(section).getByText(
        /^Replace the files: it changes a file the library already has, so only an admin can confirm it/,
      ),
    ).toBeVisible();
  });

  it("with nothing left to offer, leaves the episodes out and still says why", async () => {
    serve(
      {
        rows: [existing, fresh],
        seasonDub: dub({ ask: false, offers: [] }),
        notOffered: adminOnly,
      },
      "sonarr",
    );
    open();
    expect(
      await screen.findByText(
        "Nothing is offered for these episodes on this server, so they are left out.",
      ),
    ).toBeVisible();
    expect(screen.getAllByText(/so only an admin can confirm it/)).toHaveLength(2);
  });
});

describe("flow 2 options only an admin may confirm", () => {
  const show = (notOffered: { offer: "merge" | "version"; reason: string }[]) => {
    const base = flow2Jobs.find((j) => j.id === flow2JobId);
    const detail: JobDetail = {
      ...jobDetail(base as NonNullable<typeof base>),
      target: { ...flow2Target },
      flow2: flow2Fixture("match", { offers: [], notOffered, version: null }),
    };
    applyScenarios(["flow2"]);
    server.use(http.get("/api/jobs/:id", () => HttpResponse.json(detail)));
    renderApp(`/jobs/${flow2JobId}/flow2`);
  };

  it("says that an admin must confirm the upload, and offers neither", async () => {
    show([
      { offer: "merge", reason: "admin_required" },
      { offer: "version", reason: "admin_required" },
    ]);
    expect(
      await screen.findByRole("heading", { name: "An admin must confirm this upload" }),
    ).toBeVisible();
    expect(
      screen.getByText(/^Merging into the film’s file and keeping a second version/),
    ).toBeVisible();
    expect(
      screen.getByText(
        /^Merge the audio into the library file: it changes a file the library already has/,
      ),
    ).toBeVisible();
    expect(screen.getByText(/^Keep it as a second version: it changes a file/)).toBeVisible();
    expect(screen.queryByRole("button", { name: /Merge|Make the clips/ })).not.toBeInTheDocument();
  });

  it("names only the option an admin would be offered", async () => {
    show([
      { offer: "merge", reason: "admin_required" },
      { offer: "version", reason: "versions_disabled" },
    ]);
    expect(
      await screen.findByText(
        "Merging into the film’s file changes the library, which only an admin can do on this server. Nothing was changed.",
      ),
    ).toBeVisible();
    expect(screen.queryByText(/keeping a second version/)).not.toBeInTheDocument();
  });
});
