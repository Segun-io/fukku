import { Ban, Check, ExternalLink, Hourglass, Info, TriangleAlert } from "lucide-react";
import type { JobDetail, MergeInfo, PostStepInfo, VersionResult } from "@/api/types";
import { StatusLabel, type StatusTone } from "@/components/patterns/status-label";
import { Notice } from "@/components/shell/notice";
import { Button } from "@/components/ui/button";
import type { TKey } from "@/i18n";
import { useT } from "@/i18n";
import { versionNoticeText } from "@/lib/flow2-text";
import { mergeRefusalText } from "@/lib/job-text";
import { cn } from "@/lib/utils";

const appName = { radarr: "Radarr", sonarr: "Sonarr" } as const;

/** The post-processing steps the UI names (`import.step.<name>`); others show their own name. */
const knownSteps = ["mediaServerRefresh", "mediaServerVersions", "subtitles"] as const;
const knownResults = ["imported", "skipped", "failed"] as const;

/** The meaning of each result of a file and of each status of a step (docs/design.md, §3). */
const rowTone: Record<string, StatusTone> = {
  imported: "done",
  skipped: "waiting",
  failed: "error",
};
const stepTone: Record<PostStepInfo["status"], StatusTone> = {
  ok: "done",
  failed: "error",
  skipped: "waiting",
  pending: "waiting",
};

/** Where the state of a row stands, so that the states read down one column. */
const stateColumn = "shrink-0 sm:w-36";

/** The merge of a flow 2a job (#55): where it stands, then what it adds. */
function MergeProgress({ merge, job }: { merge: MergeInfo; job: JobDetail }) {
  const { t, formatBytes } = useT();
  const failed = merge.state === "failed";
  const made = merge.state === "merged";
  const imported = job.import?.rows.some((r) => r.result === "imported") ?? false;
  // The file was made and the job still failed: Radarr did not take it (`merge_rejected`).
  const rejected = made && !imported && job.state === "failed";
  const refusal = mergeRefusalText(merge.refusal);
  const none = t("import.merge.fact.no");
  return (
    <div className="grid gap-4">
      <h3>{t("import.merge.title")}</h3>
      <ol aria-label={t("import.merge.steps")} className="divide-y border-y">
        <li className="flex flex-col gap-x-4 gap-y-1 py-3 sm:flex-row sm:items-baseline sm:justify-between">
          <span className="font-medium">{t("import.merge.step.make")}</span>
          <StatusLabel tone={failed ? "error" : made ? "done" : "waiting"} className={stateColumn}>
            {t(
              failed
                ? "import.status.failed"
                : made
                  ? "import.status.ok"
                  : merge.state === "running"
                    ? "import.status.pending"
                    : "import.row.waiting",
            )}
          </StatusLabel>
        </li>
        <li className="flex flex-col gap-x-4 gap-y-1 py-3 sm:flex-row sm:items-baseline sm:justify-between">
          <span className="font-medium">{t("import.merge.step.import")}</span>
          <StatusLabel
            tone={imported ? "done" : rejected ? "error" : "waiting"}
            icon={failed ? Ban : undefined}
            className={stateColumn}
          >
            {t(
              imported
                ? "import.status.ok"
                : rejected
                  ? "import.status.failed"
                  : failed
                    ? "import.status.skipped"
                    : "import.row.waiting",
            )}
          </StatusLabel>
        </li>
      </ol>
      {merge.state === "pending" && <p role="status">{t("import.merge.pending")}</p>}
      {merge.state === "running" && (
        <p role="status" className="max-w-[65ch] text-ink-2">
          {t("import.merge.running")}
        </p>
      )}
      {made && (
        <p role="status" className="max-w-[65ch] text-ink-2">
          {t("import.merge.merged", { name: merge.name, size: formatBytes(merge.size) })}
        </p>
      )}
      {failed && (
        <Notice role="alert" tone="error" icon={TriangleAlert} title={t("import.merge.failed")}>
          {merge.refusal !== "" && t(refusal.key, refusal.params)}
        </Notice>
      )}
      <dl className="grid grid-cols-1 gap-x-6 gap-y-2 text-sm sm:grid-cols-[auto_minmax(0,1fr)]">
        <div className="grid sm:col-span-2 sm:grid-cols-subgrid">
          <dt className="text-muted-foreground">{t("import.merge.fact.audio")}</dt>
          <dd className="figure">{merge.audioTracks.length}</dd>
        </div>
        <div className="grid sm:col-span-2 sm:grid-cols-subgrid">
          <dt className="text-muted-foreground">{t("import.merge.fact.subtitles")}</dt>
          <dd className="figure">{merge.subtitleTracks.length}</dd>
        </div>
        <div className="grid sm:col-span-2 sm:grid-cols-subgrid">
          <dt className="text-muted-foreground">{t("import.merge.fact.offset")}</dt>
          <dd className="figure">
            {t("import.merge.fact.offset.value", { offset: String(merge.offsetMs) })}
          </dd>
        </div>
        {merge.languageTag !== "" && (
          <div className="grid sm:col-span-2 sm:grid-cols-subgrid">
            <dt className="text-muted-foreground">{t("import.merge.fact.tag")}</dt>
            <dd translate="no">{merge.languageTag}</dd>
          </div>
        )}
        {merge.trackName !== "" && (
          <div className="grid sm:col-span-2 sm:grid-cols-subgrid">
            <dt className="text-muted-foreground">{t("import.merge.fact.name")}</dt>
            <dd translate="no">{merge.trackName}</dd>
          </div>
        )}
        <div className="grid sm:col-span-2 sm:grid-cols-subgrid">
          <dt className="text-muted-foreground">{t("import.merge.fact.original")}</dt>
          <dd>{merge.keepOriginalDefault ? t("import.merge.fact.yes") : none}</dd>
        </div>
      </dl>
    </div>
  );
}

