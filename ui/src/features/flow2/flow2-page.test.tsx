import { screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { HttpResponse, http } from "msw";
import { afterEach, describe, expect, it } from "vitest";
import type { Flow2, Flow2ConfirmRequest, JobDetail, Preview, PreviewRequest } from "@/api/types";
import { jobDetail } from "@/mocks/fixtures";
import {
  flow2DifferentJobId,
  flow2Fixture,
  flow2JobId,
  flow2Jobs,
  flow2PalJobId,
  flow2Target,
  readyPreview,
} from "@/mocks/flow2-fixtures";
import { applyScenarios, setMockAdmin } from "@/mocks/handlers";
import { server } from "@/mocks/server";
import { renderApp } from "@/test/render";

afterEach(() => server.events.removeAllListeners());

const slow = { timeout: 6000 };

const open = (jobId: string = flow2JobId) => {
  applyScenarios(["flow2"]);
  return renderApp(`/jobs/${jobId}/flow2`);
};

/** The job as the page reads it, with whatever flow 2 the test needs. */
function serve(flow2: Flow2 | null, over: Partial<JobDetail> = {}) {
  const base = flow2Jobs.find((j) => j.id === flow2JobId);
  const detail: JobDetail = {
    ...jobDetail(base as NonNullable<typeof base>),
    target: { ...flow2Target },
    flow2,
    ...over,
  };
  server.use(http.get("/api/jobs/:id", () => HttpResponse.json(detail)));
}

/** Records the bodies of the preview and confirm requests, in order. */
function record() {
  const previews: PreviewRequest[] = [];
  const confirms: Flow2ConfirmRequest[] = [];
  server.events.on("request:start", async ({ request }) => {
    if (request.method !== "POST") return;
    if (request.url.endsWith("/preview")) {
      const text = await request.clone().text();
      previews.push(text === "" ? {} : JSON.parse(text));
    }
    if (request.url.endsWith("/confirm")) confirms.push(await request.clone().json());
  });
  return { previews, confirms };
}

const makeClips = async (user: ReturnType<typeof userEvent.setup>) =>
  user.click(await screen.findByRole("button", { name: "Make the clips" }));

/** The notice that states a finding: the one whose heading is `title`. */
const finding = async (title: string) => {
  const heading = await screen.findByRole("heading", { name: title });
  return heading.closest('[role="status"]') as HTMLElement;
};

describe("the comparison", () => {
  it("shows both files side by side and calls an equal length the same cut", async () => {
    open();
    expect(
      await screen.findByRole("heading", { name: "Another language of this film" }),
    ).toBeVisible();
    expect(screen.getByText("Sintel (2010) in Radarr")).toBeVisible();

    const library = screen.getByRole("region", { name: "In the library" });
    expect(within(library).getByText("Sintel (2010) WEBDL-1080p.mkv")).toBeVisible();
    expect(within(library).getByText("WEBDL-1080p")).toBeVisible();
    expect(within(library).getAllByText("English").length).toBeGreaterThan(0);
    const upload = screen.getByRole("region", { name: "Your upload" });
    expect(within(upload).getByText("Sintel.2010.Spanish.1080p.mkv")).toBeVisible();
    expect(within(upload).getByText(/Latino/)).toBeVisible();

    expect(await finding("Same cut")).toHaveTextContent("within 0.2 s");
  });

  it("calls a PAL speed-up by its name and offers only a second version", async () => {
    open(flow2PalJobId);
    const notice = await finding("A PAL speed-up");
    expect(notice).toHaveTextContent("23.976 to 25");
    expect(notice).toHaveTextContent("1.0427");
    expect(screen.queryByRole("radio")).not.toBeInTheDocument();
    expect(screen.queryByRole("heading", { name: "Check that the audio is in sync" })).toBeNull();
    expect(screen.getByRole("heading", { name: "Keep as a second version" })).toBeVisible();
    // The finding already says why there is no merge: it is not listed again.
    expect(screen.queryByText(/merging needs the same cut/)).not.toBeInTheDocument();
  });

  it("calls anything else a different cut", async () => {
    open(flow2DifferentJobId);
    expect(await finding("A different cut")).toHaveTextContent("52 s");
    expect(screen.getByRole("heading", { name: "Keep as a second version" })).toBeVisible();
  });

  it("says why nothing is offered when both are switched off", async () => {
    serve(
      flow2Fixture("match", {
        offers: [],
        notOffered: [
          { offer: "merge", reason: "merge_disabled" },
          { offer: "version", reason: "versions_disabled" },
        ],
        version: null,
      }),
    );
    open();
    expect(
      await screen.findByRole("heading", { name: "Nothing can be done with this upload here" }),
    ).toBeVisible();
    expect(
      screen.getByText(
        "Merge the audio into the library file: merging is switched off on this server.",
      ),
    ).toBeVisible();
    expect(
      screen.getByText(
        "Keep it as a second version: second versions are switched off on this server.",
      ),
    ).toBeVisible();
    expect(screen.queryByRole("button", { name: /Merge|Make the clips/ })).not.toBeInTheDocument();
  });

  it("explains that versions need a media server that can group them", async () => {
    serve(
      flow2Fixture("match", {
        offers: ["merge"],
        notOffered: [{ offer: "version", reason: "versions_unsupported" }],
        version: null,
      }),
    );
    open();
    expect(await screen.findByText(/need a media server that can group them/)).toBeVisible();
    expect(screen.getByRole("heading", { name: "Check that the audio is in sync" })).toBeVisible();
  });

  it("lets the user choose between a merge and a second version with the keyboard", async () => {
    const user = userEvent.setup();
    open();
    const merge = await screen.findByRole("radio", {
      name: /Merge the audio into the library file/,
    });
    expect(merge).toBeChecked();
    merge.focus();
    await user.keyboard("{ArrowDown}");
    expect(screen.getByRole("radio", { name: /Keep it as a second version/ })).toBeChecked();
    expect(screen.getByRole("heading", { name: "Keep as a second version" })).toBeVisible();
    expect(screen.queryByRole("heading", { name: "Check that the audio is in sync" })).toBeNull();
  });
});

describe("the states of the page", () => {
  it("waits while Radarr's file is being read", async () => {
    serve(flow2Fixture("match", { state: "pending", finding: null, offers: [] }));
    open();
    expect(
      await screen.findByText("Radarr’s file is being read. This takes a moment."),
    ).toBeVisible();
  });

  it("does not wait forever for a job without an accepted video", async () => {
    serve(flow2Fixture("match", { state: "pending", finding: null, upload: null, offers: [] }));
    open();
    expect(
      await screen.findByRole("heading", { name: "There is no video to compare" }),
    ).toBeVisible();
    expect(screen.queryByText(/being read/)).not.toBeInTheDocument();
  });

  it("offers no clips while the job is only matched", async () => {
    serve(flow2Fixture("match"), { state: "matched" });
    open();
    expect(await screen.findByText(/being read/)).toBeVisible();
    expect(screen.queryByRole("button", { name: "Make the clips" })).not.toBeInTheDocument();
  });

  it("says so when there is no video to compare", async () => {
    serve(flow2Fixture("match", { upload: null, finding: null, state: "ready" }));
    open();
    expect(
      await screen.findByRole("heading", { name: "There is no video to compare" }),
    ).toBeVisible();
  });

  it("sends a film without a file to the usual review", async () => {
    serve(null);
    open();
    expect(
      await screen.findByRole("heading", { name: "This film has no file in the library" }),
    ).toBeVisible();
    expect(screen.getByRole("link", { name: "Review and import" })).toHaveAttribute(
      "href",
      `/jobs/${flow2JobId}/confirm`,
    );
  });

  it("asks to choose the film again once the job is back at probed", async () => {
    serve(null, { state: "probed", target: null });
    open();
    expect(await screen.findByRole("heading", { name: "Choose the film again" })).toBeVisible();
    expect(screen.getByRole("link", { name: "Choose the film again" })).toHaveAttribute(
      "href",
      `/jobs/${flow2JobId}/match`,
    );
  });

  it("refuses a job that is already importing", async () => {
    serve(flow2Fixture("match"), { state: "importing" });
    open();
    expect(
      await screen.findByRole("heading", { name: "This upload cannot be compared now" }),
    ).toBeVisible();
  });

  it("shows an unknown job as not found", async () => {
    applyScenarios(["flow2"]);
    renderApp(`/jobs/${"f".repeat(32)}/flow2`);
    expect(await screen.findByRole("heading", { name: "This upload was not found" })).toBeVisible();
  });
});

describe("the sync check", () => {
  it("makes the clips with the defaults, shows them, and waits for the user's word", async () => {
    const { previews } = record();
    const user = userEvent.setup();
    open();
    const make = await screen.findByRole("button", { name: "Make the clips" });
    expect(screen.getByRole("combobox", { name: "Audio track to add" })).toHaveValue("1");
    expect(screen.getByRole("textbox", { name: "Offset in milliseconds" })).toHaveValue("0");
    await user.click(make);
    expect(await screen.findByText(/Making the clips/)).toBeVisible();
    expect(previews).toEqual([{ offsetMs: 0, audioTrack: 1 }]);

    const clips = await screen.findByRole("list", { name: "Clips" }, slow);
    const videos = within(clips).getAllByLabelText(/^Clip \d of 3/);
    expect(videos).toHaveLength(3);
    expect(videos[0]).toHaveAttribute("src", `/api/jobs/${flow2JobId}/preview/1`);
    expect(within(clips).getByText("Clip 2 of 3, from 7:14")).toBeVisible();
    expect(screen.getByRole("button", { name: "They match, continue" })).toBeVisible();
    // Nothing to confirm until they say so.
    expect(screen.queryByRole("heading", { name: "Merge into the library file" })).toBeNull();
  });

  it("sends the chosen track and offset, and a keyboard user can submit with Enter", async () => {
    const { previews } = record();
    const user = userEvent.setup();
    open();
    await user.selectOptions(
      await screen.findByRole("combobox", { name: "Audio track to add" }),
      "2",
    );
    const offset = screen.getByRole("textbox", { name: "Offset in milliseconds" });
    await user.clear(offset);
    await user.type(offset, "−800{Enter}");
    await screen.findByText(/Making the clips/);
    expect(previews).toEqual([{ offsetMs: -800, audioTrack: 2 }]);
  });

  it("refuses an offset that is not a whole number in range without asking the server", async () => {
    const { previews } = record();
    const user = userEvent.setup();
    open();
    const offset = await screen.findByRole("textbox", { name: "Offset in milliseconds" });
    await user.clear(offset);
    await user.type(offset, "70000");
    await makeClips(user);
    expect(await screen.findByRole("alert")).toHaveTextContent(
      "Enter a whole number from −60000 to 60000.",
    );
    expect(offset).toHaveAttribute("aria-invalid", "true");
    expect(offset).toHaveFocus();
    expect(previews).toEqual([]);
  });

  it("shows why the clips could not be made", async () => {
    serve(flow2Fixture("match"));
    server.use(
      http.get("/api/jobs/:id/preview", () =>
        HttpResponse.json({
          ...readyPreview(flow2JobId, { offsetMs: 0, audioTrack: 1 }, "2026-01-03T09:00:00Z"),
          state: "failed",
          clips: [],
          error: { code: "timeout", message: "x" },
        } satisfies Preview),
      ),
    );
    open();
    const alert = await screen.findByText("The clips could not be made");
    expect(alert.closest('[role="status"]')).toHaveTextContent("Making a clip took too long.");
    expect(screen.getByRole("button", { name: "Make the clips again" })).toBeVisible();
  });

  it.each([
    ["task_failed", /worker could not make the clips/],
    ["task_timeout", /worker took too long/],
  ])("explains the preview error %s", async (code, text) => {
    server.use(
      http.get("/api/jobs/:id/preview", () =>
        HttpResponse.json({
          ...readyPreview(flow2JobId, { offsetMs: 0, audioTrack: 1 }, "2026-01-03T09:00:00Z"),
          state: "failed",
          clips: [],
          error: { code, message: "x" },
        } satisfies Preview),
      ),
    );
    open();
    expect(await screen.findByText(text)).toBeVisible();
  });

  it("shows the server's refusal of the request", async () => {
    const user = userEvent.setup();
    open();
    server.use(
      http.post("/api/jobs/:id/preview", () =>
        HttpResponse.json({ error: { code: "merge_not_offered", message: "x" } }, { status: 409 }),
      ),
    );
    await makeClips(user);
    expect(await screen.findByText(/cannot be merged into the library file/)).toBeVisible();
  });

  it("asks for new clips when the offset changes after the clips were made", async () => {
    const user = userEvent.setup();
    open();
    await makeClips(user);
    await screen.findByRole("list", { name: "Clips" }, slow);
    const offset = screen.getByRole("textbox", { name: "Offset in milliseconds" });
    await user.clear(offset);
    await user.type(offset, "250");
    expect(screen.getByText(/changed after these clips were made/)).toBeVisible();
    expect(screen.queryByRole("button", { name: "They match, continue" })).not.toBeInTheDocument();
  });

  it("moves the offset with the buttons, which only write into the field", async () => {
    const { previews } = record();
    const user = userEvent.setup();
    open();
    await makeClips(user);
    await screen.findByRole("list", { name: "Clips" }, slow);
    const offset = screen.getByRole("textbox", { name: "Offset in milliseconds" });
    const late = screen.getByRole("group", { name: "The sound comes after the picture" });
    const early = screen.getByRole("group", { name: "The sound comes before the picture" });

    await user.click(within(late).getByRole("button", { name: "250 ms earlier" }));
    await user.click(within(late).getByRole("button", { name: "50 ms earlier" }));
    expect(offset).toHaveValue("-300");
    expect(screen.getByText("The new audio starts 0.3 s earlier.")).toBeVisible();
    // The clips were made at 0: they no longer show what would be merged.
    expect(screen.getByText(/changed after these clips were made/)).toBeVisible();
    expect(screen.queryByRole("button", { name: "They match, continue" })).not.toBeInTheDocument();
    // Nothing was asked of the server by the buttons.
    expect(previews).toHaveLength(1);

    // Back to the offset of the clips: they can be confirmed again.
    await user.click(within(early).getByRole("button", { name: "250 ms later" }));
    await user.click(within(early).getByRole("button", { name: "50 ms later" }));
    expect(offset).toHaveValue("0");
    expect(screen.getByRole("button", { name: "They match, continue" })).toBeVisible();

    await user.click(within(early).getByRole("button", { name: "50 ms later" }));
    await user.click(screen.getByRole("button", { name: "Make the clips again" }));
    await waitFor(() => expect(previews).toHaveLength(2));
    expect(previews[1]).toEqual({ offsetMs: 50, audioTrack: 1 });
  });

  it("keeps the offset within its bounds when a button would pass them", async () => {
    const user = userEvent.setup();
    open();
    const offset = await screen.findByRole("textbox", { name: "Offset in milliseconds" });
    await user.clear(offset);
    await user.type(offset, "59900");
    await user.click(screen.getByRole("button", { name: "250 ms later" }));
    expect(offset).toHaveValue("60000");
    await user.click(screen.getByRole("button", { name: "250 ms later" }));
    expect(offset).toHaveValue("60000");
    await user.clear(offset);
    await user.type(offset, "-60000");
    await user.click(screen.getByRole("button", { name: "50 ms earlier" }));
    expect(offset).toHaveValue("-60000");
  });

  it("keeps the headings in order down to the merge confirmation", async () => {
    const user = userEvent.setup();
    open();
    await makeClips(user);
    await screen.findByRole("list", { name: "Clips" }, slow);
    await user.click(screen.getByRole("button", { name: "They match, continue" }));
    await screen.findByRole("heading", { name: "Merge into the library file" });
    const levels = screen
      .getAllByRole("heading")
      .map((h) => Number(h.tagName.slice(1)))
      .filter((n) => n > 0);
    // No heading is more than one level below the one before it.
    levels.forEach((level, i) => {
      if (i > 0) expect(level - (levels[i - 1] ?? level)).toBeLessThanOrEqual(1);
    });
    expect(
      screen.getByRole("heading", { level: 4, name: "Merge the audio into the library file?" }),
    ).toBeVisible();
  });
});

describe("the merge", () => {
  async function toMerge(user: ReturnType<typeof userEvent.setup>) {
    await makeClips(user);
    await screen.findByRole("list", { name: "Clips" }, slow);
    await user.click(screen.getByRole("button", { name: "They match, continue" }));
    return screen.findByRole("heading", { name: "Merge into the library file" });
  }

  it("confirms with the preview's track and offset and goes to the job", async () => {
    const { confirms } = record();
    const user = userEvent.setup();
    const { router } = open();
    await user.selectOptions(
      await screen.findByRole("combobox", { name: "Audio track to add" }),
      "2",
    );
    const offset = screen.getByRole("textbox", { name: "Offset in milliseconds" });
    await user.clear(offset);
    await user.type(offset, "-800");
    await toMerge(user);

    await user.click(screen.getByRole("checkbox", { name: /Spanish/ }));
    await user.type(screen.getByRole("textbox", { name: "Language tag" }), "es-419");
    await user.type(screen.getByRole("textbox", { name: "Track name" }), "Español (Latino)");
    await user.click(
      screen.getByRole("checkbox", { name: "Keep the original audio as the default track" }),
    );

    const form = screen.getByRole("form", { name: "Merge the audio into the library file?" });
    // The B6 warning is in the confirmation and has to be ticked.
    expect(within(form).getByText(/own Search button in Radarr can still replace/)).toBeVisible();
    await user.click(within(form).getByRole("button", { name: "Merge into the library file" }));
    expect(await within(form).findByText("Tick the box above to continue.")).toBeVisible();
    expect(confirms).toEqual([]);
    await user.click(within(form).getByRole("checkbox"));
    await user.click(within(form).getByRole("button", { name: "Merge into the library file" }));

    await waitFor(() => expect(router.state.location.pathname).toBe(`/jobs/${flow2JobId}`));
    expect(confirms).toEqual([
      {
        flow2: "merge",
        merge: {
          audioTracks: [2],
          subtitleTracks: [3],
          offsetMs: -800,
          languageTag: "es-419",
          trackName: "Español (Latino)",
          keepOriginalDefault: false,
        },
      },
    ]);
  });

  it("shows a refused merge next to the sync check, and the user can choose again (#95)", async () => {
    const { confirms, previews } = record();
    serve(
      flow2Fixture("match", {
        mergeRefusal: {
          code: "verify_failed",
          message: "The merged file failed its checks.",
          audioTracks: [2],
          subtitleTracks: [],
          offsetMs: -800,
          languageTag: "es-419",
          at: "2026-01-03T08:40:00Z",
        },
      }),
    );
    const user = userEvent.setup();
    open();
    const alert = await screen.findByRole("alert");
    expect(
      within(alert).getByRole("heading", { name: "The last merge was refused" }),
    ).toBeVisible();
    expect(within(alert).getByText(/did not pass the checks/)).toBeVisible();
    expect(within(alert).getByText(/at an offset of -800 ms/)).toBeVisible();
    expect(within(alert).getByText(/Choose another offset or audio track/)).toBeVisible();
    // The sync check is there, with its own form, so another offset can be tried.
    const offset = screen.getByRole("textbox", { name: "Offset in milliseconds" });
    await user.clear(offset);
    await user.type(offset, "-600");
    await toMerge(user);
    await user.click(screen.getByRole("checkbox", { name: /Spanish/ }));
    const form = screen.getByRole("form", { name: "Merge the audio into the library file?" });
    await user.click(within(form).getByRole("checkbox"));
    await user.click(within(form).getByRole("button", { name: "Merge into the library file" }));
    await waitFor(() => expect(confirms).toHaveLength(1));
    expect(previews[0]).toMatchObject({ offsetMs: -600 });
    expect(confirms[0]).toMatchObject({ flow2: "merge", merge: { offsetMs: -600 } });
  });

  it("shows no refusal when the last merge was not refused", async () => {
    open();
    await screen.findByRole("heading", { name: "Check that the audio is in sync" });
    expect(screen.queryByText("The last merge was refused")).not.toBeInTheDocument();
  });

  it("leaves the tag, the name and the default to the server when they are not touched", async () => {
    const { confirms } = record();
    const user = userEvent.setup();
    open();
    await toMerge(user);
    const form = screen.getByRole("form", { name: "Merge the audio into the library file?" });
    await user.click(within(form).getByRole("checkbox"));
    await user.click(within(form).getByRole("button", { name: "Merge into the library file" }));
    await waitFor(() => expect(confirms).toHaveLength(1));
    // What the form shows is what is sent, the default of the original audio included.
    expect(confirms[0]).toEqual({
      flow2: "merge",
      merge: { audioTracks: [1], subtitleTracks: [], offsetMs: 0, keepOriginalDefault: true },
    });
  });

  it("withdraws the merge when the offset changes after the clips were checked", async () => {
    const { confirms } = record();
    const user = userEvent.setup();
    open();
    await toMerge(user);
    expect(
      screen.getByRole("form", { name: "Merge the audio into the library file?" }),
    ).toBeVisible();
    const offset = screen.getByRole("textbox", { name: "Offset in milliseconds" });
    await user.clear(offset);
    await user.type(offset, "-900");
    expect(screen.queryByRole("form", { name: /Merge the audio/ })).not.toBeInTheDocument();
    expect(screen.queryByRole("heading", { name: "Merge into the library file" })).toBeNull();
    expect(screen.getByText(/changed after these clips were made/)).toBeVisible();
    // Back to what the clips show: the confirmation comes back.
    await user.clear(offset);
    await user.type(offset, "0");
    expect(
      screen.getByRole("form", { name: "Merge the audio into the library file?" }),
    ).toBeVisible();
    expect(confirms).toEqual([]);
  });

  it("withdraws the merge when the track changes after the clips were checked", async () => {
    const user = userEvent.setup();
    open();
    await toMerge(user);
    await user.selectOptions(screen.getByRole("combobox", { name: "Audio track to add" }), "2");
    expect(screen.queryByRole("form", { name: /Merge the audio/ })).not.toBeInTheDocument();
  });

  it("moves the focus to the merge when the user says the clips match", async () => {
    const user = userEvent.setup();
    open();
    const heading = await toMerge(user);
    expect(heading).toHaveFocus();
  });

  it("lets an offset with a minus sign be typed on a phone keyboard", async () => {
    open();
    const offset = await screen.findByRole("textbox", { name: "Offset in milliseconds" });
    expect(offset).toHaveAttribute("inputmode", "text");
    expect(offset).toHaveAttribute("pattern");
  });

  it("refuses a language tag that is not canonical, and says so by the field", async () => {
    const { confirms } = record();
    const user = userEvent.setup();
    open();
    await toMerge(user);
    const tag = screen.getByRole("textbox", { name: "Language tag" });
    await user.type(tag, "es_419");
    const form = screen.getByRole("form", { name: "Merge the audio into the library file?" });
    await user.click(within(form).getByRole("checkbox"));
    await user.click(within(form).getByRole("button", { name: "Merge into the library file" }));
    expect(await screen.findByText(/not a valid language tag/)).toBeVisible();
    expect(tag).toHaveAttribute("aria-invalid", "true");
    expect(tag).toHaveFocus();
    expect(confirms).toEqual([]);
    await user.clear(tag);
    await user.type(tag, "es-419");
    await user.click(within(form).getByRole("button", { name: "Merge into the library file" }));
    await waitFor(() => expect(confirms).toHaveLength(1));
  });

  it("hands the job's subtitle files over with the merge, as an import does", async () => {
    const { confirms } = record();
    const user = userEvent.setup();
    serve(flow2Fixture("match"), {
      files: [
        {
          id: 401,
          path: "files/Sintel.2010.es.srt",
          name: "Sintel.2010.es.srt",
          kind: "subtitle",
          size: 1700,
          status: "ok",
          reason: null,
          from: null,
          probe: null,
          extraction: null,
        },
      ],
    });
    open();
    await toMerge(user);
    expect(await screen.findByRole("heading", { name: "Subtitle files" })).toBeVisible();
    const form = screen.getByRole("form", { name: "Merge the audio into the library file?" });
    await user.click(within(form).getByRole("checkbox"));
    await user.click(within(form).getByRole("button", { name: "Merge into the library file" }));
    await waitFor(() => expect(confirms).toHaveLength(1));
    expect(confirms[0]).toMatchObject({
      flow2: "merge",
      subtitles: [{ fileId: 401, language: "es" }],
    });
  });

  it.each([
    ["preview_required", /sync clips are out of date/],
    ["merge_not_offered", /cannot be merged into the library file/],
    ["storage_unavailable", /Storage is not available/],
  ])("shows the refusal %s", async (code, text) => {
    const user = userEvent.setup();
    open();
    await toMerge(user);
    server.use(
      http.post("/api/jobs/:id/confirm", () =>
        HttpResponse.json({ error: { code, message: "x" } }, { status: 409 }),
      ),
    );
    const form = screen.getByRole("form", { name: "Merge the audio into the library file?" });
    await user.click(within(form).getByRole("checkbox"));
    await user.click(within(form).getByRole("button", { name: "Merge into the library file" }));
    expect(await screen.findByText(text)).toBeVisible();
  });

  it("offers to choose the film again when its file changed", async () => {
    const user = userEvent.setup();
    open();
    await toMerge(user);
    server.use(
      http.post("/api/jobs/:id/confirm", () =>
        HttpResponse.json({ error: { code: "library_changed", message: "x" } }, { status: 409 }),
      ),
    );
    const form = screen.getByRole("form", { name: "Merge the audio into the library file?" });
    await user.click(within(form).getByRole("checkbox"));
    await user.click(within(form).getByRole("button", { name: "Merge into the library file" }));
    expect(await screen.findByText(/changed since you chose the film/)).toBeVisible();
    expect(screen.getByRole("link", { name: "Choose the film again" })).toHaveAttribute(
      "href",
      `/jobs/${flow2JobId}/match`,
    );
  });
});

describe("the second version", () => {
  const form = () => screen.getByRole("form", { name: "Keep this as a second version?" });

  it("proposes the label, tells how the version is treated and confirms it", async () => {
    const { confirms } = record();
    const user = userEvent.setup();
    const { router } = open(flow2PalJobId);
    const label = await screen.findByRole("textbox", { name: "Version label" });
    expect(label).toHaveValue("Español");
    expect(screen.getByText("File name: Sintel (2010) - Español.mkv")).toBeVisible();
    expect(screen.getByText(/Radarr does not know this version/)).toBeVisible();
    expect(screen.getByText(/the media server loses the grouping/)).toBeVisible();
    expect(screen.getByText(/the version stays on disk/)).toBeVisible();

    await user.clear(label);
    await user.type(label, "Latino");
    await user.click(within(form()).getByRole("button", { name: "Keep as a second version" }));
    await waitFor(() => expect(router.state.location.pathname).toBe(`/jobs/${flow2PalJobId}`));
    expect(confirms).toEqual([{ flow2: "version", version: { label: "Latino" } }]);
  });

  it("explains a problem with the proposed label, and the fix for an admin", async () => {
    setMockAdmin(true);
    serve(
      flow2Fixture("pal", {
        version: {
          label: "Español 1080p",
          language: "es",
          name: "Sintel (2010) - Español 1080p.mkv",
          problem: { reason: "label_forbidden", token: "1080p", message: "x" },
          notices: [],
        },
      }),
    );
    const user = userEvent.setup();
    open(flow2PalJobId);
    const notice = await screen.findByRole("alert");
    expect(
      within(notice).getByRole("heading", { name: "This label cannot be used as it is" }),
    ).toBeVisible();
    expect(notice).toHaveTextContent("“1080p” is a word kept out of labels");
    expect(await within(notice).findByText(/versions\.forbiddenLabelTokens/)).toBeVisible();
    const label = screen.getByRole("textbox", { name: "Version label" });
    expect(label).toHaveAttribute("aria-invalid", "true");
    // Changing the label retires the problem of the proposed one.
    await user.clear(label);
    await user.type(label, "Latino");
    expect(screen.queryByText(/is a word kept out of labels/)).not.toBeInTheDocument();
  });

  it("does not show the admin fix to someone who does not run the server", async () => {
    serve(
      flow2Fixture("pal", {
        version: {
          label: "",
          language: "",
          name: "",
          problem: { reason: "label_empty", message: "x" },
          notices: ["subtitles_not_handed_over"],
        },
      }),
    );
    open(flow2PalJobId);
    const notice = await screen.findByRole("alert");
    expect(notice).toHaveTextContent("No label could be proposed");
    expect(screen.queryByText(/versions\.forbiddenLabelTokens/)).not.toBeInTheDocument();
    expect(screen.getByText(/subtitle files of this upload are not handed/)).toBeVisible();
  });

  it.each([
    ["version_label", /That label cannot be used/],
    ["version_exists", /A version with this name already exists/],
    ["version_unavailable", /second version is not possible right now/],
    ["storage_unavailable", /Storage is not available/],
    ["library_changed", /changed since you chose the film/],
  ])("shows the refusal %s", async (code, text) => {
    const user = userEvent.setup();
    open(flow2PalJobId);
    await screen.findByRole("textbox", { name: "Version label" });
    server.use(
      http.post("/api/jobs/:id/confirm", () =>
        HttpResponse.json(
          { error: { code, message: "x" } },
          { status: code === "storage_unavailable" ? 503 : 409 },
        ),
      ),
    );
    await user.click(within(form()).getByRole("button", { name: "Keep as a second version" }));
    expect(await screen.findByText(text)).toBeVisible();
  });
});
