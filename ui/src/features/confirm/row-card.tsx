import { LoaderCircle, TriangleAlert, X } from "lucide-react";
import { useId } from "react";
import type { Proposal, ProposalRow, RowEdit } from "@/api/proposal";
import type { JobFile } from "@/api/types";
import { ProbeDetails } from "@/components/jobs/probe-details";
import { SelectField } from "@/components/patterns/select-field";
import { Button } from "@/components/ui/button";
import { useT } from "@/i18n";
import { cn } from "@/lib/utils";
import { hasIssues, warningText } from "./notices";

const appName: Record<Proposal["app"], string> = {
  radarr: "Radarr",
  sonarr: "Sonarr",
  "": "Radarr or Sonarr",
};

type Props = {
  row: ProposalRow;
  proposal: Proposal;
  /** The job file the row is made of, for what the probe found. */
  file: JobFile | undefined;
  title: string;
  /** True while the app re-checks an edit of this row. */
  checking: boolean;
  /** The user has agreed to import the row despite its rejections and warnings. */
  overridden: boolean;
  /** Pressing "Import" found this row not agreed to yet. */
  missing: boolean;
  onOverride: (value: boolean) => void;
  onEdit: (edit: RowEdit) => void;
};

/**
 * One file next to the film or episode it goes to: what the app made of it (quality,
 * languages, custom formats, score), what is against importing it, and the corrections the
 * user can make. Every correction is re-checked by the app; the row says so while it waits.
 */