const versionStatuses = ["placing", "linked", "grouped", "waiting", "film_deleted", "missing"];

/** Where the second version stands in the media server, and what to know about it. */
function VersionStatus({ version }: { version: VersionResult }) {
  const { t } = useT();
  const known = versionStatuses.includes(version.status);
  const tone: StatusTone =
    version.status === "grouped"
      ? "done"
      : version.status === "missing" || version.status === "film_deleted"
        ? "caution"
        : "waiting";
  return (
    <div className="grid gap-2">
      <StatusLabel tone={tone} role="status" className="text-base">
        {known
          ? t(`import.version.status.${version.status}` as TKey)
          : t("import.version.status.unknown", { status: version.status })}
      </StatusLabel>
      {version.detail !== "" && (
        <p className="font-mono text-sm text-muted-foreground [overflow-wrap:anywhere]">
          {version.detail}
        </p>
      )}
      {version.notices.length > 0 && (
        <ul className="grid list-disc gap-1 pl-5 text-sm text-ink-2">
          {version.notices.map((code) => (
            <li key={code} className="max-w-[65ch]">
              {t(versionNoticeText(code).key, versionNoticeText(code).params)}
            </li>
          ))}
        </ul>
      )}
    </div>
  );
}

/** The merge warnings of B6: what can still replace the merged file. One notice for each. */
const mergeWarningCodes = ["search_can_replace", "upgrade_can_replace"] as const;

/**
 * What happens after the confirmation, and what it left. While the app imports: how many
 * files are in the library, as a figure and a tray that fills, then each file with its
 * state. When it is done the result leads: the title is in the library, the link to it,
 * and, if the title was left unmonitored, what that means; the steps that followed (media
 * server, subtitles) come after, for the record. A failed step does not undo the import.
 */
