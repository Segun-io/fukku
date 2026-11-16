// The state of the mapping editor, kept apart from React so it can be tested alone.
//
// The proposal is the app's last verdict for every row. A user's edit is shown at once (the
// "overlay") with the row marked "checking" until the app's fresh verdict comes back; a
// failed check keeps the edit visible and offers a retry. Edits to one row are sent one at a
// time and a second edit made while the first is being checked is merged into the next one.

import type { Proposal, ProposalRow, RowEdit, RowEpisode, SeriesEpisode } from "@/api/proposal";

// ---- Edits ----------------------------------------------------------------------------

/**
 * `later` after `earlier`, as one edit. The season and the episodes describe one thing: the
 * later of the two wins and the other is dropped (the app works the season out of the
 * episodes, and renumbers the episodes for a new season).
 */
export function mergeEdits(earlier: RowEdit, later: RowEdit): RowEdit {
  const out: RowEdit = { ...earlier, ...later };
  if (later.episodeIds !== undefined && later.seasonNumber === undefined) {
    delete out.seasonNumber;
  }
  if (later.seasonNumber !== undefined && later.episodeIds === undefined) {
    delete out.episodeIds;
  }
  return out;
}

const episodeOf = (id: number, episodes: readonly SeriesEpisode[]) =>
  episodes.find((e) => e.id === id);

/**
 * What the row looks like with `edit` applied, before the app has answered: the same fields
 * the verdict will fill. A season change alone keeps the episode numbers in the new season,
 * as the app does; the episodes are then shown as unknown until the verdict.
 */
export function applyEdit(row: ProposalRow, edit: RowEdit, proposal: Proposal): ProposalRow {
  const out = { ...row };
  if (edit.skip !== undefined) out.skip = edit.skip;
  if (edit.episodeIds !== undefined) {
    const eps = edit.episodeIds
      .map((id) => episodeOf(id, proposal.episodes ?? []))
      .filter((e): e is SeriesEpisode => e !== undefined);
    out.episodes = eps.map(({ seasonNumber: _season, ...rest }): RowEpisode => rest);
    if (eps[0]) out.seasonNumber = eps[0].seasonNumber;
  } else if (edit.seasonNumber !== undefined) {
    const numbers = (row.episodes ?? []).map((e) => e.number);
    const eps = numbers
      .map((n) =>
        (proposal.episodes ?? []).find(
          (e) => e.seasonNumber === edit.seasonNumber && e.number === n,
        ),
      )
      .filter((e): e is SeriesEpisode => e !== undefined);
    out.seasonNumber = edit.seasonNumber;
    out.episodes = eps.map(({ seasonNumber: _season, ...rest }): RowEpisode => rest);
  }
  if (edit.qualityId !== undefined) {
    out.quality = proposal.qualities.find((q) => q.id === edit.qualityId) ?? row.quality;
  }
  if (edit.languageIds !== undefined) {
    const ids = edit.languageIds;
    out.languages = proposal.languages.filter((l) => ids.includes(l.id));
  }
  return out;
}

// ---- Rows that wait for the app --------------------------------------------------------

export type CheckStatus = "checking" | "failed";

/** A row with an edit the app has not answered yet, or has failed to check. */
export type Pending = {
  /** What the user changed, shown in the row meanwhile. */
  edit: RowEdit;
  status: CheckStatus;
  error: Error | null;
};

/** A bulk edit in flight, or failed. Its rows show "checking" or the failure. */
export type BulkPending = {
  rowIds: string[];
  status: CheckStatus;
  error: Error | null;
};

export type EditorState = {
  pending: Record<string, Pending>;
  bulk: BulkPending | null;
};

export const initialState: EditorState = { pending: {}, bulk: null };

