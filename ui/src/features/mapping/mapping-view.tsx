import { Link } from "@tanstack/react-router";
import {
  type ColumnDef,
  getCoreRowModel,
  getFilteredRowModel,
  type RowSelectionState,
  useReactTable,
} from "@tanstack/react-table";
import { CircleAlert, Clock, ListTree } from "lucide-react";
import { useEffect, useMemo, useRef, useState } from "react";
import type { Proposal } from "@/api/proposal";
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
  selectClass,
} from "./row-cells";
import { needsAttention, rowStatus, seasonsOf, shownRows, summarize } from "./state";
import { useMapping } from "./use-mapping";

const appNames = { radarr: "Radarr", sonarr: "Sonarr", "": "" } as const;

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

  const table = useReactTable({
    data: items,
    columns,
    getRowId: (item) => item.row.id,
    getCoreRowModel: getCoreRowModel(),
    getFilteredRowModel: getFilteredRowModel(),
    enableRowSelection: true,
    onRowSelectionChange: setSelection,
    state: {
      rowSelection: selection,
      columnFilters: onlyAttention ? [{ id: "attention", value: true }] : [],
      columnVisibility: { attention: false, select: show, season: show, episodes: show },
    },
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

  return (
    <MappingProvider value={ctx}>
      <div className="grid gap-5">
        <p className="max-w-[65ch] text-ink-2">{t("mapping.intro", { app: appName })}</p>
        {show && (
          <p className="max-w-[65ch] text-ink-2">{t("mapping.fromNames", { app: appName })}</p>
        )}

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
          className="flex flex-wrap items-center gap-x-6 gap-y-3"
        >
          <ul className="flex flex-wrap gap-x-5 gap-y-1 font-medium">
            <li>{t("mapping.summary.toImport", { count: summary.toImport })}</li>
            <li>{t("mapping.summary.skipped", { count: summary.skipped })}</li>
            <li className={cn(summary.attention > 0 && "text-caution-foreground")}>
              {t("mapping.summary.attention", { count: summary.attention })}
            </li>
          </ul>
          <label className="inline-flex min-h-11 items-center gap-3">
            <input
              type="checkbox"
              className="size-5 accent-primary"
              checked={onlyAttention}
              onChange={(e) => setOnlyAttention(e.target.checked)}
            />
            {t("mapping.filter")}
          </label>
        </section>

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
          <div className="overflow-x-auto rounded-xl border bg-card">
            <table className="w-full min-w-[56rem] border-collapse text-left">
              <caption className="sr-only">{t("mapping.table.label")}</caption>
              <thead>
                <tr className="border-b text-sm text-muted-foreground">
                  {table.getVisibleLeafColumns().map((col) => (
                    <th key={col.id} scope="col" className="px-3 py-2 font-medium">
                      {col.id === "select" ? (
                        <SelectAll table={table} />
                      ) : (
                        <ColumnTitle id={col.id} />
                      )}
                    </th>
                  ))}
                </tr>
              </thead>
              <tbody>
                {rows.map((row) => (
                  <tr key={row.id} className="border-b align-top last:border-b-0">
                    {row.getVisibleCells().map((cell) => {
                      const Cell = cells[cell.column.id] ?? Nothing;
                      return (
                        <td key={cell.id} className="px-3 py-3">
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
          </div>
        ) : (
          <>
            <SelectAll table={table} />
            <ul aria-label={t("mapping.table.label")} className="grid gap-3">
              {rows.map((row) => (
                <li key={row.id} className="grid gap-3 rounded-xl border bg-card p-4">
                  {row.getVisibleCells().map((cell) => {
                    const id = cell.column.id;
                    const Cell = cells[id] ?? Nothing;
                    return (
                      <div key={cell.id} className={cn(id === "select" && "-mb-2")}>
                        {id !== "select" && id !== "file" && id !== "skip" && id !== "notes" && (
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
          </>
        )}

        <div className="flex flex-wrap items-center gap-x-5 gap-y-3">
          {canContinue ? (
            <Button asChild size="lg">
              <Link to="/jobs/$id" params={{ id: jobId }}>
                {t("mapping.continue")}
              </Link>
            </Button>
          ) : (
            <Button size="lg" disabled>
              {t("mapping.continue")}
            </Button>
          )}
          <p className="max-w-[52ch] text-sm text-muted-foreground">
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
  file: ({ item }) => <FileCell row={item.row} />,
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
    <label className="inline-flex min-h-11 min-w-11 items-center justify-center">
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

function SelectAll({ table }: { table: ReturnType<typeof useReactTable<Item>> }) {
  const { t } = useT();
  const ref = useRef<HTMLInputElement>(null);
  const all = table.getIsAllRowsSelected();
  const some = table.getIsSomeRowsSelected();
  useEffect(() => {
    if (ref.current) ref.current.indeterminate = some && !all;
  }, [some, all]);
  return (
    <label className="inline-flex min-h-11 min-w-11 items-center gap-2">
      <input
        ref={ref}
        type="checkbox"
        className="size-5 accent-primary"
        aria-label={t("mapping.select.all")}
        checked={all}
        onChange={(e) => table.toggleAllRowsSelected(e.target.checked)}
      />
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
  return (
    <section
      aria-label={t("mapping.bulk.label")}
      className="grid gap-3 rounded-xl border bg-muted p-4"
    >
      <p aria-live="polite" className="font-medium">
        {count > 0 ? t("mapping.bulk.selected", { count }) : t("mapping.bulk.none")}
      </p>
      <div className="flex flex-wrap items-end gap-x-6 gap-y-3">
        <form
          className="flex flex-wrap items-end gap-2"
          onSubmit={(e) => {
            e.preventDefault();
            if (idle && shiftOk) onShift(shiftValue);
          }}
        >
          <label className="grid gap-1 text-sm text-muted-foreground">
            {t("mapping.bulk.shift")}
            <input
              type="number"
              inputMode="numeric"
              step={1}
              value={shift}
              onChange={(e) => setShift(e.target.value)}
              className={cn(selectClass, "w-24")}
            />
          </label>
          <Button
            type="submit"
            variant="outline"
            className="h-10 pointer-coarse:h-11"
            disabled={!idle || !shiftOk}
          >
            {t("mapping.bulk.shift.apply")}
          </Button>
        </form>
        <form
          className="flex flex-wrap items-end gap-2"
          onSubmit={(e) => {
            e.preventDefault();
            if (idle && season !== "") onSeason(Number(season));
          }}
        >
          <label className="grid gap-1 text-sm text-muted-foreground">
            {t("mapping.bulk.season")}
            <select
              value={season}
              onChange={(e) => setSeason(e.target.value)}
              className={cn(selectClass, "w-40")}
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
            className="h-10 pointer-coarse:h-11"
            disabled={!idle || season === ""}
          >
            {t("mapping.bulk.season.apply")}
          </Button>
        </form>
        <Button type="button" variant="ghost" disabled={count === 0} onClick={onClear}>
          {t("mapping.bulk.clear")}
        </Button>
      </div>
      {status?.status === "checking" && (
        <p role="status" className="inline-flex items-center gap-1.5 text-sm text-muted-foreground">
          <Clock aria-hidden="true" className="size-4" />
          {t("mapping.bulk.checking")}
        </p>
      )}
      {status?.status === "failed" && (
        <div role="alert" className="grid gap-2 text-sm">
          <p className="inline-flex items-center gap-1.5 text-destructive">
            <CircleAlert aria-hidden="true" className="size-4" />
            {t("mapping.bulk.failed")}
          </p>
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
