import { Link } from "@tanstack/react-router";
import {
  type ColumnDef,
  getCoreRowModel,
  getFilteredRowModel,
  type RowSelectionState,
  useReactTable,
} from "@tanstack/react-table";
import { Ban, ListTree, TriangleAlert } from "lucide-react";
import { useEffect, useMemo, useRef, useState } from "react";
import type { Proposal } from "@/api/proposal";
import { inputClass, selectClass } from "@/components/patterns/field";
import { StatusLabel } from "@/components/patterns/status-label";
import { Notice } from "@/components/shell/notice";
import { Button } from "@/components/ui/button";
import { useT } from "@/i18n";
import { cn } from "@/lib/utils";
import {
  EpisodesCell,
  FileCell,
  type Item,
  LanguagesCell,
  MappingProvider,
  NotesCell,
  QualityCell,
  SeasonCell,
  SkipCell,
} from "./row-cells";
import { needsAttention, rowStatus, seasonsOf, shownRows, summarize } from "./state";
import { useMapping } from "./use-mapping";

const appNames = { radarr: "Radarr", sonarr: "Sonarr", "": "" } as const;

// How a row is laid out. A file is its label first (the name over its figures, and its state
// at the far edge), then the controls in columns, then what is against it in sentences that
// get the full width. The same areas serve the table (from 64rem, where the columns have
// headers) and the list below it, where each control carries its caption.
const layouts = {
  show: {
    columns: "grid-cols-[2.75rem_9rem_minmax(0,1fr)_10.5rem_minmax(5rem,7rem)_6rem]",
    head: "[grid-template-areas:'select_season_episodes_quality_languages_skip']",
    wide: "[grid-template-areas:'select_file_file_file_file_file'_'._season_episodes_quality_languages_skip'_'._notes_notes_notes_notes_notes']",
    narrow: cn(
      "grid-cols-[2.75rem_5.5rem_minmax(0,1fr)]",
      "[grid-template-areas:'select_file_file'_'season_season_episodes'_'quality_quality_quality'_'languages_languages_skip'_'notes_notes_notes']",
      "sm:grid-cols-[2.75rem_5.5rem_minmax(0,1fr)_11rem]",
      "sm:[grid-template-areas:'select_file_file_file'_'season_season_episodes_quality'_'languages_languages_languages_skip'_'notes_notes_notes_notes']",
    ),
  },
  film: {
    columns: "grid-cols-[14rem_minmax(0,1fr)_6rem]",
    head: "[grid-template-areas:'quality_languages_skip']",
    wide: "[grid-template-areas:'file_file_file'_'quality_languages_skip'_'notes_notes_notes']",
    narrow: cn(
      "grid-cols-[minmax(0,1fr)_auto]",
      "[grid-template-areas:'file_file'_'quality_quality'_'languages_skip'_'notes_notes']",
    ),
  },
} as const;

/** Where each cell stands in the areas above, and how it sits on its line. */
const place: Record<string, string> = {
  select: "[grid-area:select]",
  file: "[grid-area:file] min-w-0 self-center",
  season: "[grid-area:season] min-w-0",
  episodes: "[grid-area:episodes] min-w-0",
  quality: "[grid-area:quality] min-w-0",
  languages: "[grid-area:languages] min-w-0 self-center",
  skip: "[grid-area:skip] self-center justify-self-start",
  notes: "[grid-area:notes] min-w-0 empty:hidden",
};

/** True from the width where the rows are a table; below it they are cards. */
function useWide(query = "(min-width: 64rem)"): boolean {
  const [wide, setWide] = useState(() =>
    typeof window.matchMedia === "function" ? window.matchMedia(query).matches : true,
  );
  useEffect(() => {
    if (typeof window.matchMedia !== "function") return;
    const mq = window.matchMedia(query);
    const on = () => setWide(mq.matches);
    on();
    mq.addEventListener("change", on);
    return () => mq.removeEventListener("change", on);
  }, [query]);
  return wide;
}

