// The match endpoints of #37: which apps are configured, the title lookups, and choosing
// (or un-choosing) the title of a job.

import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { apiFetch, isPermanent } from "./client";
import { jobKey, jobsKey } from "./jobs";
import type { Apps, Job, MatchRequest, MovieHit, SeriesHit } from "./types";

export const appsKey = ["apps"] as const;

export const getApps = (signal?: AbortSignal) => apiFetch<Apps>("/api/apps", { signal });

export type LookupKind = "movies" | "series";

export function lookupTitles(
  kind: "movies",
  term: string,
  signal?: AbortSignal,
): Promise<{ results: MovieHit[] }>;
export function lookupTitles(
  kind: "series",
  term: string,
  signal?: AbortSignal,
): Promise<{ results: SeriesHit[] }>;
export function lookupTitles(kind: LookupKind, term: string, signal?: AbortSignal) {
  return apiFetch(`/api/lookup/${kind}?term=${encodeURIComponent(term)}`, { signal });
}

const matchPath = (id: string) => `/api/jobs/${encodeURIComponent(id)}/match`;

export const matchJob = (id: string, body: MatchRequest) =>
  apiFetch<Job>(matchPath(id), { method: "POST", body: JSON.stringify(body) });
export const unmatchJob = (id: string) => apiFetch<Job>(matchPath(id), { method: "DELETE" });

const retry = (count: number, err: Error) => !isPermanent(err) && count < 1;

/** The configured apps with their profiles, root folders and defaults. Changes rarely. */
export function useApps() {
  return useQuery({
    queryKey: appsKey,
    queryFn: ({ signal }) => getApps(signal),
    staleTime: 5 * 60_000,
    retry,
  });
}

/** A title search. Nothing is asked until `term` is not empty. */
export function useLookup(kind: LookupKind, term: string) {
  return useQuery({
    queryKey: ["lookup", kind, term] as const,
    queryFn: async ({ signal }): Promise<(MovieHit | SeriesHit)[]> =>
      (
        await (kind === "movies"
          ? lookupTitles("movies", term, signal)
          : lookupTitles("series", term, signal))
      ).results,
    enabled: term !== "",
    staleTime: 60_000,
    retry: false,
  });
}

/** Choose the title of a job, or take the choice back (`null`: `DELETE`). Refreshes the job after. */
export function useMatchAction(id: string) {
  const client = useQueryClient();
  return useMutation({
    mutationFn: (body: MatchRequest | null) => (body ? matchJob(id, body) : unmatchJob(id)),
    onSettled: async () => {
      await client.invalidateQueries({ queryKey: jobsKey });
      await client.invalidateQueries({ queryKey: jobKey(id) });
    },
  });
}
