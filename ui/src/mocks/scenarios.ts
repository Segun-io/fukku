// Extra data for `npm run dev:mock`, to look at the pages with content the fixtures of the
// tests do not have: an empty list, an administrator, and a season of 24 episodes with long
// names. Open any page with `?mock=` and one or more names, separated by commas:
//
//   /jobs?mock=empty          nobody has uploaded anything
//   /jobs?mock=admin          the user is an administrator
//   /jobs?mock=season         adds the season below
//   /jobs?mock=mapping        adds a season and a film waiting for their files to be checked
//
// The choice is kept for the tab (sessionStorage), so it survives a reload; `?mock=` with
// nothing after it clears it. Names are made up.

import type { Job, JobFile } from "@/api/types";
import { mappingFilmJobId, mappingJobId } from "./mapping-fixtures";

export const scenarioNames = ["empty", "admin", "season", "mapping"] as const;
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
