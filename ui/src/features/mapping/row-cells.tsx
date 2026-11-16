// What one row of the editor shows and lets the user change. The same cells are laid out as a
// table row or as a card, so a change is made in one place.

import { Ban, CircleAlert, TriangleAlert } from "lucide-react";
import { createContext, type ReactNode, useContext } from "react";
import { ApiError } from "@/api/client";
import type { Notice, Proposal, ProposalRow, RowEdit } from "@/api/proposal";
import { selectClass } from "@/components/patterns/field";
import { StatusLabel } from "@/components/patterns/status-label";
import { Button } from "@/components/ui/button";
import { useT } from "@/i18n";
import { cn } from "@/lib/utils";
import { type BulkPending, nextEpisode, type Pending, seasonsOf } from "./state";

export type Item = { row: ProposalRow; status: Pending | BulkPending | null };

export type MappingContext = {
  proposal: Proposal;
  appName: string;
  edit: (rowId: string, change: RowEdit) => void;
  retry: (item: Item) => void;
  discard: (item: Item) => void;
};

const Ctx = createContext<MappingContext | null>(null);
export const MappingProvider = Ctx.Provider;

function useMapping(): MappingContext {
  const ctx = useContext(Ctx);
  if (!ctx) throw new Error("the mapping cells need a <MappingProvider>");
  return ctx;
}

/** A file the app did not list: nothing can change but skipping it. */
export const isNotOffered = (row: ProposalRow) =>
  row.warnings.some((w) => w.code === "not_offered");

/**
 * The head of a row, set like a file label: the name over its figures (how long the file is,
 * against how long the episode should be), and at the far edge what the app makes of it, in
 * words. The state has a place of its own, so "Checking…" takes it without moving anything.
 */
export function FileCell({ item }: { item: Item }) {
  const { t, formatDuration } = useT();
  const { row } = item;
  const expected = (row.episodes ?? []).reduce((sum, e) => sum + e.runtimeMs, 0);
  const actual = formatDuration(row.durationMs);
  return (
    <div className="flex min-w-0 flex-col gap-x-6 gap-y-2 sm:flex-row sm:items-start sm:justify-between">
      <div className="min-w-0">
        <p
          translate="no"
          className={cn(
            "font-medium [overflow-wrap:anywhere]",
            row.skip && "text-muted-foreground",
          )}
        >
          {row.name}
        </p>
        <p className="figure text-sm whitespace-normal text-muted-foreground">
          {expected > 0
            ? t("mapping.duration", { actual, expected: formatDuration(expected) })
            : t("mapping.duration.only", { actual })}
        </p>
      </div>
      <RowState item={item} />
    </div>
  );
}

/** What stands for the whole row: one of six states, each with the icon of its meaning. */
function RowState({ item }: { item: Item }) {
  const { t } = useT();
  const { row, status } = item;
  const place = "shrink-0 sm:w-52 sm:pt-0.5";
  if (status?.status === "checking") {
    return (
      <StatusLabel tone="waiting" role="status" className={place}>
        {t("mapping.checking")}
      </StatusLabel>
    );
  }
  if (status?.status === "failed") {
    return (
      <StatusLabel tone="error" className={place}>
        {t("mapping.state.failed")}
      </StatusLabel>
    );
  }
  if (row.skip) {
    return (
      <StatusLabel tone="waiting" icon={Ban} className={place}>
        {t("mapping.state.skipped")}
      </StatusLabel>
    );
  }
  if (row.rejections.length > 0) {
    return (
      <StatusLabel tone="error" className={place}>
        {t("mapping.state.refused")}
      </StatusLabel>
    );
  }
  if (row.warnings.length > 0) {
    return (
      <StatusLabel tone="caution" className={place}>
        {t("mapping.state.attention")}
      </StatusLabel>
    );
  }
  return (
    <StatusLabel tone="done" className={place}>
      {t("mapping.ok")}
    </StatusLabel>
  );
}