export function RowCard({
  row,
  proposal,
  file,
  title,
  checking,
  overridden,
  missing,
  onOverride,
  onEdit,
}: Props) {
  const { t, formatDuration } = useT();
  const id = useId();
  const app = appName[proposal.app];
  const issues = hasIssues(row);
  const offered = row.quality !== null;
  const locked = checking || row.skip;
  // The server skips a file the app did not offer, or one for an episode that has a file:
  // that is not the user's to undo here.
  const forced = row.warnings.filter((w) => w.code === "not_offered" || w.code === "existing_file");

  const chosen = new Set(row.languages.map((l) => l.id));
  const addable = proposal.languages.filter((l) => !chosen.has(l.id));
  const ids = row.languages.map((l) => l.id);

  return (
    <li
      aria-busy={checking}
      className={cn("grid gap-5 py-6", row.skip && "text-muted-foreground")}
      data-row={row.id}
    >
      <h3 translate="no" className="[overflow-wrap:anywhere]">
        {row.name}
      </h3>

      <div className="grid gap-6 sm:grid-cols-2">
        <section aria-labelledby={`${id}-to`} className="grid content-start gap-1">
          <h4 id={`${id}-to`} className="text-sm font-normal text-muted-foreground">
            {t("confirm.row.goesTo", { app })}
          </h4>
          <p translate="no" className="font-medium [overflow-wrap:anywhere]">
            {title}
          </p>
          {row.episodes && row.episodes.length > 0 && (
            <ul className="grid gap-0.5 text-sm">
              {row.episodes.map((e) => (
                <li key={e.id} translate="no" className="[overflow-wrap:anywhere]">
                  {t("confirm.row.episode", {
                    season: String(row.seasonNumber ?? 0),
                    number: String(e.number),
                    title: e.title,
                  })}
                </li>
              ))}
            </ul>
          )}
        </section>

        <section aria-labelledby={`${id}-file`} className="grid content-start gap-1">
          <h4 id={`${id}-file`} className="text-sm font-normal text-muted-foreground">
            {t("confirm.row.file")}
          </h4>
          <p className="figure">
            {row.durationMs > 0 ? formatDuration(row.durationMs) : t("confirm.row.noDuration")}
          </p>
          {file?.probe && <ProbeDetails name={row.name} probe={file.probe} open={false} />}
        </section>
      </div>

      {offered ? (
        <div className="grid gap-4">
          <div className="grid gap-4 sm:grid-cols-2">
            <SelectField
              label={t("confirm.quality")}
              value={String(row.quality?.id ?? "")}
              disabled={locked}
              onChange={(e) => onEdit({ qualityId: Number(e.target.value) })}
            >
              {/* The app's own quality may be missing from its list of choices. */}
              {row.quality && !proposal.qualities.some((q) => q.id === row.quality?.id) && (
                <option value={String(row.quality.id)}>{row.quality.name}</option>
              )}
              {proposal.qualities.map((q) => (
                <option key={q.id} value={String(q.id)}>
                  {q.name}
                </option>
              ))}
            </SelectField>

            <div className="grid content-start gap-1 text-sm text-muted-foreground">
              <span>{t("confirm.languages")}</span>
              <ul className="flex min-h-10 flex-wrap items-center gap-2 text-base text-foreground">
                {row.languages.length === 0 && (
                  <li className="text-muted-foreground">{t("confirm.languages.none")}</li>
                )}
                {row.languages.map((l) => (
                  <li key={l.id} className="inline-flex items-center rounded-md border bg-card">
                    <span className="px-2">{l.name}</span>
                    <button
                      type="button"
                      disabled={locked}
                      aria-label={t("confirm.languages.remove", { name: l.name })}
                      onClick={() => onEdit({ languageIds: ids.filter((i) => i !== l.id) })}
                      className="inline-flex size-9 items-center justify-center rounded-r-md hover:bg-accent disabled:opacity-50 pointer-coarse:size-11"
                    >
                      <X aria-hidden="true" className="size-4" />
                    </button>
                  </li>
                ))}
              </ul>
              {addable.length > 0 && (
                <SelectField
                  label={t("confirm.languages.add")}
                  value=""
                  disabled={locked}
                  onChange={(e) => {
                    if (e.target.value !== "")
                      onEdit({ languageIds: [...ids, Number(e.target.value)] });
                  }}
                >
                  <option value="">{t("confirm.languages.choose")}</option>
                  {addable.map((l) => (
                    <option key={l.id} value={String(l.id)}>
                      {l.name}
                    </option>
                  ))}
                </SelectField>
              )}
            </div>
          </div>

          <dl className="grid gap-x-6 gap-y-1 text-sm sm:grid-cols-[auto_minmax(0,1fr)]">
            <dt className="text-muted-foreground">{t("confirm.formats")}</dt>
            <dd translate="no" className="[overflow-wrap:anywhere]">
              {row.customFormats.length > 0
                ? row.customFormats.join(", ")
                : t("confirm.formats.none")}
            </dd>
            <dt className="text-muted-foreground">{t("confirm.score")}</dt>
            <dd className="figure">{row.customFormatScore}</dd>
          </dl>
        </div>
      ) : null}

      {checking && (
        <p role="status" className="flex items-center gap-2 text-sm">
          <LoaderCircle aria-hidden="true" className="size-4 animate-spin" />
          {t("confirm.checking", { app })}
        </p>
      )}

      {issues && !row.skip && (
        <div
          className={cn(
            "rounded-lg border border-caution-edge bg-caution p-4 text-foreground",
            missing && "ring-2 ring-destructive",
          )}
        >
          <h4 className="flex items-center gap-2 text-base">
            <TriangleAlert aria-hidden="true" className="size-5 shrink-0 text-caution-foreground" />
            {t("confirm.issues.title")}
          </h4>
          <ul className="mt-2 grid list-disc gap-1.5 pl-5 marker:text-caution-foreground">
            {row.rejections.map((r) => (
              <li key={`r-${r.reason}`} className="max-w-[65ch]">
                {t("confirm.rejection", { app })}{" "}
                <span translate="no" lang="en" className="[overflow-wrap:anywhere]">
                  {r.reason}
                </span>
              </li>
            ))}
            {row.warnings.map((w) => (
              <li key={`w-${w.code}`} className="max-w-[65ch]">
                {t(warningText(w.code).key, warningText(w.code).params)}
                {w.code === "language" && w.score !== undefined && (
                  <span className="mt-1 block text-sm text-ink-2">
                    {t("confirm.warning.language.facts", {
                      formats: w.customFormats?.length
                        ? w.customFormats.join(", ")
                        : t("confirm.formats.none"),
                      score: w.score,
                      min: w.minFormatScore ?? 0,
                    })}
                  </span>
                )}
              </li>
            ))}
          </ul>
          <label className="mt-3 flex min-h-11 cursor-pointer items-center gap-3 font-medium">
            <input
              type="checkbox"
              data-override={row.id}
              checked={overridden}
              aria-invalid={missing && !overridden ? true : undefined}
              onChange={(e) => onOverride(e.target.checked)}
              className="size-5 shrink-0 cursor-pointer accent-primary"
            />
            {t("confirm.override")}
          </label>
        </div>
      )}

      {row.skip && (
        <div role="note" className="grid gap-1 text-sm">
          <p>{t("confirm.skipped")}</p>
          {forced.map((w) => (
            <p key={w.code}>{t(warningText(w.code).key)}</p>
          ))}
        </div>
      )}

      {forced.length === 0 && (
        <div>
          <Button
            type="button"
            variant="ghost"
            disabled={checking}
            onClick={() => onEdit({ skip: !row.skip })}
          >
            {row.skip ? t("confirm.skip.undo") : t("confirm.skip")}
          </Button>
        </div>
      )}
    </li>
  );
}
