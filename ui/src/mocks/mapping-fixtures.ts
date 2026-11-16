// Mock of the proposal API (#38): the "awkward season" shape of #46, and a film. A season
// with a double episode (E01 and E02 in one file), a file named E05 that is really E04, a
// Spanish dub, and an episode that already has a file. The mock re-checks every edit the way
// the real one does, so the screen can be driven end to end. Titles are invented.

import { HttpResponse, http } from "msw";
import type {
  BulkEdit,
  Notice,
  Proposal,
  ProposalRow,
  Ref,
  RowEdit,
  RowEpisode,
  SeriesEpisode,
} from "@/api/proposal";

const id = (n: number) => n.toString(16).padStart(32, "0");

/** The jobs that have a proposal: a season (a show) and a film. */
export const mappingJobId = id(0x4a01);
export const mappingFilmJobId = id(0x4a02);

const min = 60_000;
const runtime = 22 * min;

const episodeTitles: Record<number, string[]> = {
  1: [
    "The Keeper",
    "Low Tide",
    "A Letter Unsent",
    "Fog Signals",
    "The Second Lamp",
    "Salt",
    "Return",
  ],
  2: ["Harbour Lights", "The Ferry", "Paper Boats", "Undertow"],
};

export const seriesEpisodes: SeriesEpisode[] = Object.entries(episodeTitles).flatMap(
  ([season, titles]) =>
    titles.map((title, i) => ({
      id: Number(season) * 100 + i + 1,
      seasonNumber: Number(season),
      number: i + 1,
      title,
      runtimeMs: runtime,
      // Season 1, episode 7 is in the library already: it cannot be imported in this version.
      hasFile: season === "1" && i === 6,
    })),
);

const minFormatScore = 0;
const english: Ref = { id: 1, name: "English" };
const spanish: Ref = { id: 3, name: "Spanish" };
const qualities: Ref[] = [
  { id: 3, name: "WEBDL-1080p" },
  { id: 7, name: "Bluray-1080p" },
  { id: 5, name: "WEBDL-720p" },
];

type Stored = {
  id: string;
  fileId: number;
  name: string;
  durationMs: number;
  skip: boolean;
  seasonNumber?: number;
  episodeIds?: number[];
  quality: Ref | null;
  languages: Ref[];
  customFormats: string[];
  customFormatScore: number;
};

type MockProposal = {
  app: "radarr" | "sonarr";
  target: { titleId: number; title: string; year: number };
  rows: Stored[];
  /** The next PATCH or bulk answers with this status, once (a test or a demo sets it). */
  fail?: { status: number; code: string };
};

function season(): Stored[] {
  const file = (
    n: number,
    episodeNumber: number,
    durationMs: number,
    extra: Partial<Stored> = {},
  ) => ({
    id: `r${n}`,
    fileId: 200 + n,
    name: `Lighthouse.Letters.S01E${String(episodeNumber).padStart(2, "0")}.1080p.WEB-DL.mkv`,
    durationMs,
    skip: false,
    seasonNumber: 1,
    episodeIds: [100 + episodeNumber],
    quality: qualities[0] ?? null,
    languages: [english],
    customFormats: [],
    customFormatScore: 0,
    ...extra,
  });
  return [
    // A double: one file with the length of two episodes, which the app reads as E01 only.
    file(1, 1, 2 * runtime + 40_000),
    file(2, 3, runtime + 30_000),
    // Named E05; the episode it holds is E04.
    file(3, 5, runtime + 20_000),
    // A Spanish dub.
    file(4, 6, runtime + 10_000, {
      languages: [spanish],
      customFormats: ["Not original language"],
      customFormatScore: -10000,
    }),
    // E07 already has a file in the library.
    file(5, 7, runtime + 50_000),
  ];
}

let proposals = new Map<string, MockProposal>();