export function MappingView({ jobId, proposal }: { jobId: string; proposal: Proposal }) {
  const { t } = useT();
  const mapping = useMapping(jobId);
  const wide = useWide();
  const show = proposal.app === "sonarr";
  const appName = appNames[proposal.app];

  const items = useMemo<Item[]>(
    () =>
      shownRows(proposal, mapping.state).map((row) => ({
        row,
        status: rowStatus(mapping.state, row.id),
      })),
    [proposal, mapping.state],
  );

  const [onlyAttention, setOnlyAttention] = useState(false);
  const [selection, setSelection] = useState<RowSelectionState>({});

  const columns = useMemo<ColumnDef<Item>[]>(
    () => [
      { id: "select", header: () => null },
      { id: "file", accessorFn: (i) => i.row.name },
      { id: "season", accessorFn: (i) => i.row.seasonNumber, enableHiding: true },
      { id: "episodes", accessorFn: (i) => i.row.episodes?.length },
      { id: "quality", accessorFn: (i) => i.row.quality?.name },
      { id: "languages", accessorFn: (i) => i.row.languages.length },
      { id: "skip", accessorFn: (i) => i.row.skip },
      // Kept in the table for the filter; a row being checked stays in view.
      {
        id: "attention",
        accessorFn: (i) => needsAttention(i.row) || i.status !== null,
        filterFn: "equals",
      },
      { id: "notes", accessorFn: (i) => i.row.warnings.length },
    ],
    [],
  );

  const columnFilters = useMemo(
    () => (onlyAttention ? [{ id: "attention", value: true }] : []),
    [onlyAttention],
  );
  const columnVisibility = useMemo(
    () => ({ attention: false, select: show, season: show, episodes: show }),
    [show],
  );

  const table = useReactTable({
    data: items,
    columns,
    getRowId: (item) => item.row.id,
    getCoreRowModel: getCoreRowModel(),
    getFilteredRowModel: getFilteredRowModel(),
    enableRowSelection: true,
    onRowSelectionChange: setSelection,
    // There are no pages. Left on, the table resets its page index in a microtask each time
    // the filters change identity, which sets state, which renders again: with a filter list
    // made on every render that never ends, and after a real click it froze the page.
    autoResetPageIndex: false,
    state: { rowSelection: selection, columnFilters, columnVisibility },
  });

  const rows = table.getRowModel().rows;
  const selectedIds = table.getFilteredSelectedRowModel().rows.map((r) => r.id);
  const summary = summarize(items.map((i) => i.row));
  const checking =
    mapping.state.bulk?.status === "checking" || items.some((i) => i.status?.status === "checking");
  const failed = items.some((i) => i.status?.status === "failed");

  const ctx = {
    proposal,
    appName,
    edit: mapping.edit,
    retry: (item: Item) => {
      if (mapping.state.bulk?.rowIds.includes(item.row.id) && !mapping.state.pending[item.row.id]) {
        mapping.retryBulk();
      } else mapping.retry(item.row.id);
    },
    discard: (item: Item) => {
      if (mapping.state.bulk?.rowIds.includes(item.row.id) && !mapping.state.pending[item.row.id]) {
        mapping.discardBulk();
      } else mapping.discard(item.row.id);
    },
  };

  const canContinue = summary.toImport + summary.attention > 0 && !checking && !failed;
  const layout = layouts[show ? "show" : "film"];
  const rowLook = "grid gap-x-3 gap-y-3 px-4 py-4 data-selected:bg-primary-soft/40";

  return (
    <MappingProvider value={ctx}>
      <div className="grid gap-6">
        {proposal.hints.map((h) =>
          h.code === "dvd_order" ? (
            <Notice
              key={h.code}
              tone="caution"
              icon={ListTree}
              title={t("mapping.hint.dvd_order.title")}
            >
              {t("mapping.hint.dvd_order.body")}
            </Notice>
          ) : (
            <Notice
              key={h.code}
              tone="caution"
              icon={ListTree}
              title={t("mapping.hint.unknown", { code: h.code })}
            />
          ),
        )}

        <section
          aria-label={t("mapping.summary.label")}
          className="flex flex-wrap items-center justify-between gap-x-8 gap-y-1 border-y py-2"
        >
          <ul className="flex flex-wrap gap-x-6 gap-y-1">
            <li>
              <StatusLabel tone="done" className="text-base">
                {t("mapping.summary.toImport", { count: summary.toImport })}
              </StatusLabel>
            </li>
            <li>
              <StatusLabel
                tone={summary.attention > 0 ? "caution" : "waiting"}
                icon={TriangleAlert}
                className="text-base"
              >
                {t("mapping.summary.attention", { count: summary.attention })}
              </StatusLabel>
            </li>
            <li>
              <StatusLabel tone="waiting" icon={Ban} className="text-base">
                {t("mapping.summary.skipped", { count: summary.skipped })}
              </StatusLabel>
            </li>
          </ul>
          <label className="inline-flex min-h-11 items-center gap-3">
            <input
              type="checkbox"
              name="only-attention"
              className="size-5 shrink-0 accent-primary"
              checked={onlyAttention}
              onChange={(e) => setOnlyAttention(e.target.checked)}
            />
            {t("mapping.filter")}
          </label>
        </section>

        <div className="grid gap-4">
          {show && (
            <BulkBar
              seasons={seasonsOf(proposal.episodes ?? [])}
              count={selectedIds.length}
              status={mapping.state.bulk}
              onShift={(shift) => void mapping.bulk({ rowIds: selectedIds, shift })}
              onSeason={(seasonNumber) => void mapping.bulk({ rowIds: selectedIds, seasonNumber })}
              onClear={() => table.resetRowSelection()}
              onRetry={mapping.retryBulk}
              onDiscard={mapping.discardBulk}
            />
          )}

          {rows.length === 0 ? (
            <p className="rounded-xl border border-dashed border-input px-5 py-6 text-ink-2">
              {t("mapping.filter.empty")}
            </p>
          ) : wide ? (
            // The rows are laid out with CSS grid, and an element whose `display` is not the
            // table's own can lose its table role in a browser. The roles are written out, so
            // the files stay a table of rows and cells for a screen reader whatever the CSS.
            <table
              // biome-ignore lint/a11y/noRedundantRoles: kept when `display` is not `table`
              role="table"
              aria-label={t("mapping.table.label")}
              className="block overflow-hidden rounded-xl border bg-card shadow-sm"
            >
              {/* biome-ignore lint/a11y/noRedundantRoles: kept when `display` is not `table` */}
              <thead role="rowgroup" className="block border-b bg-muted">
                <tr
                  // biome-ignore lint/a11y/noRedundantRoles: kept when `display` is not `table`
                  role="row"
                  className={cn("grid items-center gap-x-3 px-4", layout.columns, layout.head)}
                >
                  {table.getVisibleLeafColumns().map((col) => (
                    <th
                      key={col.id}
                      // biome-ignore lint/a11y/noRedundantRoles: kept when `display` is not `table`
                      role="columnheader"
                      scope="col"
                      className={cn(
                        "text-left text-sm font-medium text-ink-2",
                        col.id === "file" || col.id === "notes"
                          ? "sr-only"
                          : cn("py-1", place[col.id]),
                      )}
                    >
                      {col.id === "select" ? (
                        <SelectAll table={table} />
                      ) : (
                        <ColumnTitle id={col.id} />
                      )}
                    </th>
                  ))}
                </tr>
              </thead>
              {/* biome-ignore lint/a11y/noRedundantRoles: kept when `display` is not `table` */}
              <tbody role="rowgroup" className="block divide-y">
                {rows.map((row) => (
                  <tr
                    key={row.id}
                    // biome-ignore lint/a11y/noRedundantRoles: kept when `display` is not `table`
                    role="row"
                    data-selected={row.getIsSelected() || undefined}
                    className={cn(rowLook, layout.columns, layout.wide)}
                  >
                    {row.getVisibleCells().map((cell) => {
                      const Cell = cells[cell.column.id] ?? Nothing;
                      return (
                        <td
                          key={cell.id}
                          // biome-ignore lint/a11y/noRedundantRoles: kept when `display` is not `table`
                          role="cell"
                          className={cn("p-0", place[cell.column.id])}
                        >
                          <Cell
                            item={row.original}
                            selected={row.getIsSelected()}
                            toggle={row.getToggleSelectedHandler()}
                          />
                        </td>
                      );
                    })}
                  </tr>
                ))}
              </tbody>
            </table>
          ) : (
            <div className="overflow-hidden rounded-xl border bg-card shadow-sm">
              {show && (
                <div className="border-b bg-muted px-4 text-sm font-medium text-ink-2">
                  <SelectAll table={table} named />
                </div>
              )}
              <ul aria-label={t("mapping.table.label")} className="divide-y">
                {rows.map((row) => (
                  <li
                    key={row.id}
                    data-selected={row.getIsSelected() || undefined}
                    className={cn(rowLook, layout.narrow)}
                  >
                    {row.getVisibleCells().map((cell) => {
                      const id = cell.column.id;
                      const Cell = cells[id] ?? Nothing;
                      const captioned =
                        id === "season" ||
                        id === "episodes" ||
                        id === "quality" ||
                        id === "languages";
                      return (
                        <div key={cell.id} className={cn(place[id], id === "select" && "-mt-2.5")}>
                          {captioned && (
                            <p className="mb-1 text-sm text-muted-foreground">
                              <ColumnTitle id={id} />
                            </p>
                          )}
                          <Cell
                            item={row.original}
                            selected={row.getIsSelected()}
                            toggle={row.getToggleSelectedHandler()}
                          />
                        </div>
                      );
                    })}
                  </li>
                ))}
              </ul>
            </div>
          )}
        </div>

        <div className="flex flex-col gap-x-5 gap-y-3 sm:flex-row sm:items-center">
          {canContinue ? (
            <Button asChild size="lg">
              <Link to="/jobs/$id/confirm" params={{ id: jobId }}>
                {t("mapping.continue")}
              </Link>
            </Button>
          ) : (
            <Button size="lg" disabled>
              {t("mapping.continue")}
            </Button>
          )}
          <p aria-live="polite" className="max-w-[52ch] text-sm text-ink-2">
            {summary.toImport + summary.attention === 0
              ? t("mapping.continue.none")
              : checking || failed
                ? t("mapping.continue.busy")
                : summary.attention > 0
                  ? t("mapping.continue.attention")
                  : ""}
          </p>
        </div>
      </div>
    </MappingProvider>
  );
}

