import { keepPreviousData, useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { jobPollInterval, jobsPollInterval } from "@/lib/job-text";
import { apiFetch, isPermanent } from "./client";
import type { Job, JobDetail, JobFilter, JobList } from "./types";

const post = { method: "POST" } as const;

function jobPath(id: string, action?: string): string {
  return `/api/jobs/${encodeURIComponent(id)}${action ? `/${action}` : ""}`;
}

export function listJobs(filter: JobFilter = {}, signal?: AbortSignal): Promise<JobList> {
  const q = new URLSearchParams();
  for (const s of filter.states ?? []) q.append("state", s);
  if (filter.mine) q.set("mine", "true");
  if (filter.limit !== undefined) q.set("limit", String(filter.limit));
  if (filter.offset !== undefined) q.set("offset", String(filter.offset));
  const qs = q.toString();
  return apiFetch<JobList>(`/api/jobs${qs ? `?${qs}` : ""}`, { signal });
}

export const getJob = (id: string, signal?: AbortSignal) =>
  apiFetch<JobDetail>(jobPath(id), { signal });
export const createJob = () => apiFetch<Job>("/api/jobs", post);
export const submitJob = (id: string) => apiFetch<Job>(jobPath(id, "submit"), post);
export const cancelJob = (id: string) => apiFetch<Job>(jobPath(id, "cancel"), post);
export const retryJob = (id: string) => apiFetch<Job>(jobPath(id, "retry"), post);
export const deleteJob = (id: string) => apiFetch<void>(jobPath(id), { method: "DELETE" });

export const jobsKey = ["jobs"] as const;
export const jobKey = (id: string) => ["job", id] as const;

const retry = (count: number, err: Error) => !isPermanent(err) && count < 1;

// Polling (#13): a refetch interval that depends on the data, so a job that stops moving
// stops being asked about. Hidden tabs are not polled (`refetchIntervalInBackground` off).
export function useJobs(filter: JobFilter = {}) {
  return useQuery({
    queryKey: [...jobsKey, filter],
    queryFn: ({ signal }) => listJobs(filter, signal),
    retry,
    placeholderData: keepPreviousData,
    refetchInterval: (query) =>
      query.state.data ? jobsPollInterval(query.state.data.jobs.map((j) => j.state)) : false,
    refetchIntervalInBackground: false,
  });
}

export function useJob(id: string) {
  return useQuery({
    queryKey: jobKey(id),
    queryFn: ({ signal }) => getJob(id, signal),
    retry,
    refetchInterval: (query) => jobPollInterval(query.state.data?.state),
    refetchIntervalInBackground: false,
  });
}

export type JobMutation = "cancel" | "retry" | "delete";

/** Cancel, retry or delete a job; the list and the job are refreshed afterwards. */
export function useJobAction(id: string) {
  const client = useQueryClient();
  return useMutation({
    mutationFn: async (action: JobMutation): Promise<void> => {
      switch (action) {
        case "cancel":
          await cancelJob(id);
          return;
        case "retry":
          await retryJob(id);
          return;
        case "delete":
          await deleteJob(id);
          return;
      }
    },
    onSettled: async (_data, _error, action) => {
      await client.invalidateQueries({ queryKey: jobsKey });
      // A deleted job has nothing left to refresh; the page leaves it.
      if (action !== "delete") await client.invalidateQueries({ queryKey: jobKey(id) });
    },
  });
}
