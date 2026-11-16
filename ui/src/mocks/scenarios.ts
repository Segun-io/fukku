// Extra data for `npm run dev:mock`, to look at the pages with content the fixtures of the
// tests do not have: an empty list, an administrator, and a season of 24 episodes with long
// names. Open any page with `?mock=` and one or more names, separated by commas:
//
//   /jobs?mock=empty          nobody has uploaded anything
//   /jobs?mock=admin          the user is an administrator
//   /jobs?mock=season         adds the season below
//   /jobs?mock=mapping        adds a season and a film waiting for their files to be checked
//   /jobs?mock=mapping,rejected   the app refuses two files of that season, and the film
//   /jobs?mock=mapping,pending    the app has not answered yet
//   /jobs?mock=mapping,slow       every change takes four seconds to check
//   /jobs?mock=mapping,failing    every change fails to be checked
//   /jobs?mock=importing      adds an import that is running and one that waits for the app
//   /jobs?mock=dub            adds a finished import of a dub: unmonitored, one step failed
//
// The choice is kept for the tab (sessionStorage), so it survives a reload; `?mock=` with
// nothing after it clears it. Names are made up.

import type { Job, JobDetail, JobFile, JobState } from "@/api/types";
import { mappingFilmJobId, mappingJobId } from "./mapping-fixtures";

export const scenarioNames = [
  "empty",
  "admin",
  "season",
  "mapping",
  "rejected",
  "pending",
  "slow",
  "failing",
  "importing",
  "dub",
] as const;
export type Scenario = (typeof scenarioNames)[number];

const storageKey = "fukku.mock";

/** The scenarios asked for in the address, or kept from an earlier page of this tab. */
export function readScenarios(): Scenario[] {
  const asked = new URLSearchParams(window.location.search).get("mock");
  if (asked !== null) sessionStorage.setItem(storageKey, asked);
  const names = (sessionStorage.getItem(storageKey) ?? "").split(",");
  return scenarioNames.filter((name) => names.includes(name));
}

const seasonId = "5ea5".padStart(32, "0");
const zip =
  "The Long Example Show With A Subtitle - Season 01 (2019) [1080p WEB-DL x265 10bit].zip";
const episodes = 24;
const episodeSize = 1_850_000_000;

function episodeName(n: number): string {
  const nn = String(n).padStart(2, "0");
  return `The.Long.Example.Show.With.A.Subtitle.S01E${nn}.An.Episode.Title.That.Goes.On.1080p.WEB-DL.x265.10bit-GROUP.mkv`;
}

export const seasonJob: Job = {
  id: seasonId,
  owner: "user1",
  state: "probed",
  attempt: 1,
  error: null,
  name: zip,
  fileCount: episodes + 1,
  totalSize: episodes * episodeSize,
  createdAt: "2026-01-01T11:00:00Z",
  updatedAt: "2026-01-01T11:40:00Z",
  expiresAt: "2026-01-15T11:40:00Z",
  actions: ["cancel"],
};

export const seasonFiles: JobFile[] = [
  {
    id: 100,
    path: `archives/${zip}`,
    name: zip,
    kind: "archive",
    size: episodes * episodeSize,
    status: "ok",
    reason: null,
    from: null,
    probe: null,
    extraction: null,
  },
  ...Array.from({ length: episodes }, (_, i): JobFile => {
    const refused = i === 6;
    return {
      id: 101 + i,
      path: `files/season/${episodeName(i + 1)}`,
      name: episodeName(i + 1),
      kind: "video",
      size: episodeSize + i * 1_000_000,
      status: refused ? "refused" : "ok",
      reason: refused ? "no_duration" : null,
      from: `archives/${zip}`,
      probe: null,
      extraction: null,
    };
  }),
];

