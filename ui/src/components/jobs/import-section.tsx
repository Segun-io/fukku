import {
  Check,
  CircleAlert,
  ExternalLink,
  Hourglass,
  LoaderCircle,
  TriangleAlert,
} from "lucide-react";
import type { JobDetail, PostStepInfo } from "@/api/types";
import { Notice } from "@/components/shell/notice";
import type { TKey } from "@/i18n";
import { useT } from "@/i18n";
import { cn } from "@/lib/utils";

const appName = { radarr: "Radarr", sonarr: "Sonarr" } as const;

/** The post-processing steps the UI names (`import.step.<name>`); others show their own name. */
const knownSteps = ["mediaServerRefresh", "subtitles"] as const;
const knownResults = ["imported", "skipped", "failed"] as const;

function StepIcon({ status }: { status: PostStepInfo["status"] }) {
  const cls = "mt-0.5 size-5 shrink-0";
  switch (status) {
    case "ok":
      return <Check aria-hidden="true" className={cn(cls, "text-primary")} />;
    case "failed":
      return <CircleAlert aria-hidden="true" className={cn(cls, "text-destructive")} />;
    case "skipped":
      return <Hourglass aria-hidden="true" className={cn(cls, "text-muted-foreground")} />;
    default:
      return (
        <LoaderCircle
          aria-hidden="true"
          className={cn(cls, "animate-spin text-muted-foreground")}
        />
      );
  }
}

/**
 * What happens after the confirmation: the app importing each file, then the steps that
 * follow (media server, subtitles), and at the end the link to the title and what to do
 * about a step that failed. A failed step does not undo the import.
 */
export function ImportSection({ job, className }: { job: JobDetail; className?: string }) {
  const { t } = useT();
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

  return (
    <section aria-labelledby="import-title" className={cn("grid gap-4", className)}>
      <h2 id="import-title">{t(finished ? "import.title.done" : "import.title")}</h2>

      {job.state === "importing" && (
        <p role="status" className="max-w-[65ch] text-ink-2">
          {t("import.running", { app: app || t("import.app") })}
        </p>
      )}

      {showRows && imp && (
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
              <li key={r.id} className="flex flex-wrap items-baseline justify-between gap-x-4 py-3">
                <span translate="no" className="[overflow-wrap:anywhere]">
                  {r.name || t("import.row", { n: i + 1, total: imp.rows.length })}
                </span>
                <span className={cn("font-medium", r.result === "failed" && "text-destructive")}>
                  {label}
                </span>
              </li>
            );
          })}
        </ul>
      )}

      {steps.length > 0 && (
        <div className="grid gap-2">
          {job.state === "post-processing" && (
            <p className="max-w-[65ch] text-ink-2">{t("import.post.running")}</p>
          )}
          <ul aria-label={t("import.steps")} className="divide-y border-y">
            {steps.map((s) => {
              const known = (knownSteps as readonly string[]).includes(s.step);
              return (
                <li key={s.step} className="flex gap-3 py-3">
                  <StepIcon status={s.status} />
                  <div className="min-w-0">
                    <p className="flex flex-wrap items-baseline justify-between gap-x-4">
                      <span className="font-medium" translate={known ? undefined : "no"}>
                        {known ? t(`import.step.${s.step}` as TKey) : s.step}
                      </span>
                      <span className="text-sm text-muted-foreground">
                        {t(`import.status.${s.status}` as TKey)}
                      </span>
                    </p>
                    {s.detail !== "" && (
                      <p className="figure mt-1 text-sm text-muted-foreground [overflow-wrap:anywhere]">
                        {s.detail}
                      </p>
                    )}
                    {s.status === "failed" && (
                      <p className="mt-1 max-w-[65ch] text-sm">
                        {known
                          ? t(`import.step.${s.step}.failed` as TKey)
                          : t("import.step.failed")}
                      </p>
                    )}
                  </div>
                </li>
              );
            })}
          </ul>
        </div>
      )}

      {finished && (
        <div className="grid gap-3">
          <Notice
            tone="done"
            icon={Check}
            role="status"
            title={t("import.done.title", { title: job.target?.title ?? "" })}
          >
            {t("import.done.body", { app: app || t("import.app") })}
            {failedSteps.length > 0 && <> {t("import.done.failedSteps")}</>}
          </Notice>
          {result?.titleLink ? (
            <p>
              <a
                href={result.titleLink}
                target="_blank"
                rel="noopener noreferrer"
                className="inline-flex min-h-11 items-center gap-2 font-medium underline underline-offset-4"
              >
                {t("import.link")}
                <ExternalLink aria-hidden="true" className="size-4" />
              </a>
            </p>
          ) : (
            <p className="max-w-[65ch] text-sm text-muted-foreground">{t("import.link.none")}</p>
          )}
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
    </section>
  );
}