// ---- Columns ---------------------------------------------------------------------------

type CellProps = { item: Item; selected: boolean; toggle: (e: unknown) => void };

function ColumnTitle({ id }: { id: string }) {
  const { t } = useT();
  const key = (
    {
      file: "mapping.col.file",
      season: "mapping.col.season",
      episodes: "mapping.col.episodes",
      quality: "mapping.col.quality",
      languages: "mapping.col.languages",
      skip: "mapping.col.skip",
      notes: "mapping.col.notes",
    } as const
  )[id as "file"];
  return <>{key ? t(key) : null}</>;
}

const Nothing = () => null;

/** The cell of each column, by its id. Defined once: a component made on each render would be
 * mounted again each time, and a control being used would lose the focus. */
const cells: Record<string, (props: CellProps) => React.ReactNode> = {
  select: SelectCell,
  file: ({ item }) => <FileCell item={item} />,
  season: ({ item }) => <SeasonCell row={item.row} />,
  episodes: ({ item }) => <EpisodesCell row={item.row} />,
  quality: ({ item }) => <QualityCell row={item.row} />,
  languages: ({ item }) => <LanguagesCell row={item.row} />,
  skip: ({ item }) => <SkipCell row={item.row} />,
  notes: ({ item }) => <NotesCell item={item} />,
};