/** Two jobs in `awaiting-confirmation`, for the mapping editor (`mapping-fixtures.ts`). */
export const mappingJobs: Job[] = [
  {
    id: mappingJobId,
    owner: "user1",
    state: "awaiting-confirmation",
    attempt: 1,
    error: null,
    name: "Lighthouse Letters - Season 1.zip",
    fileCount: 6,
    totalSize: 5 * episodeSize,
    createdAt: "2026-01-01T10:00:00Z",
    updatedAt: "2026-01-01T10:30:00Z",
    expiresAt: null,
    actions: ["cancel"],
  },
  {
    id: mappingFilmJobId,
    owner: "user1",
    state: "awaiting-confirmation",
    attempt: 1,
    error: null,
    name: "Sintel.2010.1080p.mkv",
    fileCount: 1,
    totalSize: episodeSize,
    createdAt: "2026-01-01T09:00:00Z",
    updatedAt: "2026-01-01T09:30:00Z",
    expiresAt: null,
    actions: ["cancel"],
  },
];

// ---- Imports under way and finished (#40) ------------------------------------------------

const hex = (n: number) => n.toString(16).padStart(32, "0");
export const importingJobId = hex(0x4b01);
export const waitingJobId = hex(0x4b02);
export const dubJobId = hex(0x4b03);

function importJob(id: string, state: JobState, name: string, hour: number): Job {
  const at = (minute: number) => `2026-01-02T${String(hour).padStart(2, "0")}:${minute}:00Z`;
  return {
    id,
    owner: "user1",
    state,
    attempt: 1,
    error: null,
    name,
    fileCount: 4,
    totalSize: 4 * episodeSize,
    createdAt: at(10),
    updatedAt: at(40),
    expiresAt: null,
    actions: state === "done" ? ["delete"] : ["cancel"],
  };
}

/** What the store recorded on the way to `last`, one minute apart. */
function historyTo(job: Job, last: JobState[]): JobDetail["history"] {
  const reached: JobState[] = [
    "uploading",
    "uploaded",
    "probing",
    "probed",
    "matched",
    "awaiting-confirmation",
    ...last,
  ];
  const start = new Date(job.createdAt).getTime();
  return reached.map((to, i) => ({
    at: new Date(start + i * 60_000).toISOString(),
    from: i === 0 ? null : (reached[i - 1] ?? null),
    to,
    actor: i === 0 ? job.owner : "fukku",
    note: "",
  }));
}

const episodeFile = (n: number) => `Lighthouse.Letters.S02E0${n}.1080p.WEB-DL.mkv`;
const importRows = (results: string[]) =>
  results.map((result, i) => ({ id: `r${i + 1}`, name: episodeFile(i + 1), result }));
const lighthouse = { app: "sonarr", titleId: 12, title: "Lighthouse Letters", year: 2019 } as const;

const importing = importJob(importingJobId, "importing", "Lighthouse Letters - Season 2.zip", 12);
const waiting: Job = {
  ...importJob(waitingJobId, "stalled", "Lighthouse Letters - Season 2 (second try).zip", 11),
  error: { code: "arr_unavailable", message: "The app is not answering." },
};
const dub = importJob(dubJobId, "done", "Sintel.2010.1080p.Spanish.mkv", 10);

/** `importing`: one import that moves and one that waits for the app. */
export const importingJobs: Job[] = [importing, waiting];
/** `dub`: a finished import that left the title unmonitored, with one step that failed. */
export const dubJobs: Job[] = [dub];

/** What `GET /api/jobs/{id}` adds for the jobs above. */
export const importDetails: Record<string, Partial<JobDetail>> = {
  [importingJobId]: {
    target: lighthouse,
    history: historyTo(importing, ["importing"]),
    import: { status: "running", rows: importRows(["imported", "imported", "", ""]) },
  },
  [waitingJobId]: {
    target: lighthouse,
    history: historyTo(waiting, ["importing", "stalled"]),
    import: { status: "running", rows: importRows(["imported", "", "", ""]) },
  },
  [dubJobId]: {
    target: { app: "radarr", titleId: 20, title: "Sintel", year: 2010 },
    history: historyTo(dub, ["importing", "imported", "post-processing", "done"]),
    import: {
      status: "done",
      rows: [{ id: "r1", name: "Sintel.2010.1080p.Spanish.mkv", result: "imported" }],
    },
    result: {
      titleLink: "https://media.example.com/web/index.html#!/details?id=0a1b2c",
      monitored: false,
      unmonitorReason: "dub_only",
      postProcessing: [
        { step: "mediaServerRefresh", status: "ok", detail: "" },
        { step: "subtitles", status: "failed", detail: "subtitle manager: no answer in 30 s" },
      ],
    },
  },
};
