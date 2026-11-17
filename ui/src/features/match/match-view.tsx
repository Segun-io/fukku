import { Link } from "@tanstack/react-router";
import { Search } from "lucide-react";
import { useId, useState } from "react";
import { useLookup } from "@/api/match";
import type { AppName, Apps, JobDetail } from "@/api/types";
import { ErrorMessage } from "@/components/error-message";
import { inputClass } from "@/components/patterns/field";
import { StatusLabel } from "@/components/patterns/status-label";
import { EmptyState, LoadingState } from "@/components/shell/states";
import { Button } from "@/components/ui/button";
import { useT } from "@/i18n";
import { cn } from "@/lib/utils";
import { guessKind, type Hit, toHit } from "./hits";
import { ExistsNotice, MatchOptions, nameOf } from "./match-options";

type Props = { job: JobDetail; apps: Apps };

/** Which kind of title: shown as two choices only when both apps are configured. */
function KindChoice({
  available,
  value,
  onChange,
}: {
  available: AppName[];
  value: AppName;
  onChange: (kind: AppName) => void;
}) {
  const { t } = useT();
  const name = useId();
  if (available.length < 2) return null;
  return (
    <fieldset className="grid gap-1">
      <legend className="font-medium">{t("match.kind.legend")}</legend>
      <div className="flex flex-wrap gap-x-8">
        {available.map((kind) => (
          <label key={kind} className="flex min-h-11 items-center gap-2">
            <input
              type="radio"
              name={name}
              className="size-5 accent-primary"
              checked={value === kind}
              onChange={() => onChange(kind)}
            />
            {t(kind === "radarr" ? "match.kind.movie" : "match.kind.series")}
          </label>
        ))}
      </div>
      <p className="max-w-[65ch] text-sm text-muted-foreground">{t("match.kind.hint")}</p>
    </fieldset>
  );
}

function HitRow({
  hit,
  checked,
  onSelect,
  group,
}: {
  hit: Hit;
  checked: boolean;
  onSelect: () => void;
  group: string;
}) {
  const { t } = useT();
  return (
    <li>
      <label
        className={cn(
          "flex min-h-11 cursor-pointer items-start gap-3 px-4 py-4 hover:bg-accent/60",
          "has-[:focus-visible]:outline-2 has-[:focus-visible]:-outline-offset-2 has-[:focus-visible]:outline-ring",
          checked && "bg-primary-soft hover:bg-primary-soft",
        )}
      >
        <input
          type="radio"
          name={group}
          className="mt-0.5 size-5 shrink-0 accent-primary outline-none"
          checked={checked}
          onChange={onSelect}
        />
        <span className="grid min-w-0 gap-1.5">
          <span translate="no" className="font-medium [overflow-wrap:anywhere]">
            {t("match.hit.name", { title: hit.title, year: String(hit.year) })}
          </span>
          {(hit.inLibrary || hit.hasFile || hit.seasons.length > 0) && (
            <span className="flex flex-wrap gap-x-4 gap-y-1">
              {hit.inLibrary && (
                <StatusLabel tone="done">
                  {t("match.hit.inLibrary", { app: nameOf(hit.app) })}
                </StatusLabel>
              )}
              {hit.hasFile && <StatusLabel tone="caution">{t("match.hit.hasFile")}</StatusLabel>}
              {hit.seasons.length > 0 && (
                <span className="text-sm text-ink-2">
                  {t("match.hit.seasons", { count: hit.seasons.length })}
                </span>
              )}
            </span>
          )}
          <span className="line-clamp-3 max-w-[65ch] text-sm text-ink-2">
            {hit.overview || t("match.hit.noOverview")}
          </span>
        </span>
      </label>
    </li>
  );
}

/** The match step: kind, search, results, options of the picked title. */
export function MatchView({ job, apps }: Props) {
  const { t } = useT();
  const available = (["radarr", "sonarr"] as const).filter((a) => apps[a]);
  const [kind, setKind] = useState<AppName>(guessKind(job.files, apps) ?? "radarr");
  const [text, setText] = useState("");
  const [term, setTerm] = useState("");
  const [picked, setPicked] = useState<number | null>(null);
  const group = useId();
  const inputId = useId();
  const hintId = useId();

  const lookup = useLookup(kind === "radarr" ? "movies" : "series", term);
  const hits = (lookup.data ?? []).map((h) => toHit(kind, h));
  const hit = hits.find((h) => h.id === picked) ?? null;

  function search(e: React.FormEvent) {
    e.preventDefault();
    const next = text.trim();
    if (next === "") return;
    setPicked(null);
    setTerm(next);
  }

  function changeKind(next: AppName) {
    setKind(next);
    setPicked(null);
  }

  return (
    <div className="grid gap-8">
      <KindChoice available={available} value={kind} onChange={changeKind} />

      <search>
        <form onSubmit={search} className="grid gap-2">
          <label htmlFor={inputId} className="font-medium">
            {t("match.search.label")}
          </label>
          <div className="flex gap-2">
            <input
              id={inputId}
              name="term"
              type="text"
              className={cn(inputClass, "flex-1")}
              value={text}
              onChange={(e) => setText(e.target.value)}
              aria-describedby={hintId}
              autoComplete="off"
              enterKeyHint="search"
              spellCheck={false}
            />
            <Button type="submit" size="lg">
              <Search aria-hidden="true" />
              {t("match.search.submit")}
            </Button>
          </div>
          <p id={hintId} className="max-w-[65ch] text-sm text-muted-foreground">
            {t("match.search.hint")}
          </p>
        </form>
      </search>

      {term !== "" && lookup.isPending && <LoadingState rows={2} />}
      {lookup.isError && <ErrorMessage error={lookup.error} />}

      {lookup.isSuccess && hits.length === 0 && (
        <EmptyState title={t("match.results.none.title", { term })}>
          {t("match.results.none.body")}
        </EmptyState>
      )}

      {hits.length > 0 && (
        <fieldset className="grid min-w-0 gap-3">
          <legend className="float-left font-display text-lg leading-tight font-bold tracking-[-0.02em]">
            {t("match.results.title")}
          </legend>
          <p role="status" className="text-sm text-muted-foreground">
            {t("match.results.count", { count: hits.length })}
          </p>
          <ul className="clear-both divide-y overflow-hidden rounded-xl border bg-card shadow-sm">
            {hits.map((h) => (
              <HitRow
                key={h.id}
                hit={h}
                group={group}
                checked={h.id === picked}
                onSelect={() => setPicked(h.id)}
              />
            ))}
          </ul>
        </fieldset>
      )}

      {hit?.hasFile && <ExistsNotice key={hit.id} jobId={job.id} tmdbId={hit.id} />}
      {hit && !hit.hasFile && (
        <MatchOptions key={`${hit.app}-${hit.id}`} jobId={job.id} hit={hit} infos={apps} />
      )}

      <p className="border-t pt-4">
        <Link
          to="/jobs/$id"
          params={{ id: job.id }}
          className="inline-flex min-h-11 items-center text-sm underline underline-offset-4"
        >
          {t("match.manual")}
        </Link>
      </p>
    </div>
  );
}
