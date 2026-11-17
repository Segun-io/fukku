import { describe, expect, it } from "vitest";
import type { Proposal, ProposalRow, SeriesEpisode } from "@/api/proposal";
import {
  applyEdit,
  type EditorAction,
  editorReducer,
  initialState,
  mergeEdits,
  needsAttention,
  nextEpisode,
  rowStatus,
  seasonsOf,
  shownRows,
  summarize,
} from "./state";

const ep = (id: number, seasonNumber: number, number: number): SeriesEpisode => ({
  id,
  seasonNumber,
  number,
  title: `Episode ${number}`,
  runtimeMs: 1_320_000,
  hasFile: false,
});
const episodes = [ep(101, 1, 1), ep(102, 1, 2), ep(103, 1, 3), ep(201, 2, 1), ep(202, 2, 2)];

const row = (id: string, extra: Partial<ProposalRow> = {}): ProposalRow => ({
  id,
  fileId: 1,
  name: `${id}.mkv`,
  durationMs: 1_320_000,
  skip: false,
  seasonNumber: 1,
  episodes: [{ id: 103, number: 3, title: "Episode 3", runtimeMs: 1_320_000, hasFile: false }],
  quality: { id: 3, name: "WEBDL-1080p" },
  languages: [{ id: 1, name: "English" }],
  customFormats: [],
  customFormatScore: 0,
  rejections: [],
  warnings: [],
  importable: true,
  ...extra,
});

const proposal = (rows: ProposalRow[]): Proposal => ({
  state: "ready",
  app: "sonarr",
  target: { titleId: 1, title: "Some Show", year: 2010 },
  rows,
  hints: [],
  episodes,
  qualities: [
    { id: 3, name: "WEBDL-1080p" },
    { id: 7, name: "Bluray-1080p" },
  ],
  languages: [{ id: 1, name: "English" }],
});

const run = (...actions: EditorAction[]) => actions.reduce(editorReducer, initialState);

describe("mergeEdits", () => {
  it("lets the later edit win field by field", () => {
    expect(mergeEdits({ skip: true, qualityId: 3 }, { qualityId: 7 })).toEqual({
      skip: true,
      qualityId: 7,
    });
  });

  it("keeps the later of the episodes and the season, which describe one thing", () => {
    expect(mergeEdits({ seasonNumber: 2 }, { episodeIds: [101] })).toEqual({ episodeIds: [101] });
    expect(mergeEdits({ episodeIds: [101] }, { seasonNumber: 2 })).toEqual({ seasonNumber: 2 });
    expect(mergeEdits({ seasonNumber: 2 }, { seasonNumber: 2, episodeIds: [201] })).toEqual({
      seasonNumber: 2,
      episodeIds: [201],
    });
  });
});

describe("an edit", () => {
  it("is shown at once and the row is checking until the app answers", () => {
    const p = proposal([row("r1")]);
    const state = run({ type: "edited", rowId: "r1", edit: { episodeIds: [101, 102] } });
    expect(rowStatus(state, "r1")).toMatchObject({ status: "checking", error: null });
    const [shown] = shownRows(p, state);
    expect(shown?.episodes?.map((e) => e.number)).toEqual([1, 2]);
    expect(shown?.seasonNumber).toBe(1);
    // The app's own copy of the row is untouched.
    expect(p.rows[0]?.episodes).toHaveLength(1);
  });

  it("goes away when the app has answered", () => {
    const state = run(
      { type: "edited", rowId: "r1", edit: { skip: true } },
      { type: "checked", rowId: "r1", more: false },
    );
    expect(state.pending).toEqual({});
    expect(rowStatus(state, "r1")).toBeNull();
  });

  it("is only applied to its own row", () => {
    const p = proposal([row("r1"), row("r2")]);
    const state = run({ type: "edited", rowId: "r1", edit: { skip: true } });
    expect(shownRows(p, state).map((r) => r.skip)).toEqual([true, false]);
  });
});

describe("a second edit of the same row while the first is being checked", () => {
  it("merges into what is shown, and the row keeps checking after the first answer", () => {
    const p = proposal([row("r1")]);
    const state = run(
      { type: "edited", rowId: "r1", edit: { episodeIds: [101] } },
      { type: "edited", rowId: "r1", edit: { qualityId: 7 } },
      { type: "checked", rowId: "r1", more: true },
    );
    expect(state.pending.r1).toMatchObject({
      status: "checking",
      edit: { episodeIds: [101], qualityId: 7 },
    });
    const [shown] = shownRows(p, state);
    expect(shown?.episodes?.[0]?.number).toBe(1);
    expect(shown?.quality?.name).toBe("Bluray-1080p");
    // The last answer ends it.
    expect(editorReducer(state, { type: "checked", rowId: "r1", more: false }).pending).toEqual({});
  });
});

