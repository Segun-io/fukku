import { Ban, Check, ExternalLink, Hourglass, TriangleAlert } from "lucide-react";
import type { JobDetail, PostStepInfo } from "@/api/types";
import { StatusLabel, type StatusTone } from "@/components/patterns/status-label";
import { Notice } from "@/components/shell/notice";
import { Button } from "@/components/ui/button";
import type { TKey } from "@/i18n";
import { useT } from "@/i18n";
import { cn } from "@/lib/utils";

const appName = { radarr: "Radarr", sonarr: "Sonarr" } as const;

/** The post-processing steps the UI names (`import.step.<name>`); others show their own name. */
const knownSteps = ["mediaServerRefresh", "subtitles"] as const;
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
  const failedSteps = steps.filter((s) => s.status === "failed");
  const imported = imp?.rows.filter((r) => r.result === "imported").length ?? 0;
  const total = imp?.rows.length ?? 0;

  return (
    <section aria-labelledby="import-title" className={cn("grid gap-4", className)}>
      <h2 id="import-title">{t(finished ? "import.title.done" : "import.title")}</h2>

      {job.state === "importing" && (
        <p role="status" className="max-w-[65ch] text-ink-2">
          {t("import.running", { app: app || t("import.app") })}
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
            icon={Check}
            role="status"
            title={t("import.done.title", { title: job.target?.title ?? "" })}
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
              {t("import.done.body", { app: app || t("import.app") })}
              {failedSteps.length > 0 && <> {t("import.done.failedSteps")}</>}
            </p>
            {!result?.titleLink && <p className="mt-1">{t("import.link.none")}</p>}
          </Notice>
          {result?.monitored === false && (
            <Notice tone="caution" icon={TriangleAlert} title={t("import.unmonitored.title")}>
              {t(
                result.unmonitorReason === "chosen"
                  ? "import.unmonitored.chosen"
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
