import { screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { HttpResponse, http } from "msw";
import { afterEach, describe, expect, it } from "vitest";
import type { Proposal, ProposalRow, SeasonDub } from "@/api/proposal";
import type { ConfirmRequest, JobDetail } from "@/api/types";
import { versionDub, versionRow, versionView } from "@/mocks/episode-version-fixtures";
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

/** Episode 7 already has a file: the server keeps its row out of the import. */
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

function serve(rows: ProposalRow[], dub: SeasonDub) {
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

/** The server's refusal of a confirmation, in its JSON. */
function refuse(status: number, code: string, rows?: string[]) {
  server.use(
    http.post("/api/jobs/:id/confirm", () =>
      HttpResponse.json({ error: { code, message: "x", ...(rows ? { rows } : {}) } }, { status }),
    ),
  );
}

const open = () => {
  applyScenarios(["mapping"]);
  return renderApp(`/jobs/${mappingJobId}/confirm`);
};
const importButton = () => screen.findByRole("button", { name: "Import" });
const chooseVersion = async (user: ReturnType<typeof userEvent.setup>) =>
  user.click(await screen.findByRole("radio", { name: /Keep as second versions/ }));
const labelField = () => screen.findByRole("textbox", { name: "Version label" });
/** The notice that lists the rows a refusal names. */
const refusedNotice = async () =>
  (await screen.findByText("These files cannot be kept as second versions")).closest(
    "[role=alert]",
  ) as HTMLElement;

describe("the season-dub choice: second versions", () => {
  it("offers the version policy with what it does, and shows nothing of it until chosen", async () => {
    const user = userEvent.setup();
    serve([existing, fresh], versionDub());
    open();
    const radio = await screen.findByRole("radio", { name: /Keep as second versions/ });
    expect(radio).not.toBeChecked();
    const group = screen.getByRole("group", { name: "What should happen to them?" });
    expect(group).toHaveTextContent("kept next to it as a second version");
    expect(group).toHaveTextContent("Sonarr does not track the second files");
    expect(screen.getAllByRole("radio")).toHaveLength(3);
    expect(screen.queryByRole("textbox", { name: "Version label" })).not.toBeInTheDocument();
    await user.click(radio);
    expect(await labelField()).toHaveValue("Español");
  });

  it("shows the label, each episode with its name, and the notices", async () => {
    const user = userEvent.setup();
    serve([existing, fresh], versionDub());
    open();
    await chooseVersion(user);
    expect(await labelField()).toHaveValue("Español");
    const heading = screen.getByRole("heading", { name: "Episodes kept as second versions" });
    const list = heading.nextElementSibling as HTMLElement;
    const item = within(list).getByText(existing.name).closest("li") as HTMLElement;
    expect(item).toHaveTextContent("Season 1, episode 7");
    expect(item).toHaveTextContent("Lighthouse Letters (2019) - S01E07 - Español.mkv");
    expect(screen.getByRole("heading", { name: "Good to know" })).toBeVisible();
    expect(
      screen.getByText(/Sonarr does not track the second files\. It never renames/),
    ).toBeVisible();
    expect(screen.getByText(/If an episode’s own file is deleted/)).toBeVisible();
  });

  it("sends the choice and the label, and counts the versions with the files", async () => {
    const bodies = recordConfirm();
    const user = userEvent.setup();
    serve([existing, fresh], versionDub());
    const { router } = open();
    await screen.findByText("1 file will be imported");
    await chooseVersion(user);
    expect(await screen.findByText("1 episode is kept as a second version")).toBeVisible();
    expect(screen.getByText("1 file will be imported")).toBeVisible();
    // The row that is kept as a version is not "left out".
    expect(screen.queryByText(/^\d+ (is|are) left out$/)).not.toBeInTheDocument();
    const field = await labelField();
    await user.clear(field);
    await user.type(field, "Latino");
    await user.click(await importButton());
    await waitFor(() => expect(router.state.location.pathname).toBe(`/jobs/${mappingJobId}`));
    expect(bodies).toEqual([
      { overrides: [], subtitles: [], seasonDub: "version", version: { label: "Latino" } },
    ]);
  });

  it("sends an empty version when the label is cleared, so the server proposes its own", async () => {
    const bodies = recordConfirm();
    const user = userEvent.setup();
    serve([existing, fresh], versionDub());
    open();
    await chooseVersion(user);
    await user.clear(await labelField());
    await user.click(await importButton());
    await waitFor(() => expect(bodies).toHaveLength(1));
    expect(bodies[0]).toMatchObject({ seasonDub: "version", version: {} });
    expect(bodies[0]?.version).toEqual({});
  });

  it("allows the confirmation with no file left in, as subtitles only does", async () => {
    const bodies = recordConfirm();
    const user = userEvent.setup();
    serve([existing], versionDub());
    const { router } = open();
    expect(
      await screen.findByText("Every file is left out, so there is nothing to import."),
    ).toBeVisible();
    expect(await importButton()).toBeDisabled();
    await chooseVersion(user);
    expect(
      screen.getByText("Nothing new will be imported. 1 episode is kept as a second version."),
    ).toBeVisible();
    expect(await importButton()).toBeEnabled();
    await user.click(await importButton());
    await waitFor(() => expect(router.state.location.pathname).toBe(`/jobs/${mappingJobId}`));
    expect(bodies[0]).toMatchObject({ seasonDub: "version", version: { label: "Español" } });
  });

  it("still asks for a choice before importing", async () => {
    const bodies = recordConfirm();
    const user = userEvent.setup();
    serve([existing, fresh], versionDub());
    open();
    await user.click(await importButton());
    expect(
      await screen.findByText("Choose what to do with the episodes that already have a file."),
    ).toBeVisible();
    expect(bodies).toEqual([]);
  });

  it("applies the server's own policy version: the label is asked, the choice is not sent", async () => {
    const bodies = recordConfirm();
    const user = userEvent.setup();
    serve(
      [existing, fresh],
      versionDub({ policy: "version", ask: false, decided: "version", offers: ["version"] }),
    );
    open();
    expect(
      await screen.findByText(/This server settles it by itself: Keep as second versions\./),
    ).toBeVisible();
    expect(screen.queryByText(/Another option/)).not.toBeInTheDocument();
    expect(screen.queryByText(/Nothing is offered for these episodes/)).not.toBeInTheDocument();
    expect(screen.queryByRole("radio")).not.toBeInTheDocument();
    const field = await labelField();
    await user.clear(field);
    await user.type(field, "Dub");
    await user.click(await importButton());
    await waitFor(() => expect(bodies).toHaveLength(1));
    expect(bodies[0]).not.toHaveProperty("seasonDub");
    expect(bodies[0]?.version).toEqual({ label: "Dub" });
  });

  it("explains a label problem of the proposed label, and drops it once the label is changed", async () => {
    const user = userEvent.setup();
    serve(
      [existing, fresh],
      versionDub({
        version: versionView({
          label: "S01E02 Español",
          problem: { reason: "label_episode", message: "x" },
          rows: [versionRow("r1", 107, 1, 7, { name: "" })],
        }),
      }),
    );
    open();
    await chooseVersion(user);
    const notice = (await screen.findByText("This label cannot be used as it is")).closest(
      "[role=alert]",
    ) as HTMLElement;
    expect(notice).toHaveTextContent("must not contain a season or episode number");
    expect(await labelField()).toHaveAttribute("aria-invalid", "true");
    expect(
      screen.getByText("The file name is set from this label when you confirm."),
    ).toBeVisible();
    const field = await labelField();
    await user.clear(field);
    await user.type(field, "Español");
    expect(screen.queryByText("This label cannot be used as it is")).not.toBeInTheDocument();
  });

  it.each([
    ["double_episode", /holds several episodes, so it cannot be a second version/],
    ["shared_file", /also holds other episodes/],
    ["no_file", /Sonarr lists no file for this episode/],
    ["outside_library", /outside the library folders/],
    ["name_too_long", /name would be too long/],
    ["version_exists", /already exists\. Use another label/],
    ["name_unreadable", /would not read as exactly this episode/],
    ["folder_unreadable", /cannot read the episode’s folder/],
    ["something_new", /cannot be a second version \(code: something_new\)/],
  ])("says per row why it cannot be a version: %s", async (problem, text) => {
    const user = userEvent.setup();
    serve(
      [existing, fresh],
      versionDub({
        version: versionView({ rows: [versionRow("r1", 107, 1, 7, { name: "", problem })] }),
      }),
    );
    open();
    await chooseVersion(user);
    const item = (await screen.findByText(existing.name, { selector: "span" })).closest(
      "li",
    ) as HTMLElement;
    expect(within(item).getByText(text)).toBeVisible();
  });

  it.each([
    ["version_upgrade", /take this file for an upgrade of the episode’s own file/],
    ["version_unchecked", /gives no verdict on this file/],
    ["version_exists", /A file or an earlier version with this name already exists\.$/],
  ])("shows the refusal %s with the rows it names", async (code, text) => {
    const user = userEvent.setup();
    serve([existing, fresh], versionDub());
    open();
    await chooseVersion(user);
    refuse(409, code, ["r1"]);
    await user.click(await importButton());
    const notice = await refusedNotice();
    const item = notice.querySelector('[data-refused-row="r1"]') as HTMLElement;
    expect(item).toHaveTextContent(existing.name);
    expect(item).toHaveTextContent("S01E07");
    expect(within(item).getByText(text)).toBeVisible();
    expect(within(notice).getByText(/change the match/)).toBeVisible();
    // The refusal goes away when the label is changed: it was about the old one.
    await user.type(await labelField(), "x");
    expect(
      screen.queryByText("These files cannot be kept as second versions"),
    ).not.toBeInTheDocument();
  });

  it("shows the problem of each row for version_unavailable", async () => {
    const user = userEvent.setup();
    serve(
      [existing, fresh],
      versionDub({
        version: versionView({
          rows: [versionRow("r1", 107, 1, 7, { name: "", problem: "outside_library" })],
        }),
      }),
    );
    open();
    await chooseVersion(user);
    refuse(409, "version_unavailable", ["r1"]);
    await user.click(await importButton());
    const notice = await refusedNotice();
    expect(within(notice).getByText(/outside the library folders/)).toBeVisible();
    expect(
      screen.getByText(/A second version is not possible right now: it is switched off/),
    ).toBeVisible();
  });

  it("keeps the label and the choice after a refusal", async () => {
    const user = userEvent.setup();
    serve([existing, fresh], versionDub());
    open();
    await chooseVersion(user);
    refuse(409, "version_upgrade", ["r1"]);
    await user.click(await importButton());
    await refusedNotice();
    expect(screen.getByRole("radio", { name: /Keep as second versions/ })).toBeChecked();
    expect(await labelField()).toHaveValue("Español");
  });

  it("marks the label when the server refuses it", async () => {
    const user = userEvent.setup();
    serve([existing, fresh], versionDub());
    open();
    await chooseVersion(user);
    refuse(409, "version_label");
    await user.click(await importButton());
    expect(
      await screen.findByText("That label cannot be used. Change it and try again."),
    ).toBeVisible();
    expect(await labelField()).toHaveAttribute("aria-invalid", "true");
  });

  it("says when the storage is not available", async () => {
    const user = userEvent.setup();
    serve([existing, fresh], versionDub());
    open();
    await chooseVersion(user);
    refuse(503, "storage_unavailable");
    await user.click(await importButton());
    expect(
      await screen.findByText("Storage is not available right now. Try again later."),
    ).toBeVisible();
    expect(
      screen.queryByText("These files cannot be kept as second versions"),
    ).not.toBeInTheDocument();
  });
});