describe("a failed re-check", () => {
  const failure = new Error("down");
  const failed = run(
    { type: "edited", rowId: "r1", edit: { episodeIds: [102] } },
    { type: "failed", rowId: "r1", error: failure },
  );

  it("keeps the edit visible with the error", () => {
    expect(rowStatus(failed, "r1")).toMatchObject({ status: "failed", error: failure });
    const [shown] = shownRows(proposal([row("r1")]), failed);
    expect(shown?.episodes?.[0]?.number).toBe(2);
  });

  it("is retried: back to checking, same edit", () => {
    const state = editorReducer(failed, { type: "retry", rowId: "r1" });
    expect(state.pending.r1).toMatchObject({
      status: "checking",
      error: null,
      edit: { episodeIds: [102] },
    });
  });

  it("can be undone: the row returns to the app's verdict", () => {
    const state = editorReducer(failed, { type: "discard", rowId: "r1" });
    expect(shownRows(proposal([row("r1")]), state)[0]?.episodes?.[0]?.number).toBe(3);
  });

  it("is merged with a later edit, which sends them together", () => {
    const state = editorReducer(failed, { type: "edited", rowId: "r1", edit: { skip: true } });
    expect(state.pending.r1).toMatchObject({
      status: "checking",
      edit: { episodeIds: [102], skip: true },
    });
  });
});

describe("a bulk edit", () => {
  it("marks its rows while it runs and says when it failed", () => {
    const started = run({ type: "bulkStarted", rowIds: ["r1", "r2"] });
    expect(rowStatus(started, "r2")).toMatchObject({ status: "checking" });
    expect(rowStatus(started, "r3")).toBeNull();
    const failed = editorReducer(started, { type: "bulkFailed", error: new Error("x") });
    expect(rowStatus(failed, "r1")).toMatchObject({ status: "failed" });
    expect(editorReducer(failed, { type: "bulkDone" }).bulk).toBeNull();
    expect(editorReducer(failed, { type: "bulkDiscard" }).bulk).toBeNull();
  });

  it("does not hide a row's own pending edit", () => {
    const state = run(
      { type: "bulkStarted", rowIds: ["r1"] },
      { type: "edited", rowId: "r1", edit: { skip: true } },
    );
    expect(rowStatus(state, "r1")).toBe(state.pending.r1);
  });
});

describe("applyEdit", () => {
  const p = proposal([row("r1")]);

  it("moves a row to a season by keeping its episode numbers", () => {
    const out = applyEdit(row("r1"), { seasonNumber: 2 }, p);
    expect(out.seasonNumber).toBe(2);
    expect(out.episodes).toEqual([]); // season 2 has no episode 3 in this series
    const one = applyEdit(row("r1", { episodes: [{ ...ep(103, 1, 3) }] }), { seasonNumber: 2 }, p);
    expect(one.episodes).toEqual([]);
    const first = applyEdit(
      row("r1", { episodes: [{ ...ep(101, 1, 1) }] }),
      { seasonNumber: 2 },
      p,
    );
    expect(first.episodes?.map((e) => e.id)).toEqual([201]);
  });

  it("takes the season from the episodes", () => {
    const out = applyEdit(row("r1"), { episodeIds: [201, 202] }, p);
    expect(out.seasonNumber).toBe(2);
    expect(out.episodes?.map((e) => e.number)).toEqual([1, 2]);
    expect(out.episodes?.[0]).not.toHaveProperty("seasonNumber");
  });

  it("sets the skip, the quality and the languages", () => {
    const out = applyEdit(row("r1"), { skip: true, qualityId: 7, languageIds: [1] }, p);
    expect(out).toMatchObject({
      skip: true,
      quality: { id: 7 },
      languages: [{ id: 1, name: "English" }],
    });
  });
});

describe("who skipped a row (#102)", () => {
  const p = proposal([row("r1")]);
  it("records the user's skip, and hands a row with a file back to the server's", () => {
    const withFile = row("r1", {
      skip: true,
      skippedBy: "server",
      warnings: [{ code: "existing_file" }],
    });
    const left = applyEdit(withFile, { skip: true }, p);
    expect(left).toMatchObject({ skip: true, skippedBy: "user" });
    expect(applyEdit(left, { skip: false }, p)).toMatchObject({ skip: true, skippedBy: "server" });
    const plain = applyEdit(row("r2", { skip: true, skippedBy: "user" }), { skip: false }, p);
    expect(plain.skip).toBe(false);
    expect(plain).not.toHaveProperty("skippedBy");
  });
});

describe("the summary and the filter", () => {
  const rows = [
    row("ok"),
    row("warned", { importable: false, warnings: [{ code: "language" }] }),
    row("none", { importable: false, episodes: [] }),
    row("skipped", { skip: true, importable: false, warnings: [{ code: "existing_file" }] }),
  ];

  it("counts the rows to import, skipped and in need of attention", () => {
    expect(summarize(rows)).toEqual({ toImport: 1, skipped: 1, attention: 2 });
  });

  it("calls a skipped row fine, whatever it carries", () => {
    expect(rows.map(needsAttention)).toEqual([false, true, true, false]);
  });
});

describe("helpers", () => {
  it("lists the seasons in order", () => {
    expect(seasonsOf([...episodes].reverse())).toEqual([1, 2]);
  });

  it("finds the episode after one, in its season", () => {
    expect(nextEpisode(1, ep(101, 1, 1), episodes)?.id).toBe(102);
    expect(nextEpisode(1, ep(103, 1, 3), episodes)).toBeUndefined();
  });
});