function SelectCell({ item, selected, toggle }: CellProps) {
  const { t } = useT();
  return (
    <label className="-ml-3 inline-flex min-h-11 min-w-11 items-center justify-center">
      <input
        type="checkbox"
        className="size-5 accent-primary"
        aria-label={t("mapping.select.row", { name: item.row.name })}
        checked={selected}
        onChange={toggle}
      />
    </label>
  );
}

function SelectAll({
  table,
  named = false,
}: {
  table: ReturnType<typeof useReactTable<Item>>;
  /** Writes the name next to the box, where no column header stands above it. */
  named?: boolean;
}) {
  const { t } = useT();
  const ref = useRef<HTMLInputElement>(null);
  const all = table.getIsAllRowsSelected();
  const some = table.getIsSomeRowsSelected();
  useEffect(() => {
    if (ref.current) ref.current.indeterminate = some && !all;
  }, [some, all]);
  return (
    <label className="-ml-3 inline-flex min-h-11 min-w-11 items-center justify-center gap-3 pl-3">
      <input
        ref={ref}
        type="checkbox"
        className="size-5 accent-primary"
        aria-label={t("mapping.select.all")}
        checked={all}
        onChange={(e) => table.toggleAllRowsSelected(e.target.checked)}
      />
      {named && <span aria-hidden="true">{t("mapping.select.all")}</span>}
    </label>
  );
}

