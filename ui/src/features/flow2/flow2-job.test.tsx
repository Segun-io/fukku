import { screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { HttpResponse, http } from "msw";
import { describe, expect, it } from "vitest";
import type { ImportInfo, ImportResult, JobDetail, JobState, MergeInfo } from "@/api/types";
import { jobDetail } from "@/mocks/fixtures";
import { flow2Fixture, flow2JobId, flow2Jobs, flow2Target } from "@/mocks/flow2-fixtures";
import { applyScenarios } from "@/mocks/handlers";
import { server } from "@/mocks/server";
import { renderApp } from "@/test/render";

const slow = { timeout: 14000 };

/** The flow 2 job in `state`, with what the test adds to its detail. */
function serve(state: JobState, over: Partial<JobDetail> = {}) {
  const base = flow2Jobs.find((j) => j.id === flow2JobId);
  const job = { ...(base as NonNullable<typeof base>), state };
  const detail: JobDetail = {
    ...jobDetail(job),
    target: { ...flow2Target },
    flow2: flow2Fixture("match"),
    ...over,
  };
  server.use(http.get("/api/jobs/:id", () => HttpResponse.json(detail)));
  return renderApp(`/jobs/${flow2JobId}`);
}

const merge = (over: Partial<MergeInfo> = {}): MergeInfo => ({
  state: "running",
  refusal: "",
  audioTracks: [2],
  subtitleTracks: [3],
  offsetMs: -800,
  languageTag: "es-419",
  trackName: "Español (Latino)",
  keepOriginalDefault: true,
  arrLanguage: { id: 37, name: "Spanish (Latino)" },
  name: "",
  size: 0,
  ...over,
});

const importOf = (m: MergeInfo, rows: ImportInfo["rows"] = []): ImportInfo => ({
  status: "running",
  rows,
  merge: m,
});

const result = (over: Partial<ImportResult> = {}): ImportResult => ({
  titleLink: null,
  monitored: false,
  unmonitorReason: "merged",
  postProcessing: [],
  merge: {
    name: "Sintel (2010) WEBDL-1080p.mkv",
    warnings: [{ code: "search_can_replace", message: "x" }],
  },
  ...over,
});

describe("the job page of a flow 2 upload", () => {
  it("leads to the comparison instead of the mapping and the review", async () => {
    serve("awaiting-confirmation");
    const link = await screen.findByRole("link", { name: "Compare with the library file" });
    expect(link).toHaveAttribute("href", `/jobs/${flow2JobId}/flow2`);
    expect(screen.queryByRole("link", { name: "Check the files" })).not.toBeInTheDocument();
    expect(screen.queryByRole("link", { name: "Review and import" })).not.toBeInTheDocument();
  });

  it("runs the whole way: compare, merge, progress, result with the B6 warning", async () => {
    const user = userEvent.setup();
    applyScenarios(["flow2"]);
    const { router } = renderApp(`/jobs/${flow2JobId}/flow2`);
    await user.click(await screen.findByRole("button", { name: "Make the clips" }));
    await screen.findByRole("list", { name: "Clips" }, slow);
    await user.click(screen.getByRole("button", { name: "They match, continue" }));
    const form = await screen.findByRole("form", {
      name: "Merge the audio into the library file?",
    });
    await user.click(within(form).getByRole("checkbox"));
    await user.click(within(form).getByRole("button", { name: "Merge into the library file" }));
    await waitFor(() => expect(router.state.location.pathname).toBe(`/jobs/${flow2JobId}`));

    expect(await screen.findByRole("heading", { name: "The merge" })).toBeVisible();
    const done = await screen.findByText("This film is not monitored", undefined, slow);
    expect(done).toBeVisible();
    expect(
      screen.getByText(
        /own Search button in Radarr can still replace it with a release that has no new audio/,
      ),
    ).toBeVisible();
    expect(screen.getByText("The merged file is in the library")).toBeVisible();
  }, 30_000);
});

describe("the progress of a merge", () => {
  const steps = () => screen.getByRole("list", { name: "Steps of the merge" });

  it("waits its turn, then runs", async () => {
    serve("importing", { import: importOf(merge({ state: "pending" })) });
    expect(await screen.findByText("The merge is waiting its turn.")).toBeVisible();
    expect(within(steps()).getAllByText("Waiting")).toHaveLength(2);
  });

  it("says what is added while the worker merges", async () => {
    serve("importing", { import: importOf(merge()) });
    expect(await screen.findByText(/The audio is being merged into a copy/)).toBeVisible();
    const facts = screen.getByText("Audio tracks").closest("dl") as HTMLElement;
    expect(within(facts).getByText("−800 ms".replace("−", "-"))).toBeVisible();
    expect(within(facts).getByText("es-419")).toBeVisible();
    expect(within(facts).getByText("Español (Latino)")).toBeVisible();
  });

  it("shows the merged file while Radarr imports it", async () => {
    serve("importing", {
      import: importOf(
        merge({ state: "merged", name: "Sintel (2010) WEBDL-1080p.mkv", size: 4_300_000_000 }),
        [{ id: "r1", name: "Sintel (2010) WEBDL-1080p.mkv", result: "" }],
      ),
    });
    expect(
      await screen.findByText(
        /The merged file is ready: Sintel \(2010\) WEBDL-1080p\.mkv \(4\.3 GB\)/,
      ),
    ).toBeVisible();
    const list = within(steps()).getAllByRole("listitem");
    expect(within(list[0] as HTMLElement).getByText("Done")).toBeVisible();
    expect(within(list[1] as HTMLElement).getByText("Waiting")).toBeVisible();
  });

  it.each([
    ["verify_failed", /did not pass the checks/],
    ["free_space", /not enough free space/],
    ["mux_failed", /reported a problem/],
    ["something_new", /refused \(code: something_new\)/],
  ])("explains a refusal of the merge: %s", async (refusal, text) => {
    serve("failed", {
      error: { code: "merge_failed", message: "x" },
      import: { ...importOf(merge({ state: "failed", refusal })), status: "failed" },
    });
    const notice = (
      await screen.findByText("The merge did not finish. The library file was not changed.")
    ).closest("div") as HTMLElement;
    expect(notice).toHaveTextContent(text);
  });

  it("shows the failure on the import step when Radarr did not take the merged file", async () => {
    serve("failed", {
      error: { code: "merge_rejected", message: "x" },
      import: {
        status: "failed",
        rows: [],
        merge: merge({ state: "merged", name: "Sintel (2010) WEBDL-1080p.mkv", size: 1 }),
      },
    });
    await screen.findByRole("list", { name: "Steps of the merge" });
    const list = within(steps()).getAllByRole("listitem");
    expect(within(list[0] as HTMLElement).getByText("Done")).toBeVisible();
    expect(within(list[1] as HTMLElement).getByText("Failed")).toBeVisible();
  });

  it("shows the make step as failed, and the import as not used", async () => {
    serve("failed", {
      error: { code: "merge_failed", message: "x" },
      import: { ...importOf(merge({ state: "failed", refusal: "mux_failed" })), status: "failed" },
    });
    await screen.findByRole("list", { name: "Steps of the merge" });
    const list = within(steps()).getAllByRole("listitem");
    expect(within(list[0] as HTMLElement).getByText("Failed")).toBeVisible();
    expect(within(list[1] as HTMLElement).getByText("Not used")).toBeVisible();
  });

  it("names the failed job's error", async () => {
    serve("failed", {
      error: { code: "merge_failed", message: "x" },
      import: { ...importOf(merge({ state: "failed" })), status: "failed" },
    });
    expect(
      await screen.findByText(/The merge could not be made\. The library was not changed/),
    ).toBeVisible();
  });
});

describe("the result of a merge", () => {
  const doneMerge = (r: ImportResult) =>
    serve("done", {
      import: {
        status: "done",
        rows: [{ id: "r1", name: "Sintel (2010) WEBDL-1080p.mkv", result: "imported" }],
        merge: merge({
          state: "merged",
          name: "Sintel (2010) WEBDL-1080p.mkv",
          size: 4_300_000_000,
        }),
      },
      result: r,
    });

  it("says the film is unmonitored and that its own Search can still replace the file", async () => {
    doneMerge(result());
    expect(await screen.findByText("This film is not monitored")).toBeVisible();
    expect(
      screen.getByText(/pressing the film’s own Search button in Radarr can still replace it/i),
    ).toBeVisible();
    expect(
      screen.getByText("Radarr replaced its file with Sintel (2010) WEBDL-1080p.mkv."),
    ).toBeVisible();
    // The generic notice does not say it a second time.
    expect(
      screen.queryByRole("heading", { name: "This title is not monitored" }),
    ).not.toBeInTheDocument();
  });

  it("warns that a better release can replace it when the film is still monitored", async () => {
    doneMerge(
      result({
        monitored: true,
        unmonitorReason: "",
        merge: { name: "x.mkv", warnings: [{ code: "upgrade_can_replace", message: "x" }] },
      }),
    );
    expect(await screen.findByText("This film is still monitored")).toBeVisible();
    expect(
      screen.getByText(/A better release without the new audio can replace the merged file/),
    ).toBeVisible();
  });

  it("shows a warning code it does not know with the code", async () => {
    doneMerge(result({ merge: { name: "x.mkv", warnings: [{ code: "other", message: "x" }] } }));
    expect(
      await screen.findByText(
        /Something in Radarr can still replace the merged file \(code: other\)/,
      ),
    ).toBeVisible();
  });
});

describe("the result of a second version", () => {
  const version = (status: string, detail = "") =>
    serve("done", {
      import: { status: "done", rows: [] },
      result: {
        titleLink: null,
        monitored: null,
        unmonitorReason: "",
        postProcessing: [],
        version: {
          name: "Sintel (2010) - Español.mkv",
          label: "Español",
          status,
          detail,
          groupedAt: null,
          primaryChangedAt: null,
          notices: ["outside_radarr", "kept_on_delete"],
        },
      },
    });

  it("says where the version is and how it is treated", async () => {
    version("grouped");
    expect(await screen.findByText("The second version of Sintel is in place")).toBeVisible();
    expect(
      screen.getByText("It is kept as Sintel (2010) - Español.mkv, labelled Español."),
    ).toBeVisible();
    expect(screen.getByText("The media server lists it with the film.")).toBeVisible();
    expect(screen.getByText(/Radarr does not know this version/)).toBeVisible();
    expect(screen.getByText(/the version stays on disk/)).toBeVisible();
  });

  it.each([
    ["waiting", /has not grouped it yet/],
    ["film_deleted", /no longer has this film/],
    ["missing", /no longer on disk/],
    ["newer", /State of the version: newer/],
  ])("says what the status %s means", async (status, text) => {
    version(status, "detail for admins");
    expect(await screen.findByText(text)).toBeVisible();
    expect(screen.getByText("detail for admins")).toBeVisible();
  });

  it("shows the placing while the job is importing", async () => {
    serve("importing", { import: { status: "running", rows: [], merge: null } });
    expect(await screen.findByText(/Fukku is placing the version next to the film/)).toBeVisible();
  });
});

describe("the result of a season dub", () => {
  const dub = (
    seasonDub: ImportResult["seasonDub"],
    rows: ImportInfo["rows"],
    over: Partial<ImportResult> = {},
  ) =>
    serve("done", {
      flow2: null,
      import: { status: "done", rows },
      result: {
        titleLink: "https://media.example.com/web/index.html#!/details?id=0a1b2c",
        monitored: true,
        unmonitorReason: "",
        postProcessing: [],
        seasonDub,
        ...over,
      },
    });
  const imported = [{ id: "r1", name: "S01E08.mkv", result: "imported" }];

  it("says plainly that the season is unmonitored and that a search can still replace it", async () => {
    dub({ policy: "replace", seasons: [1] }, imported, {
      monitored: false,
      unmonitorReason: "dub_only",
    });
    expect(
      await screen.findByText("Your files replaced the ones already in the library for season 1."),
    ).toBeVisible();
    expect(screen.getByText("This title is not monitored")).toBeVisible();
    expect(screen.getByText(/title's own Search button in Radarr still can/)).toBeVisible();
  });

  it("names every season that was replaced", async () => {
    dub({ policy: "replace", seasons: [1, 2] }, imported);
    expect(
      await screen.findByText(
        "Your files replaced the ones already in the library for seasons 1, 2.",
      ),
    ).toBeVisible();
  });

  it("says nothing was imported when only the subtitles were added", async () => {
    dub({ policy: "subtitlesOnly", seasons: [1] }, [], { titleLink: null });
    expect(await screen.findByText("Nothing was imported")).toBeVisible();
    expect(screen.getByText(/The subtitles went to the subtitle manager/)).toBeVisible();
    expect(screen.queryByText(/is in the library/)).not.toBeInTheDocument();
  });

  it("does not say nothing was imported when new episodes were", async () => {
    dub({ policy: "subtitlesOnly", seasons: [1] }, imported);
    expect(await screen.findByText("Sintel is in the library")).toBeVisible();
    expect(screen.queryByText("Nothing was imported")).not.toBeInTheDocument();
    expect(screen.getByText(/those files were not replaced/)).toBeVisible();
    expect(screen.getByRole("link", { name: /Open the title in the media server/ })).toBeVisible();
  });
});

describe("the steps after the import", () => {
  it("names the step that groups a second version", async () => {
    serve("post-processing", {
      import: { status: "done", rows: [] },
      result: {
        titleLink: null,
        monitored: null,
        unmonitorReason: "",
        postProcessing: [{ step: "mediaServerVersions", status: "pending", detail: "" }],
      },
    });
    expect(
      await screen.findByText("Group the second version with the film in the media server"),
    ).toBeVisible();
  });
});
