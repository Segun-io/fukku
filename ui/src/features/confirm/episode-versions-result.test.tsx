import { screen, within } from "@testing-library/react";
import { HttpResponse, http } from "msw";
import { describe, expect, it } from "vitest";
import type { ImportInfo, ImportResult, JobDetail, JobState } from "@/api/types";
import es from "@/i18n/es";
import { versionResult, versionsResult } from "@/mocks/episode-version-fixtures";
import { jobDetail } from "@/mocks/fixtures";
import { applyScenarios } from "@/mocks/handlers";
import { mappingJobId } from "@/mocks/mapping-fixtures";
import { mappingJobs, versionsJobId } from "@/mocks/scenarios";
import { server } from "@/mocks/server";
import { renderApp } from "@/test/render";

const rows = (...results: string[]): ImportInfo["rows"] =>
  results.map((result, i) => ({ id: `r${i + 1}`, name: `File ${i + 1}.mkv`, result }));

function serve(
  state: JobState,
  imp: ImportInfo,
  result: Partial<ImportResult> | null = null,
  extra: Partial<JobDetail> = {},
) {
  const base = mappingJobs.find((j) => j.id === mappingJobId);
  const job = { ...(base as NonNullable<typeof base>), state };
  const detail: JobDetail = {
    ...jobDetail(job),
    target: { app: "sonarr", titleId: 12, title: "Lighthouse Letters", year: 2019 },
    import: imp,
    ...extra,
    result: result && {
      titleLink: null,
      monitored: true,
      unmonitorReason: "",
      postProcessing: [],
      ...result,
    },
  };
  server.use(http.get("/api/jobs/:id", () => HttpResponse.json(detail)));
  return renderApp(`/jobs/${mappingJobId}`);
}