// ---- Bulk edits ------------------------------------------------------------------------

function BulkBar({
  seasons,
  count,
  status,
  onShift,
  onSeason,
  onClear,
  onRetry,
  onDiscard,
}: {
  seasons: number[];
  count: number;
  status: { status: "checking" | "failed" } | null;
  onShift: (shift: number) => void;
  onSeason: (season: number) => void;
  onClear: () => void;
  onRetry: () => void;
  onDiscard: () => void;
}) {
  const { t } = useT();
  const [shift, setShift] = useState("1");
  const [season, setSeason] = useState("");
  const shiftValue = Number(shift);
  const shiftOk = shift.trim() !== "" && Number.isInteger(shiftValue) && shiftValue !== 0;
  const idle = count > 0 && status === null;
  const control = "h-10 pointer-coarse:h-11";
  return (
    <section aria-label={t("mapping.bulk.label")} className="grid gap-3">
      {/* One line for the selection; while the app checks a bulk change it says so in the same
          place, so the rows below do not move. */}
      {status?.status === "checking" ? (
        <StatusLabel tone="waiting" role="status" className="text-base">
          {t("mapping.bulk.checking")}
        </StatusLabel>
      ) : (
        <p
          aria-live="polite"
          className={cn("max-w-[65ch]", count > 0 ? "font-medium" : "text-ink-2")}
        >
          {count > 0 ? t("mapping.bulk.selected", { count }) : t("mapping.bulk.none")}
        </p>
      )}
      <div className="flex flex-wrap items-end gap-x-6 gap-y-3">
        <form
          className="flex items-end gap-2"
          onSubmit={(e) => {
            e.preventDefault();
            if (idle && shiftOk) onShift(shiftValue);
          }}
        >
          <label className="grid gap-1 text-sm text-muted-foreground">
            {t("mapping.bulk.shift")}
            <input
              type="number"
              name="shift"
              inputMode="numeric"
              autoComplete="off"
              step={1}
              value={shift}
              onChange={(e) => setShift(e.target.value)}
              className={cn(inputClass, "w-24 font-mono tabular-nums")}
            />
          </label>
          <Button type="submit" variant="outline" className={control} disabled={!idle || !shiftOk}>
            {t("mapping.bulk.shift.apply")}
          </Button>
        </form>
        <form
          className="flex items-end gap-2"
          onSubmit={(e) => {
            e.preventDefault();
            if (idle && season !== "") onSeason(Number(season));
          }}
        >
          <label className="grid gap-1 text-sm text-muted-foreground">
            {t("mapping.bulk.season")}
            <select
              name="season"
              autoComplete="off"
              value={season}
              onChange={(e) => setSeason(e.target.value)}
              className={cn(selectClass, "w-36")}
            >
              <option value="" />
              {seasons.map((s) => (
                <option key={s} value={s}>
                  {t("mapping.season.option", { season: s })}
                </option>
              ))}
            </select>
          </label>
          <Button
            type="submit"
            variant="outline"
            className={control}
            disabled={!idle || season === ""}
          >
            {t("mapping.bulk.season.apply")}
          </Button>
        </form>
        <Button
          type="button"
          variant="ghost"
          className={control}
          disabled={count === 0}
          onClick={onClear}
        >
          {t("mapping.bulk.clear")}
        </Button>
      </div>
      {status?.status === "failed" && (
        <div role="alert" className="flex flex-wrap items-center gap-x-4 gap-y-2">
          <StatusLabel tone="error">{t("mapping.bulk.failed")}</StatusLabel>
          <div className="flex flex-wrap gap-2">
            <Button type="button" variant="outline" size="sm" onClick={onRetry}>
              {t("mapping.retry")}
            </Button>
            <Button type="button" variant="ghost" size="sm" onClick={onDiscard}>
              {t("mapping.discard")}
            </Button>
          </div>
        </div>
      )}
    </section>
  );
}