export type EditorAction =
  /** The user changed `rowId`: show it and mark the row "checking". */
  | { type: "edited"; rowId: string; edit: RowEdit }
  /** The app answered for `rowId`. `more`: another edit of this row is waiting to be sent. */
  | { type: "checked"; rowId: string; more: boolean }
  | { type: "failed"; rowId: string; error: Error }
  | { type: "retry"; rowId: string }
  /** Drop the edit that failed; the row goes back to the app's last verdict. */
  | { type: "discard"; rowId: string }
  | { type: "bulkStarted"; rowIds: string[] }
  | { type: "bulkDone" }
  | { type: "bulkFailed"; error: Error }
  | { type: "bulkDiscard" };

export function editorReducer(state: EditorState, action: EditorAction): EditorState {
  switch (action.type) {
    case "edited": {
      const cur = state.pending[action.rowId];
      const edit = cur ? mergeEdits(cur.edit, action.edit) : action.edit;
      return {
        ...state,
        pending: { ...state.pending, [action.rowId]: { edit, status: "checking", error: null } },
      };
    }
    case "checked": {
      const cur = state.pending[action.rowId];
      if (!cur) return state;
      const pending = { ...state.pending };
      if (action.more) pending[action.rowId] = { ...cur, status: "checking", error: null };
      else delete pending[action.rowId];
      return { ...state, pending };
    }
    case "failed": {
      const cur = state.pending[action.rowId];
      if (!cur) return state;
      return {
        ...state,
        pending: {
          ...state.pending,
          [action.rowId]: { ...cur, status: "failed", error: action.error },
        },
      };
    }
    case "retry": {
      const cur = state.pending[action.rowId];
      if (!cur) return state;
      return {
        ...state,
        pending: { ...state.pending, [action.rowId]: { ...cur, status: "checking", error: null } },
      };
    }
    case "discard": {
      const pending = { ...state.pending };
      delete pending[action.rowId];
      return { ...state, pending };
    }
    case "bulkStarted":
      return { ...state, bulk: { rowIds: action.rowIds, status: "checking", error: null } };
    case "bulkDone":
    case "bulkDiscard":
      return { ...state, bulk: null };
    case "bulkFailed":
      return state.bulk
        ? { ...state, bulk: { ...state.bulk, status: "failed", error: action.error } }
        : state;
  }
}

/** The status the row shows, or `null` when the app's verdict on it is current. */
export function rowStatus(state: EditorState, rowId: string): Pending | BulkPending | null {
  const own = state.pending[rowId];
  if (own) return own;
  if (state.bulk?.rowIds.includes(rowId)) return state.bulk;
  return null;
}

/** The rows as shown: the app's last verdict with the edits it has not answered laid over. */
export function shownRows(proposal: Proposal, state: EditorState): ProposalRow[] {
  return proposal.rows.map((row) => {
    const own = state.pending[row.id];
    return own ? applyEdit(row, own.edit, proposal) : row;
  });
}

// ---- Summary and filter ----------------------------------------------------------------

/**
 * A row that needs the user: not skipped, and either not importable as it is (no episode,
 * the app's rejection, a warning). Such a row is imported only with an explicit override at
 * the confirmation (#40).
 */
export function needsAttention(row: ProposalRow): boolean {
  return !row.skip && !row.importable;
}

export type Summary = { toImport: number; skipped: number; attention: number };

export function summarize(rows: readonly ProposalRow[]): Summary {
  const out = { toImport: 0, skipped: 0, attention: 0 };
  for (const row of rows) {
    if (row.skip) out.skipped++;
    else if (row.importable) out.toImport++;
    else out.attention++;
  }
  return out;
}

/** The seasons the series has, in order. */
export function seasonsOf(episodes: readonly SeriesEpisode[]): number[] {
  return [...new Set(episodes.map((e) => e.seasonNumber))].sort((a, b) => a - b);
}

/** The episode that follows `last` in its season, if the series has one. */
export function nextEpisode(
  season: number,
  last: RowEpisode,
  episodes: readonly SeriesEpisode[],
): SeriesEpisode | undefined {
  return episodes
    .filter((e) => e.seasonNumber === season && e.number > last.number)
    .sort((a, b) => a.number - b.number)[0];
}
