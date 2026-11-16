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

/** `language` is #39's; the codes the app adds itself are in the contract of #38. */
export type Notice = { code: string };

export type ProposalRow = {
  id: string;
  fileId: number;
  name: string;
  durationMs: number;
  skip: boolean;
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

export type Proposal = {
  /** `pending` while the match step has not finished. */
  state: "pending" | "ready";
  app: "radarr" | "sonarr" | "";
  target: { titleId: number; title: string; year: number } | null;
  rows: ProposalRow[];
  hints: Notice[];
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