export function SeasonCell({ row }: { row: ProposalRow }) {
  const { t } = useT();
  const { proposal, edit } = useMapping();
  const seasons = seasonsOf(proposal.episodes ?? []);
  return (
    <select
      name={`season-${row.id}`}
      autoComplete="off"
      aria-label={t("mapping.season.for", { name: row.name })}
      className={selectClass}
      value={row.seasonNumber === undefined ? "" : String(row.seasonNumber)}
      disabled={isNotOffered(row)}
      onChange={(e) => edit(row.id, { seasonNumber: Number(e.target.value) })}
    >
      {row.seasonNumber === undefined && <option value="">{t("mapping.season.unknown")}</option>}
      {seasons.map((s) => (
        <option key={s} value={s}>
          {t("mapping.season.option", { season: s })}
        </option>
      ))}
    </select>
  );
}

export function EpisodesCell({ row }: { row: ProposalRow }) {
  const { t } = useT();
  const { proposal, edit } = useMapping();
  const first = row.episodes?.[0];
  const second = row.episodes?.[1];
  const pool = (proposal.episodes ?? []).filter((e) => e.seasonNumber === row.seasonNumber);
  const label = (e: (typeof pool)[number]) =>
    t(e.hasFile ? "mapping.episode.hasFile" : "mapping.episode.option", {
      number: e.number,
      title: e.title,
    });
  const locked = isNotOffered(row) || row.seasonNumber === undefined;

  function change(firstId: string, secondId: string) {
    const ids = [firstId, secondId].filter((v) => v !== "").map(Number);
    edit(row.id, { episodeIds: ids });
  }

  return (
    <div className="grid gap-2 sm:grid-cols-2">
      <select
        name={`episode-${row.id}`}
        autoComplete="off"
        aria-label={t("mapping.episode.first", { name: row.name })}
        className={selectClass}
        value={first ? String(first.id) : ""}
        disabled={locked}
        onChange={(e) => {
          // The second episode stays unless it is the one just chosen first.
          const keep = second && String(second.id) !== e.target.value ? String(second.id) : "";
          change(e.target.value, keep);
        }}
      >
        {!first && <option value="">{t("mapping.episode.choose")}</option>}
        {pool.map((e) => (
          <option key={e.id} value={e.id}>
            {label(e)}
          </option>
        ))}
      </select>
      <select
        name={`second-episode-${row.id}`}
        autoComplete="off"
        aria-label={t("mapping.episode.second", { name: row.name })}
        className={selectClass}
        value={second ? String(second.id) : ""}
        disabled={locked || !first}
        onChange={(e) => change(first ? String(first.id) : "", e.target.value)}
      >
        <option value="">{t("mapping.episode.none")}</option>
        {pool
          .filter((e) => e.id !== first?.id)
          .map((e) => (
            <option key={e.id} value={e.id}>
              {label(e)}
            </option>
          ))}
      </select>
    </div>
  );
}

export function QualityCell({ row }: { row: ProposalRow }) {
  const { t } = useT();
  const { proposal, edit } = useMapping();
  const known = proposal.qualities.some((q) => q.id === row.quality?.id);
  return (
    <select
      name={`quality-${row.id}`}
      autoComplete="off"
      aria-label={t("mapping.quality.for", { name: row.name })}
      className={selectClass}
      value={row.quality ? String(row.quality.id) : ""}
      disabled={isNotOffered(row)}
      onChange={(e) => edit(row.id, { qualityId: Number(e.target.value) })}
    >
      {!row.quality && (
        <option value="" disabled>
          {t("mapping.quality.none")}
        </option>
      )}
      {row.quality && !known && <option value={row.quality.id}>{row.quality.name}</option>}
      {proposal.qualities.map((q) => (
        <option key={q.id} value={q.id}>
          {q.name}
        </option>
      ))}
    </select>
  );
}

export function LanguagesCell({ row }: { row: ProposalRow }) {
  const { t } = useT();
  return row.languages.length > 0 ? (
    <span translate="no" className="[overflow-wrap:anywhere]">
      {row.languages.map((l) => l.name).join(", ")}
    </span>
  ) : (
    <span className="text-muted-foreground">{t("mapping.languages.none")}</span>
  );
}

export function SkipCell({ row }: { row: ProposalRow }) {
  const { t } = useT();
  const { edit } = useMapping();
  return (
    <label className="inline-flex min-h-11 items-center gap-2">
      <input
        type="checkbox"
        name={`skip-${row.id}`}
        className="size-5 accent-primary"
        aria-label={t("mapping.skip.for", { name: row.name })}
        checked={row.skip}
        onChange={(e) => edit(row.id, { skip: e.target.checked })}
      />
      <span aria-hidden="true">{t("mapping.skip.label")}</span>
    </label>
  );
}

