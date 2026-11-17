import { Ban, TriangleAlert, X } from "lucide-react";
import { useId } from "react";
import type { Proposal, ProposalRow, RowEdit } from "@/api/proposal";
import type { JobFile } from "@/api/types";
import { ProbeDetails } from "@/components/jobs/probe-details";
import { selectClass } from "@/components/patterns/field";
import { SelectField } from "@/components/patterns/select-field";
import { StatusLabel } from "@/components/patterns/status-label";
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
  /** The policy is `version`: a row whose episode has a file is kept as a second version. */
  version: boolean;
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
 * user can make. The head is the file label: the name, and at the far edge where the row
 * stands, in words. Every correction is re-checked by the app; the row says so in that same
 * place while it waits, so nothing below it moves. A file that is left out keeps only its
 * name and the way to take it back in.
 */
export function RowCard({
  row,
  proposal,
  file,
  title,
  version,
  checking,
  overridden,
  missing,
  onOverride,
  onEdit,
}: Props) {
  const { t, formatDuration, formatNumber } = useT();
  const id = useId();
  const app = appName[proposal.app];
  const issues = hasIssues(row);
  const offered = row.quality !== null;
  // The server skips a file the app did not offer, or one for an episode that has a file: that
  // is not the user's to undo here. Under `version` the user can leave the second version out
  // instead (#102): the row then keeps no version and its episode keeps its own file.
  const existingFile = row.warnings.some((w) => w.code === "existing_file");
  const leftOut = row.skip && row.skippedBy === "user";
  const keptAsVersion = version && existingFile && row.skip && !leftOut;
  const forced = row.warnings.filter(
    (w) => w.code === "not_offered" || (w.code === "existing_file" && !leftOut),
  );
  const mayToggle = forced.length === 0 || keptAsVersion;
  const toggleLabel = keptAsVersion
    ? "confirm.version.leaveOut"
    : leftOut && version && existingFile
      ? "confirm.version.takeBack"
      : row.skip
        ? "confirm.skip.undo"
        : "confirm.skip";

  const chosen = new Set(row.languages.map((l) => l.id));
  const addable = proposal.languages.filter((l) => !chosen.has(l.id));
  const ids = row.languages.map((l) => l.id);
  const caption = "text-sm font-normal text-muted-foreground";

  return (
    <li aria-busy={checking} className="grid gap-5 py-6" data-row={row.id}>
      <div className="flex flex-col gap-x-6 gap-y-2 sm:flex-row sm:items-start sm:justify-between">
        <h3
          translate="no"
          className={cn("[overflow-wrap:anywhere]", row.skip && "text-muted-foreground")}
        >
          {row.name}
        </h3>
        <div className="shrink-0 sm:w-56 sm:pt-0.5">
          {checking ? (
            <StatusLabel tone="waiting" role="status">
              {t("confirm.checking", { app })}
            </StatusLabel>
          ) : keptAsVersion ? (
            <StatusLabel tone="done">{t("confirm.state.version")}</StatusLabel>
          ) : row.skip ? (
            <StatusLabel tone="waiting" icon={Ban}>
              {t("confirm.state.out")}
            </StatusLabel>
          ) : issues ? (
            <StatusLabel tone="caution">
              {t(overridden ? "confirm.state.agreed" : "confirm.state.issues")}
            </StatusLabel>
          ) : (
            <StatusLabel tone="done">{t("confirm.state.ready")}</StatusLabel>
          )}
        </div>
      </div>

      {!row.skip && (
        <div className="grid gap-x-6 gap-y-4 sm:grid-cols-2">
          <section aria-labelledby={`${id}-to`} className="grid content-start gap-1">
            <h4 id={`${id}-to`} className={caption}>
              {t("confirm.row.goesTo", { app })}
            </h4>
            <p translate="no" className="font-medium [overflow-wrap:anywhere]">
              {title}
            </p>
            {row.episodes && row.episodes.length > 0 && (
              <ul className="grid gap-0.5 text-sm text-ink-2">
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
            <h4 id={`${id}-file`} className={caption}>
              {t("confirm.row.file")}
            </h4>
            <p className="figure">
              {row.durationMs > 0 ? formatDuration(row.durationMs) : t("confirm.row.noDuration")}
            </p>
            {file?.probe && <ProbeDetails name={row.name} probe={file.probe} open={false} />}
          </section>
        </div>
      )}

      {offered && !row.skip && (
        <div className="grid gap-4">
          <div className="grid items-start gap-x-6 gap-y-4 sm:grid-cols-2">
            <SelectField
              label={t("confirm.quality")}
              value={String(row.quality?.id ?? "")}
              disabled={checking}
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

            <div className="grid content-start gap-1">
              <span className={caption}>{t("confirm.languages")}</span>
              <ul className="flex min-h-10 flex-wrap items-center gap-2 pointer-coarse:min-h-11">
                {row.languages.length === 0 && (
                  <li className="text-muted-foreground">{t("confirm.languages.none")}</li>
                )}
                {row.languages.map((l) => (
                  <li key={l.id}>
                    <button
                      type="button"
                      disabled={checking}
                      aria-label={t("confirm.languages.remove", { name: l.name })}
                      onClick={() => onEdit({ languageIds: ids.filter((i) => i !== l.id) })}
                      className="inline-flex h-10 cursor-pointer items-center gap-1.5 rounded-md border border-input bg-card pr-2 pl-3 shadow-xs hover:bg-accent disabled:cursor-not-allowed disabled:opacity-60 pointer-coarse:h-11"
                    >
                      {l.name}
                      <X aria-hidden="true" className="size-4 text-muted-foreground" />
                    </button>
                  </li>
                ))}
                {addable.length > 0 && (
                  <li className="min-w-40 flex-1">
                    <select
                      name={`add-language-${row.id}`}
                      aria-label={t("confirm.languages.add")}
                      autoComplete="off"
                      className={selectClass}
                      value=""
                      disabled={checking}
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
                    </select>
                  </li>
                )}
              </ul>
            </div>
          </div>

          <dl className="flex flex-wrap gap-x-8 gap-y-1 text-sm">
            <div className="flex flex-wrap gap-x-2">
              <dt className="text-muted-foreground">{t("confirm.formats")}</dt>
              <dd translate="no" className="[overflow-wrap:anywhere]">
                {row.customFormats.length > 0
                  ? row.customFormats.join(", ")
                  : t("confirm.formats.none")}
              </dd>
            </div>
            <div className="flex flex-wrap gap-x-2">
              <dt className="text-muted-foreground">{t("confirm.score")}</dt>
              <dd className="figure">{formatNumber(row.customFormatScore)}</dd>
            </div>
          </dl>
        </div>
      )}

      {issues && !row.skip && (
        <div
          className={cn(
            "rounded-lg border border-caution-edge bg-caution p-4 text-foreground",
            missing && "outline-2 outline-offset-2 outline-destructive",
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
              name={`override-${row.id}`}
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
        <div role="note" className="grid max-w-[65ch] gap-1 text-sm text-ink-2">
          {keptAsVersion ? (
            <p>{t("confirm.version.kept")}</p>
          ) : (
            <>
              <p>{t("confirm.skipped")}</p>
              {leftOut && version && existingFile && <p>{t("confirm.version.leftOut")}</p>}
              {forced.map((w) => (
                <p key={w.code}>{t(warningText(w.code).key)}</p>
              ))}
            </>
          )}
        </div>
      )}

      {mayToggle && (
        <div>
          <Button
            type="button"
            variant="outline"
            disabled={checking}
            onClick={() => onEdit({ skip: keptAsVersion || !row.skip })}
          >
            {t(toggleLabel)}
          </Button>
        </div>
      )}
    </li>
  );
}