export function ImportSection({ job, className }: { job: JobDetail; className?: string }) {
  const { t, formatNumber } = useT();
  const imp = job.import;
  const result = job.result;
  if (!imp && !result) return null;

  const app = job.target ? appName[job.target.app] : "";
  const steps = result?.postProcessing ?? [];
  const showRows =
    imp &&
    imp.rows.length > 0 &&
    (job.state === "importing" || job.state === "failed" || job.state === "stalled");
  const finished = job.state === "done";
  // A second version is placed by Fukku itself and has no rows of its own in the app.
  const isVersion = result?.version != null || (job.flow2 != null && !imp?.merge);
  // `subtitlesOnly` leaves the episodes that have a file alone; the new ones of the same upload
  // still import, so what the result says depends on what was imported.
  const subtitlesOnly = result?.seasonDub?.policy === "subtitlesOnly";
  const anyImported = imp?.rows.some((r) => r.result === "imported") ?? false;
  const nothingImported = subtitlesOnly && !anyImported;
  const seasonList = result?.seasonDub?.seasons ?? [];
  const failedSteps = steps.filter((s) => s.status === "failed");
  const imported = imp?.rows.filter((r) => r.result === "imported").length ?? 0;
  const total = imp?.rows.length ?? 0;

  return (
    <section aria-labelledby="import-title" className={cn("grid gap-4", className)}>
      <h2 id="import-title">{t(finished ? "import.title.done" : "import.title")}</h2>

      {imp?.merge && <MergeProgress merge={imp.merge} job={job} />}

      {job.state === "importing" && !imp?.merge && (
        <p role="status" className="max-w-[65ch] text-ink-2">
          {t(isVersion ? "import.version.running" : "import.running", {
            app: app || t("import.app"),
          })}
        </p>
      )}
      {job.state === "stalled" && job.error?.code === "arr_unavailable" && (
        <StatusLabel tone="waiting" icon={Hourglass} className="text-base">
          {t("import.waiting", { app: app || t("import.app") })}
        </StatusLabel>
      )}

      {finished && (
        <div className="grid gap-3">
          <Notice
            tone="done"
            icon={nothingImported ? Info : Check}
            role="status"
            title={
              nothingImported
                ? t("import.seasonDub.subtitlesOnly.title")
                : result?.version
                  ? t("import.version.done.title", { title: job.target?.title ?? "" })
                  : result?.merge
                    ? t("import.merge.done.title")
                    : t("import.done.title", { title: job.target?.title ?? "" })
            }
            action={
              result?.titleLink ? (
                <Button asChild variant="outline">
                  <a href={result.titleLink} target="_blank" rel="noopener noreferrer">
                    {t("import.link")}
                    <ExternalLink aria-hidden="true" />
                  </a>
                </Button>
              ) : undefined
            }
          >
            <p>
              {nothingImported
                ? t("import.seasonDub.subtitlesOnly")
                : result?.version
                  ? t("import.version.done.body", {
                      name: result.version.name,
                      label: result.version.label,
                    })
                  : result?.merge
                    ? t("import.merge.done.body", {
                        app: app || t("import.app"),
                        name: result.merge.name,
                      })
                    : t("import.done.body", { app: app || t("import.app") })}
              {failedSteps.length > 0 && <> {t("import.done.failedSteps")}</>}
            </p>
            {result?.seasonDub?.policy === "replace" && (
              <p className="mt-1">
                {t("import.seasonDub.replace", {
                  count: seasonList.length,
                  seasons: seasonList.join(", "),
                })}
              </p>
            )}
            {subtitlesOnly && anyImported && (
              <p className="mt-1">{t("import.seasonDub.subtitlesOnly.mixed")}</p>
            )}
            {!result?.titleLink && !nothingImported && (
              <p className="mt-1">{t("import.link.none")}</p>
            )}
          </Notice>
          {result?.version && <VersionStatus version={result.version} />}
          {result?.merge && result.merge.warnings.length > 0
            ? result.merge.warnings.map((w) => {
                const known = (mergeWarningCodes as readonly string[]).includes(w.code);
                return (
                  <Notice
                    key={w.code}
                    tone="caution"
                    icon={TriangleAlert}
                    title={t(
                      known
                        ? (`import.merge.warning.${w.code}.title` as TKey)
                        : "import.merge.warning.unknown.title",
                    )}
                  >
                    {t(
                      known
                        ? (`import.merge.warning.${w.code}` as TKey)
                        : "import.merge.warning.unknown",
                      { app: app || t("import.app"), code: w.code },
                    )}
                  </Notice>
                );
              })
            : result?.monitored === false && (
                <Notice tone="caution" icon={TriangleAlert} title={t("import.unmonitored.title")}>
                  {t(
                    result.unmonitorReason === "chosen"
                      ? "import.unmonitored.chosen"
                      : result.unmonitorReason === "merged"
                        ? "import.unmonitored.merged"
                        : "import.unmonitored.body",
                    { app: app || t("import.app") },
                  )}
                </Notice>
              )}
        </div>
      )}

      {showRows && imp && (
        <div className="grid gap-3">
          <div className="grid gap-2">
            <p className="figure text-sm whitespace-normal">
              {t("import.count", {
                done: formatNumber(imported),
                total: formatNumber(total),
              })}
            </p>
            <div
              role="progressbar"
              aria-label={t("import.rows")}
              aria-valuemin={0}
              aria-valuemax={total}
              aria-valuenow={imported}
              aria-valuetext={t("import.count", {
                done: formatNumber(imported),
                total: formatNumber(total),
              })}
              data-state={
                job.state === "failed" ? "failed" : job.state === "stalled" ? "waiting" : "moving"
              }
              className="tray"
              style={{ "--filled": (imported / total) * 100 } as React.CSSProperties}
            >
              <span />
            </div>
          </div>
          <ul aria-label={t("import.rows")} className="divide-y border-y">
            {imp.rows.map((r, i) => {
              const known = (knownResults as readonly string[]).includes(r.result);
              const label =
                r.result === ""
                  ? t("import.row.waiting")
                  : known
                    ? t(`import.row.${r.result}` as TKey)
                    : r.result;
              return (
                <li
                  key={r.id}
                  className="flex flex-col gap-x-4 gap-y-1 py-3 sm:flex-row sm:items-baseline sm:justify-between"
                >
                  <span translate="no" className="min-w-0 font-medium [overflow-wrap:anywhere]">
                    {r.name || t("import.row", { n: i + 1, total: imp.rows.length })}
                  </span>
                  <StatusLabel
                    tone={rowTone[r.result] ?? "waiting"}
                    icon={r.result === "skipped" ? Ban : undefined}
                    className={stateColumn}
                  >
                    {label}
                  </StatusLabel>
                </li>
              );
            })}
          </ul>
        </div>
      )}

      {steps.length > 0 && (
        <div className="grid gap-2">
          {job.state === "post-processing" && (
            <p className="max-w-[65ch] text-ink-2">{t("import.post.running")}</p>
          )}
          {finished && <h3 className="mt-2 text-muted-foreground">{t("import.steps")}</h3>}
          <ul aria-label={t("import.steps")} className="divide-y border-y">
            {steps.map((s) => {
              const known = (knownSteps as readonly string[]).includes(s.step);
              return (
                <li key={s.step} className="grid gap-1 py-3">
                  <p className="flex flex-col gap-x-4 gap-y-1 sm:flex-row sm:items-baseline sm:justify-between">
                    <span className="min-w-0 font-medium" translate={known ? undefined : "no"}>
                      {known ? t(`import.step.${s.step}` as TKey) : s.step}
                    </span>
                    <StatusLabel
                      tone={stepTone[s.status]}
                      icon={s.status === "skipped" ? Ban : undefined}
                      className={stateColumn}
                    >
                      {t(`import.status.${s.status}` as TKey)}
                    </StatusLabel>
                  </p>
                  {s.status === "failed" && (
                    <p className="max-w-[65ch] text-sm text-ink-2">
                      {known ? t(`import.step.${s.step}.failed` as TKey) : t("import.step.failed")}
                    </p>
                  )}
                  {s.detail !== "" && (
                    <p className="font-mono text-sm text-muted-foreground [overflow-wrap:anywhere]">
                      {s.detail}
                    </p>
                  )}
                </li>
              );
            })}
          </ul>
        </div>
      )}
    </section>
  );
}