describe("the import of episodes kept as second versions", () => {
  it("counts a row kept as a version as in the library, and names its state", async () => {
    serve("importing", { status: "running", rows: rows("version", "imported", "", "") });
    const bar = await screen.findByRole("progressbar", { name: "Files" });
    expect(bar).toHaveAttribute("aria-valuenow", "2");
    expect(bar).toHaveAttribute("aria-valuemax", "4");
    expect(screen.getByText("2 of 4 files in the library")).toBeVisible();
    const list = screen.getByRole("list", { name: "Files" });
    expect(within(list).getByText("Second version")).toBeVisible();
    expect(within(list).getByText("Imported")).toBeVisible();
  });

  it("says the second versions are in place when nothing else was imported", async () => {
    serve(
      "done",
      { status: "done", rows: rows("version") },
      {
        seasonDub: { policy: "version", seasons: [1] },
        episodeVersions: versionsResult([versionResult("r1", 7)]),
        postProcessing: [{ step: "mediaServerEpisodeVersions", status: "ok", detail: "" }],
      },
    );
    expect(
      await screen.findByText("The second versions of Lighthouse Letters are in place"),
    ).toBeVisible();
    expect(screen.getByText(/1 file is kept next to its episode’s own file/)).toBeVisible();
    const list = screen.getByRole("list", { name: "Second versions" });
    expect(
      within(list).getByText(/S01E07 Lighthouse Letters \(2019\) - S01E07 - Español\.mkv/),
    ).toBeVisible();
    expect(within(list).getByText("The media server lists it with the episode.")).toBeVisible();
    expect(screen.getByText("Label: Español")).toBeVisible();
    expect(
      screen.getByText("Group the second versions with the episodes in the media server"),
    ).toBeVisible();
  });

  it("keeps the usual title when new episodes were imported too", async () => {
    serve(
      "done",
      { status: "done", rows: rows("version", "imported") },
      { episodeVersions: versionsResult([versionResult("r1", 7)]) },
    );
    expect(await screen.findByText("Lighthouse Letters is in the library")).toBeVisible();
    expect(screen.getByRole("list", { name: "Second versions" })).toBeVisible();
  });

  it("shows the notices, including that Sonarr may adopt the version", async () => {
    serve(
      "done",
      { status: "done", rows: rows("version") },
      { episodeVersions: versionsResult([versionResult("r1", 7)]) },
    );
    expect(await screen.findByText(/Sonarr does not track the second files/)).toBeVisible();
    expect(
      screen.getByText(
        "If an episode’s own file is deleted, Sonarr takes the second file for the episode’s file on its next scan.",
      ),
    ).toBeVisible();
  });

  it("shows the subtitle notice when the server has one", async () => {
    serve(
      "done",
      { status: "done", rows: rows("version") },
      {
        episodeVersions: versionsResult([versionResult("r1", 7)], {
          notices: ["subtitles_not_handed_over", "something_new"],
        }),
      },
    );
    expect(await screen.findByText(/subtitle files of this upload are not handed/)).toBeVisible();
    expect(screen.getByText("A note from the server (code: something_new).")).toBeVisible();
  });

  it.each([
    ["placing", /being placed/],
    ["linked", /in place\. The media server groups it next/],
    ["grouped", /lists it with the episode/],
    ["waiting", /has not grouped it yet/],
    ["missing", /no longer on disk/],
    ["adopted", /now tracks this file as the episode’s own/],
    ["adoptable", /would take this file for the episode’s own on its next scan/],
    ["episode_file_missing", /episode’s own file is gone/],
    ["series_moved", /moved the series without this file/],
    ["series_deleted", /no longer has this series/],
    ["something_new", /State of the version: something_new\./],
  ])("says what the status %s means", async (status, text) => {
    serve(
      "done",
      { status: "done", rows: rows("version") },
      { episodeVersions: versionsResult([versionResult("r1", 7, { status })]) },
    );
    const list = await screen.findByRole("list", { name: "Second versions" });
    expect(within(list).getByText(text)).toBeVisible();
  });

  it.each([
    ["version_upgrade", /would take this file for the episode’s own, so it was not placed/],
    ["version_unchecked", /gave no verdict on this file/],
    ["version_exists", /file with this name already exists, so nothing was replaced/],
    ["version_failed", /could not be placed\. Tell the person who runs the server/],
    ["something_new", /not placed \(code: something_new\)/],
  ])("says why a version was refused at the placement: %s", async (refusal, text) => {
    serve(
      "done",
      { status: "done", rows: rows("failed", "version") },
      {
        episodeVersions: versionsResult([
          versionResult("r1", 7, { status: "refused", refusal, groupedAt: null }),
          versionResult("r2", 8),
        ]),
      },
    );
    const list = await screen.findByRole("list", { name: "Second versions" });
    const items = within(list).getAllByRole("listitem");
    expect(within(items[0] as HTMLElement).getByText(text)).toBeVisible();
    expect(within(items[1] as HTMLElement).getByText(/lists it with the episode/)).toBeVisible();
  });

  it("shows the detail the server gives of a version", async () => {
    serve(
      "done",
      { status: "done", rows: rows("version") },
      {
        episodeVersions: versionsResult([
          versionResult("r1", 7, {
            status: "series_moved",
            detail: "folder now at /tv/Lighthouse",
          }),
        ]),
      },
    );
    expect(await screen.findByText("folder now at /tv/Lighthouse")).toBeVisible();
  });

  it("names the step that groups the versions when it fails", async () => {
    serve(
      "done",
      { status: "done", rows: rows("version") },
      {
        episodeVersions: versionsResult([versionResult("r1", 7, { status: "waiting" })]),
        postProcessing: [{ step: "mediaServerEpisodeVersions", status: "failed", detail: "" }],
      },
    );
    expect(
      await screen.findByText(/did not group the versions with the episodes\. Fukku tries again/),
    ).toBeVisible();
  });

  it("does not call a partial result a plain success, and counts both sides", async () => {
    serve(
      "done",
      { status: "done", rows: rows("failed", "version", "version") },
      {
        episodeVersions: versionsResult([
          versionResult("r1", 7, {
            status: "refused",
            refusal: "version_upgrade",
            groupedAt: null,
          }),
          versionResult("r2", 8),
          versionResult("r3", 9),
        ]),
      },
    );
    const title = await screen.findByText("2 of 3 second versions are in place");
    const notice = title.closest("[data-tone]") as HTMLElement;
    expect(notice).toHaveAttribute("data-tone", "caution");
    expect(notice).toHaveTextContent("1 could not be placed");
  });

  it("says how many were refused next to the usual title when new episodes were imported", async () => {
    serve(
      "done",
      { status: "done", rows: rows("version", "imported") },
      {
        episodeVersions: versionsResult([
          versionResult("r1", 7, { status: "refused", refusal: "version_exists", groupedAt: null }),
          versionResult("r2", 8),
        ]),
      },
    );
    const title = await screen.findByText("Lighthouse Letters is in the library");
    expect(title.closest("[data-tone]")).toHaveTextContent("1 could not be placed");
  });

  it.each([
    ["version_upgrade", /would take every second version/],
    ["version_unchecked", /gave no verdict on any second version/],
    ["version_exists", /already exists under its name/],
  ])("explains a job that failed because every version was refused: %s", async (code, text) => {
    serve(
      "failed",
      { status: "done", rows: rows("failed") },
      {
        episodeVersions: versionsResult([
          versionResult("r1", 7, { status: "refused", refusal: code, groupedAt: null }),
        ]),
      },
      { error: { code, message: "x" } },
    );
    expect(await screen.findByText(text)).toBeVisible();
    expect(screen.queryByText(/Something went wrong with this upload/)).not.toBeInTheDocument();
    // The placement's own sentence is there for the failed job too.
    const list = screen.getByRole("list", { name: "Second versions" });
    expect(
      within(list).getByText(/was not placed|were not placed|gave no verdict|already exists/),
    ).toBeVisible();
  });

  it("writes the episode as S01E07 in Spanish too, as in the file names", () => {
    expect(es["import.episodeVersion.item"]).toBe("S{season}E{episode}");
    expect(versionResult("r1", 12).name).toContain("S01E12");
  });

  it("shows the dev scenario of a finished import with versions", async () => {
    applyScenarios(["versions"]);
    renderApp(`/jobs/${versionsJobId}`);
    expect(await screen.findByRole("list", { name: "Second versions" })).toBeVisible();
  });
});
