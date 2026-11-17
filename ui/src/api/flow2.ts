// The flow 2 endpoints (another language of a film already in the library): the sync-check
// clips (#53) and the confirmation as a merge (#55) or a second version (#56).

import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { apiFetch, isPermanent } from "./client";
import { jobKey, jobsKey } from "./jobs";
import type { Flow2ConfirmRequest, Job, Preview, PreviewRequest } from "./types";

const base = (jobId: string) => `/api/jobs/${encodeURIComponent(jobId)}`;

export const previewKey = (jobId: string) => ["preview", jobId] as const;

export const getPreview = (jobId: string, signal?: AbortSignal) =>
  apiFetch<Preview>(`${base(jobId)}/preview`, { signal });

/** A new request replaces the earlier clips. An empty request means the server's defaults. */
export const requestPreview = (jobId: string, body: PreviewRequest) =>
  apiFetch<Preview>(`${base(jobId)}/preview`, { method: "POST", body: JSON.stringify(body) });

export const confirmFlow2 = (jobId: string, body: Flow2ConfirmRequest) =>
  apiFetch<Job>(`${base(jobId)}/confirm`, { method: "POST", body: JSON.stringify(body) });

export const previewPollInterval = 2000;

/** The clips of the sync check. While the worker renders them (`pending`) it is asked again. */
export function usePreview(jobId: string, enabled: boolean) {
  return useQuery({
    queryKey: previewKey(jobId),
    queryFn: ({ signal }) => getPreview(jobId, signal),
    enabled,
    refetchInterval: (query) =>
      query.state.data?.state === "pending" ? previewPollInterval : false,
    retry: (count, err) => !isPermanent(err) && count < 1,
    refetchOnWindowFocus: false,
  });
}

/** Ask for new clips at an offset and audio track; the answer is the new (pending) preview. */
export function useRequestPreview(jobId: string) {
  const client = useQueryClient();
  return useMutation({
    mutationFn: (body: PreviewRequest) => requestPreview(jobId, body),
    onSuccess: (preview) => client.setQueryData<Preview>(previewKey(jobId), preview),
  });
}

/** Confirm a flow 2 job as a merge or a second version. The job page shows what follows. */
export function useConfirmFlow2(jobId: string) {
  const client = useQueryClient();
  return useMutation({
    mutationFn: (body: Flow2ConfirmRequest) => confirmFlow2(jobId, body),
    onSettled: () => {
      void client.invalidateQueries({ queryKey: jobsKey });
      void client.invalidateQueries({ queryKey: jobKey(jobId) });
      void client.invalidateQueries({ queryKey: previewKey(jobId) });
    },
  });
}
