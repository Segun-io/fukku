// The editing of the proposal: every change is a request, sent one at a time per row, and the
// answer (the app's fresh verdict for the row) replaces the row in the cache.

import { useQueryClient } from "@tanstack/react-query";
import { useCallback, useReducer, useRef } from "react";
import { ApiError } from "@/api/client";
import {
  type BulkEdit,
  bulkEdit,
  type Proposal,
  type ProposalRow,
  patchRow,
  proposalKey,
  type RowEdit,
} from "@/api/proposal";
import { editorReducer, initialState, mergeEdits } from "./state";

const asError = (e: unknown) => (e instanceof Error ? e : new Error(String(e)));

export function useMapping(jobId: string) {
  const client = useQueryClient();
  const [state, dispatch] = useReducer(editorReducer, initialState);
  // Edits not yet sent, by row. A row in `busy` has a request in flight; the loop that sent it
  // takes the next edit when the answer is in.
  const queue = useRef(new Map<string, RowEdit>());
  const busy = useRef(new Set<string>());
  const bulkRequest = useRef<BulkEdit | null>(null);
  const bulkBusy = useRef(false);

  const key = proposalKey(jobId);

  const write = useCallback(
    (rows: ProposalRow[]) =>
      client.setQueryData<Proposal>(
        key,
        (old) => old && { ...old, rows: old.rows.map((r) => rows.find((n) => n.id === r.id) ?? r) },
      ),
    [client, key],
  );

  // Other rows and the hints depend on every row (a duplicate, the order of the episodes), so
  // the proposal is read again once nothing is in flight. A read that is under way when an
  // edit starts is cancelled: its answer would be older than the edit's.
  const refresh = useCallback(() => {
    if (busy.current.size === 0 && !bulkBusy.current)
      void client.invalidateQueries({ queryKey: key });
  }, [client, key]);

  const drain = useCallback(
    async (rowId: string) => {
      busy.current.add(rowId);
      try {
        for (;;) {
          const edit = queue.current.get(rowId);
          if (!edit) return;
          queue.current.delete(rowId);
          await client.cancelQueries({ queryKey: key });
          try {
            const row = await patchRow(jobId, rowId, edit);
            write([row]);
            dispatch({ type: "checked", rowId, more: queue.current.has(rowId) });
          } catch (e) {
            // The edit stays queued, merged with any made meanwhile: a retry sends it all.
            queue.current.set(rowId, mergeEdits(edit, queue.current.get(rowId) ?? {}));
            dispatch({ type: "failed", rowId, error: asError(e) });
            // A 409 means the rows moved on: show what the app has now beside the edit.
            if (e instanceof ApiError && e.status === 409)
              void client.invalidateQueries({ queryKey: key });
            return;
          }
        }
      } finally {
        busy.current.delete(rowId);
        if (!queue.current.has(rowId)) refresh();
      }
    },
    [client, jobId, key, refresh, write],
  );

  const edit = useCallback(
    (rowId: string, change: RowEdit) => {
      dispatch({ type: "edited", rowId, edit: change });
      queue.current.set(rowId, mergeEdits(queue.current.get(rowId) ?? {}, change));
      if (!busy.current.has(rowId)) void drain(rowId);
    },
    [drain],
  );

  const retry = useCallback(
    (rowId: string) => {
      dispatch({ type: "retry", rowId });
      if (!busy.current.has(rowId)) void drain(rowId);
    },
    [drain],
  );

  const discard = useCallback((rowId: string) => {
    queue.current.delete(rowId);
    dispatch({ type: "discard", rowId });
  }, []);

  const runBulk = useCallback(
    async (change: BulkEdit) => {
      if (bulkBusy.current) return;
      bulkBusy.current = true;
      bulkRequest.current = change;
      dispatch({ type: "bulkStarted", rowIds: change.rowIds });
      try {
        await client.cancelQueries({ queryKey: key });
        const answer = await bulkEdit(jobId, change);
        write(answer.rows);
        dispatch({ type: "bulkDone" });
      } catch (e) {
        dispatch({ type: "bulkFailed", error: asError(e) });
        if (e instanceof ApiError && e.status === 409)
          void client.invalidateQueries({ queryKey: key });
      } finally {
        bulkBusy.current = false;
        refresh();
      }
    },
    [client, jobId, key, refresh, write],
  );

  const retryBulk = useCallback(() => {
    if (bulkRequest.current) void runBulk(bulkRequest.current);
  }, [runBulk]);

  const discardBulk = useCallback(() => {
    bulkRequest.current = null;
    dispatch({ type: "bulkDiscard" });
  }, []);

  return { state, edit, retry, discard, bulk: runBulk, retryBulk, discardBulk };
}