export function resetMapping() {
  proposals = new Map<string, MockProposal>([
    [
      mappingJobId,
      {
        app: "sonarr",
        target: { titleId: 12, title: "Lighthouse Letters", year: 2019 },
        rows: season(),
      },
    ],
    [
      mappingFilmJobId,
      {
        app: "radarr",
        target: { titleId: 20, title: "Sintel", year: 2010 },
        rows: [
          {
            id: "r1",
            fileId: 301,
            name: "Sintel.2010.1080p.mkv",
            durationMs: 888 * 1000,
            skip: false,
            quality: qualities[0] ?? null,
            languages: [english],
            customFormats: [],
            customFormatScore: 0,
          },
        ],
      },
    ],
  ]);
}
resetMapping();

/** The proposal as the mock serves it now, for the confirm handler. */
export function currentProposal(jobId: string): Proposal | undefined {
  const p = proposals.get(jobId);
  return p && proposalOf(p);
}

/** Makes the next edit of this job's proposal fail with this status, once. */
export function failNextEdit(jobId: string, status: number, code: string) {
  const p = proposals.get(jobId);
  if (p) p.fail = { status, code };
}

// ---- The re-check -----------------------------------------------------------------------

const epById = new Map(seriesEpisodes.map((e) => [e.id, e]));
const rowEpisode = ({ seasonNumber: _s, ...e }: SeriesEpisode): RowEpisode => e;

function view(p: MockProposal, s: Stored): ProposalRow {
  const eps = (s.episodeIds ?? []).map((i) => epById.get(i)).filter((e) => e !== undefined);
  const show = p.app === "sonarr";
  const warnings: Notice[] = [];
  const expected = eps.reduce((sum, e) => sum + e.runtimeMs, 0);
  const double = eps.length === 1 && Math.abs(s.durationMs / expected - 2) < 0.1;
  if (double) warnings.push({ code: "double_episode" });
  else if (expected > 0 && Math.abs(s.durationMs - expected) > expected * 0.35) {
    warnings.push({ code: "runtime_mismatch" });
  }
  if (eps.some((e) => e.hasFile)) warnings.push({ code: "existing_file" });
  const mine = new Set(eps.map((e) => e.id));
  const clash = p.rows.some(
    (o) => o !== s && !o.skip && (o.episodeIds ?? []).some((i) => mine.has(i)),
  );
  if (clash && !s.skip) warnings.push({ code: "duplicate_episode" });
  // #39: a custom-format score below the profile's minimum, with the facts.
  if (s.customFormatScore < minFormatScore) {
    warnings.push({
      code: "language",
      customFormats: [...s.customFormats],
      score: s.customFormatScore,
      minFormatScore,
    });
  }
  const row: ProposalRow = {
    id: s.id,
    fileId: s.fileId,
    name: s.name,
    durationMs: s.durationMs,
    skip: s.skip || eps.some((e) => e.hasFile),
    quality: s.quality,
    languages: s.languages,
    customFormats: s.customFormats,
    customFormatScore: s.customFormatScore,
    rejections: [],
    warnings,
    importable: false,
  };
  if (show) {
    if (s.seasonNumber !== undefined) row.seasonNumber = s.seasonNumber;
    row.episodes = eps.map(rowEpisode);
  }
  row.importable = !row.skip && (!show || eps.length > 0) && warnings.length === 0;
  return row;
}

function proposalOf(p: MockProposal): Proposal {
  const rows = p.rows.map((s) => view(p, s));
  const out: Proposal = {
    state: "ready",
    app: p.app,
    target: p.target,
    rows,
    hints: [],
    qualities,
    languages: [english, spanish],
  };
  if (p.app === "sonarr") out.episodes = seriesEpisodes;
  const mismatched = rows.filter((r) => r.warnings.some((w) => w.code === "runtime_mismatch"));
  if (rows.length > 1 && mismatched.length > rows.length / 2) out.hints.push({ code: "dvd_order" });
  return out;
}

