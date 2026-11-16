// The confirm endpoint (#40) and the edits of the confirmation page (#38): a row's quality,
// languages and "leave out", each re-checked by the app.

import { useMutation, useQueryClient } from "@tanstack/react-query";
import { apiFetch } from "./client";
import { jobKey, jobsKey } from "./jobs";
import { type Proposal, patchRow, proposalKey, type RowEdit } from "./proposal";
import type { ConfirmRequest, Job } from "./types";

export const confirmJob = (id: string, body: ConfirmRequest) =>
  apiFetch<Job>(`/api/jobs/${encodeURIComponent(id)}/confirm`, {
    method: "POST",
    body: JSON.stringify(body),
  });

/**
 * Correct one row. While the request runs the app re-checks the row (`isPending`); the
 * answer replaces the row in the cached proposal, and the proposal is read again because
 * rows depend on each other (two files for one film, two rows for one episode).
 */
export function useEditRow(id: string) {
  const client = useQueryClient();
  return useMutation({
    mutationFn: ({ rowId, edit }: { rowId: string; edit: RowEdit }) => patchRow(id, rowId, edit),
    onSuccess: (row) => {
      client.setQueryData<Proposal>(proposalKey(id), (p) =>
        p ? { ...p, rows: p.rows.map((r) => (r.id === row.id ? row : r)) } : p,
      );
      void client.invalidateQueries({ queryKey: proposalKey(id) });
    },
  });
}

export function useConfirm(id: string) {
  const client = useQueryClient();
  return useMutation({
    mutationFn: (body: ConfirmRequest) => confirmJob(id, body),
    // Not awaited: the page moves on at once and the job page shows the fresh state.
    onSettled: () => {
      void client.invalidateQueries({ queryKey: jobsKey });
      void client.invalidateQueries({ queryKey: jobKey(id) });
      void client.invalidateQueries({ queryKey: proposalKey(id) });
    },
  });
}