/**
 * What is against importing the row, in plain words, with the one-click fix a warning can
 * offer. Empty for a row with nothing against it: its state already says so. While the app
 * checks an edit the last verdict stays where it is, greyed, so the row keeps its height;
 * the new verdict replaces it when it arrives.
 */
export function NotesCell({ item }: { item: Item }) {
  const { t, formatNumber } = useT();
  const { proposal, appName, edit, retry, discard } = useMapping();
  const { row, status } = item;
  const checking = status?.status === "checking";

  if (status?.status === "failed") {
    const wrongState = status.error instanceof ApiError && status.error.status === 409;
    return (
      <div role="alert" className="grid gap-2 text-sm">
        <p className="max-w-[65ch]">
          {wrongState ? t("mapping.failed.wrongState") : t("mapping.failed")}
        </p>
        <div className="flex flex-wrap gap-2">
          <Button type="button" variant="outline" size="sm" onClick={() => retry(item)}>
            {t("mapping.retry")}
          </Button>
          <Button type="button" variant="ghost" size="sm" onClick={() => discard(item)}>
            {t("mapping.discard")}
          </Button>
        </div>
      </div>
    );
  }

  const single = row.episodes?.length === 1 ? row.episodes[0] : undefined;
  const next =
    single && row.seasonNumber !== undefined
      ? nextEpisode(row.seasonNumber, single, proposal.episodes ?? [])
      : undefined;

  const notes: ReactNode[] = [];
  for (const r of row.rejections) {
    notes.push(
      <li key={`r-${r.reason}`} className="flex items-start gap-2">
        <CircleAlert aria-hidden="true" className="mt-0.5 size-4 shrink-0 text-destructive" />
        <span className="max-w-[65ch]">
          {t(r.permanent ? "mapping.rejection.permanent" : "mapping.rejection.temporary", {
            app: appName,
            reason: r.reason,
          })}
        </span>
      </li>,
    );
  }
  for (const w of row.warnings) {
    notes.push(
      <li key={`w-${w.code}`} className="flex items-start gap-2">
        <TriangleAlert aria-hidden="true" className="mt-0.5 size-4 shrink-0 text-caution-edge" />
        <span className="grid gap-2">
          <span className="max-w-[65ch]">{warningText(w, row, appName, t, formatNumber)}</span>
          {w.code === "double_episode" && single && next && (
            <Button
              type="button"
              variant="outline"
              size="sm"
              className="justify-self-start"
              disabled={checking}
              onClick={() => edit(row.id, { episodeIds: [single.id, next.id] })}
            >
              {t("mapping.warning.double_episode.action", {
                first: single.number,
                second: next.number,
              })}
            </Button>
          )}
        </span>
      </li>,
    );
  }
  if (notes.length === 0) return null;
  return (
    <ul
      aria-busy={checking}
      className={cn("grid gap-2 text-sm", checking && "text-muted-foreground")}
    >
      {notes}
    </ul>
  );
}

function warningText(
  warning: Notice,
  row: ProposalRow,
  app: string,
  t: ReturnType<typeof useT>["t"],
  formatNumber: ReturnType<typeof useT>["formatNumber"],
): string {
  const { code } = warning;
  switch (code) {
    case "double_episode":
    case "runtime_mismatch":
    case "duplicate_episode":
    case "duplicate_film":
      return t(`mapping.warning.${code}`);
    case "existing_file":
    case "not_offered":
      return t(`mapping.warning.${code}`, { app });
    case "language": {
      // The facts the warning carries (#39); the row's own score stands in if one is missing.
      const facts = t("mapping.warning.language", {
        languages: row.languages.map((l) => l.name).join(", ") || "-",
        score: formatNumber(warning.score ?? row.customFormatScore),
        min: formatNumber(warning.minFormatScore ?? 0),
      });
      const formats = warning.customFormats ?? row.customFormats;
      return formats.length > 0
        ? `${facts} ${t("mapping.warning.language.formats", { formats: formats.join(", ") })}`
        : facts;
    }
    default:
      return t("mapping.warning.unknown", { code });
  }
}