const fail = (status: number, code: string, message: string) =>
  HttpResponse.json({ error: { code, message } }, { status });

function renumber(s: Stored, seasonNumber: number, shift: number): string | null {
  const numbers = (s.episodeIds ?? []).map((i) => (epById.get(i)?.number ?? 0) + shift);
  const ids: number[] = [];
  for (const n of numbers) {
    const e = seriesEpisodes.find((x) => x.seasonNumber === seasonNumber && x.number === n);
    if (!e) return `Season ${seasonNumber} has no episode ${n}.`;
    ids.push(e.id);
  }
  s.seasonNumber = seasonNumber;
  s.episodeIds = ids;
  return null;
}

function applyRowEdit(p: MockProposal, s: Stored, e: RowEdit): string | null {
  if (e.episodeIds !== undefined) {
    const eps = e.episodeIds.map((i) => epById.get(i));
    if (eps.some((x) => !x)) return "An episode is not in this series.";
    const seasons = new Set(eps.map((x) => x?.seasonNumber));
    if (seasons.size > 1) return "The episodes of one file must be in one season.";
    s.episodeIds = [...e.episodeIds].sort(
      (a, b) => (epById.get(a)?.number ?? 0) - (epById.get(b)?.number ?? 0),
    );
    const first = eps[0];
    if (first) s.seasonNumber = first.seasonNumber;
  } else if (e.seasonNumber !== undefined) {
    const err = renumber(s, e.seasonNumber, 0);
    if (err) return err;
  }
  if (e.qualityId !== undefined) {
    const q = qualities.find((x) => x.id === e.qualityId);
    if (!q) return "Unknown quality.";
    s.quality = q;
  }
  if (e.languageIds !== undefined) {
    s.languages = [english, spanish].filter((l) => e.languageIds?.includes(l.id));
  }
  if (e.skip !== undefined) s.skip = e.skip;
  void p;
  return null;
}

export const mappingHandlers = [
  http.get("/api/jobs/:id/proposal", ({ params }) => {
    const p = proposals.get(String(params.id));
    if (!p) return fail(404, "not_found", "No such job.");
    return HttpResponse.json(proposalOf(p));
  }),

  http.patch("/api/jobs/:id/proposal/rows/:rowId", async ({ params, request }) => {
    const p = proposals.get(String(params.id));
    if (!p) return fail(404, "not_found", "No such job.");
    if (p.fail) {
      const { status, code } = p.fail;
      p.fail = undefined;
      return fail(status, code, "Mock failure.");
    }
    const s = p.rows.find((r) => r.id === params.rowId);
    if (!s) return fail(404, "not_found", "No such row.");
    const err = applyRowEdit(p, s, (await request.json()) as RowEdit);
    if (err) return fail(400, "bad_request", err);
    const row = proposalOf(p).rows.find((r) => r.id === s.id);
    return HttpResponse.json(row);
  }),

  http.post("/api/jobs/:id/proposal/bulk", async ({ params, request }) => {
    const p = proposals.get(String(params.id));
    if (!p) return fail(404, "not_found", "No such job.");
    if (p.fail) {
      const { status, code } = p.fail;
      p.fail = undefined;
      return fail(status, code, "Mock failure.");
    }
    const body = (await request.json()) as BulkEdit;
    const chosen = p.rows.filter((r) => body.rowIds.includes(r.id));
    // All or nothing: the rows change only when every one of them can.
    const changed = chosen.map((s) => structuredClone(s));
    for (const s of changed) {
      const err =
        "shift" in body
          ? renumber(s, s.seasonNumber ?? 1, body.shift)
          : renumber(s, body.seasonNumber, 0);
      if (err) return fail(400, "bad_request", err);
    }
    chosen.forEach((s, i) => {
      Object.assign(s, changed[i]);
    });
    const all = proposalOf(p).rows;
    return HttpResponse.json({ rows: all.filter((r) => body.rowIds.includes(r.id)) });
  }),
];
