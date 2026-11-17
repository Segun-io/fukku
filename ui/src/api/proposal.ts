// The proposal endpoints of #38: what the app makes of each file, and the user's edits, each
// re-checked through the app. Rows are addressed by id; no request carries a path.

import { useQuery } from "@tanstack/react-query";
import { apiFetch, isPermanent } from "./client";

export type Ref = { id: number; name: string };

/** An episode as a row lists it (the season is the row's own). */
export type RowEpisode = {
  id: number;
  number: number;
  title: string;
  runtimeMs: number;
  hasFile: boolean;
};

/** An episode of the series, as the pickers offer it. */
export type SeriesEpisode = RowEpisode & { seasonNumber: number };

export type Rejection = { reason: string; permanent: boolean };

/**
 * A warning or a hint, by its code (#38: `not_offered`, `existing_file`, `double_episode`,
 * `runtime_mismatch`, `duplicate_episode`, `duplicate_film`; hint `dvd_order`). `language`
 * (#39) carries its facts: the matching custom formats, the row's score and the profile's minimum.
 */
export type Notice = {
  code: string;
  customFormats?: string[];
  score?: number;
  minFormatScore?: number;
};

export type ProposalRow = {
  id: string;
  fileId: number;
  name: string;
  durationMs: number;
  skip: boolean;
  /**
   * Who left the row out while `skip` is set: the user, or the server (the app did not offer
   * the file, or an episode of it has a file and the policy does not replace it). The user can
   * record a skip on a row the server skips: it keeps its own file and gets no version (#102).
   */
  skippedBy?: "user" | "server";
  /** Shows only. Absent while unknown. */
  seasonNumber?: number;
  /** Shows only. */
  episodes?: RowEpisode[];
  /** `null`: the app did not offer the file. */
  quality: Ref | null;
  languages: Ref[];
  customFormats: string[];
  customFormatScore: number;
  rejections: Rejection[];
  warnings: Notice[];
  importable: boolean;
};

/** One season of the upload's rows: how many of its episodes already have a file (#57). */
export type SeasonDubSeason = { season: number; episodes: number; existing: number; new: number };

/**
 * What to do with rows whose episodes already have a file (#57); absent when there are none.
 * `ask`: the confirmation must send one of `offers` as `seasonDub`. `decided`: the server's
 * setting fixes the policy and the question is skipped. With neither, those rows are left out.
 * Offers are `replace`, `subtitlesOnly` and `version` (#58).
 */
export type SeasonDubNotOffered = { offer: string; reason: string };

export type SeasonDub = {
  policy: string;
  ask: boolean;
  decided?: string;
  offers: string[];
  seasons: SeasonDubSeason[];
  /** Why a policy of the server's order is not among the offers (#58): `version`, with a reason. */
  notOffered?: SeasonDubNotOffered[];
  /** What keeping the rows as second versions would be (#58); present while `version` is offered. */
  version?: EpisodeVersionsView;
};

/** One row whose episode has a file, as a second version would be. */
export type EpisodeVersionRow = {
  row: string;
  episodeId: number;
  seasonNumber: number;
  episodeNumber: number;
  /** The version's file name; empty while the label or the row has a problem. */
  name: string;
  /** Empty, or why the row cannot be a version (`double_episode`, `shared_file`, `no_file`...). */
  problem: string;
};

/** Why the proposed label cannot be used (`label_empty`, `label_episode`...); `message` is English. */
export type EpisodeLabelProblem = { reason: string; token?: string; message: string };

export type EpisodeVersionsView = {
  /** The proposed label; empty when none can be proposed. */
  label: string;
  language: string;
  problem: EpisodeLabelProblem | null;
  /** The rows whose episode has a file. */
  rows: EpisodeVersionRow[];
  /** `untracked_by_sonarr`, `adopted_without_episode_file`, `subtitles_not_handed_over`. */
  notices: string[];
};

export type Proposal = {
  /** `pending` while the match step has not finished. */
  state: "pending" | "ready";
  app: "radarr" | "sonarr" | "";
  target: { titleId: number; title: string; year: number } | null;
  rows: ProposalRow[];
  hints: Notice[];
  seasonDub?: SeasonDub;
  /** Absent for a film. */
  episodes?: SeriesEpisode[];
  qualities: Ref[];
  languages: Ref[];
};

/** Any of these in one `PATCH`. */
export type RowEdit = {
  skip?: boolean;
  seasonNumber?: number;
  episodeIds?: number[];
  qualityId?: number;
  languageIds?: number[];
};

export type BulkEdit =
  | { rowIds: string[]; shift: number }
  | { rowIds: string[]; seasonNumber: number };

export const proposalKey = (jobId: string) => ["proposal", jobId] as const;

const base = (jobId: string) => `/api/jobs/${encodeURIComponent(jobId)}/proposal`;

export const getProposal = (jobId: string, signal?: AbortSignal) =>
  apiFetch<Proposal>(base(jobId), { signal });

export const patchRow = (jobId: string, rowId: string, edit: RowEdit) =>
  apiFetch<ProposalRow>(`${base(jobId)}/rows/${encodeURIComponent(rowId)}`, {
    method: "PATCH",
    body: JSON.stringify(edit),
  });

export const bulkEdit = (jobId: string, edit: BulkEdit) =>
  apiFetch<{ rows: ProposalRow[] }>(`${base(jobId)}/bulk`, {
    method: "POST",
    body: JSON.stringify(edit),
  });

export const proposalPollInterval = 2000;

/** The proposal. While the app is still looking at the files (`pending`) it is asked again. */
export function useProposal(jobId: string) {
  return useQuery({
    queryKey: proposalKey(jobId),
    queryFn: ({ signal }) => getProposal(jobId, signal),
    refetchInterval: (query) =>
      query.state.data?.state === "pending" ? proposalPollInterval : false,
    retry: (count, err) => !isPermanent(err) && count < 1,
    // Edits are written into the cache by hand; a refetch on focus would only race them.
    refetchOnWindowFocus: false,
  });
}
