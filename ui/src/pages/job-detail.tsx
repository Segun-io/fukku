import { Link, useParams } from "@tanstack/react-router";
import { ArrowLeft } from "lucide-react";
import { ApiError } from "@/api/client";
import { useJob } from "@/api/jobs";
import { useSession } from "@/api/session";
import type { JobDetail } from "@/api/types";
import { errorKey } from "@/components/error-message";
import { ImportSection } from "@/components/jobs/import-section";
import { JobActions } from "@/components/jobs/job-actions";
import { ManualImportSection } from "@/components/jobs/manual-import";
import { MatchSection } from "@/components/jobs/match-section";
import { ProbeDetails } from "@/components/jobs/probe-details";
import { StateBadge } from "@/components/jobs/state-badge";
import { FileList, type FileListItem } from "@/components/patterns/file-list";
import { FileProgress, FileProgressList } from "@/components/patterns/file-progress";
import { jobPath, StatusTimeline, standingAt } from "@/components/patterns/status-timeline";
import { EmptyState, ErrorState, LoadingState } from "@/components/shell/states";
import { Button } from "@/components/ui/button";
import { useT } from "@/i18n";
import { jobErrorText, refusalText, skipReasonText } from "@/lib/job-text";

/** M1 ends at `probed`: its timeline shows the steps up to there. */
const m1Steps = jobPath.slice(0, 4);

/** States in which nothing more happens by itself: the history is the last thing to read. */
const settled: readonly JobDetail["state"][] = ["probed", "done", "cancelled"];

const appNames = { radarr: "Radarr", sonarr: "Sonarr" } as const;

/** Up to this many videos show what was found in them without being asked. */
const openProbes = 3;

/** A section of the page: a hairline above, then its heading. */
const section = "grid gap-4 border-t pt-8";

function BackLink() {
  const { t } = useT();
  return (
    <p>
      <Link
        to="/jobs"
        className="inline-flex min-h-11 items-center gap-1.5 text-sm underline-offset-4 hover:underline"
      >
        <ArrowLeft aria-hidden="true" className="size-4" />
        {t("job.back")}
      </Link>
    </p>
  );
}

export function JobDetailPage() {
  const { id } = useParams({ from: "/jobs/$id" });
  const { t } = useT();
  const job = useJob(id);

  if (job.isPending) {
    return (
      <section className="grid gap-6">
        <BackLink />
        <LoadingState />
      </section>
    );
  }

  if (job.isError) {
    const notFound = job.error instanceof ApiError && job.error.status === 404;
    return (
      <section className="grid gap-6">
        <BackLink />
        {notFound ? (
          <EmptyState as="h1" title={t("job.notFound.title")}>
            {t("job.notFound.body")}
          </EmptyState>
        ) : (
          <ErrorState as="h1" title={t("job.error.title")} onRetry={() => void job.refetch()}>
            {t(errorKey(job.error))}
          </ErrorState>
        )}
      </section>
    );
  }

  return <JobView job={job.data} />;
}

/**
 * One upload. The page leads with what the user needs now and ends with what is only for
 * the record: while the upload moves, or after it stopped, the timeline comes first; once
 * the files are checked, the next step (importing by hand, in M1) comes first and the
 * timeline last.
 */
function JobView({ job }: { job: JobDetail }) {
  const { t, formatBytes, formatDate } = useT();
  const session = useSession();
  const showOwner = session.data?.user.admin === true && job.owner !== session.data.user.name;
  const standing = standingAt(job.state, job.history);
  const timelineSteps = jobPath.indexOf(standing) < m1Steps.length ? m1Steps : jobPath;

  const explained = job.error && (job.state === "failed" || job.state === "stalled");
  // Importing, or stalled there: the app's import command may still be running.
  const importMayRun =
    standing === "importing" &&
    (job.state === "importing" || job.state === "stalled") &&
    // Nothing is submitted to the app yet: the owner may cancel, and nothing can finish later.
    job.import?.status !== "pending";
  const errorText = explained
    ? jobErrorText(job.error?.code ?? "", job.target ? appNames[job.target.app] : "")
    : null;

  const items: FileListItem[] = job.files.map((f) => ({
    id: f.id,
    name: f.name,
    kind: f.kind,
    size: f.size,
    status: f.status,
    reason:
      f.status === "refused" && f.reason
        ? t(refusalText(f.reason).key, refusalText(f.reason).params)
        : undefined,
    from: f.from ? (f.from.split("/").pop() ?? f.from) : undefined,
  }));
  const probes = job.files.filter((f) => f.kind === "video" && f.status === "ok" && f.probe);
  const archivesWithSkips = job.files.filter(
    (f) => f.kind === "archive" && (f.extraction?.skipped?.length ?? 0) > 0,
  );

  const isSettled = settled.includes(job.state);
  const progress = (
    <section className={section}>
      <h2>{t("job.progress")}</h2>
      <StatusTimeline
        state={job.state}
        history={job.history}
        steps={timelineSteps}
        detail={errorText ? t(errorText.key, errorText.params) : undefined}
      />
    </section>
  );

  return (
    <article className="grid max-w-(--container-flow) gap-8">
      <header className="grid gap-3">
        <BackLink />
        <h1 translate="no" className="[overflow-wrap:anywhere]">
          {job.name ?? t("jobs.unnamed")}
        </h1>
        <div className="flex flex-wrap items-center gap-x-4 gap-y-1">
          <StateBadge state={job.state} />
          <span className="text-ink-2">{t(`stateHint.${job.state}`)}</span>
        </div>
        <p className="flex flex-wrap gap-x-4 gap-y-1 text-sm text-muted-foreground">
          <span className="figure">{t("job.size", { size: formatBytes(job.totalSize) })}</span>
          <span>{t("job.started", { date: formatDate(job.createdAt) })}</span>
          {showOwner && <span translate="no">{t("job.owner", { owner: job.owner })}</span>}
        </p>
        {job.expiresAt && (
          <p className="text-sm text-muted-foreground">
            {t("job.expires", { date: formatDate(job.expiresAt) })}
          </p>
        )}
        <JobActions job={job} importMayRun={importMayRun} />
      </header>

      <MatchSection job={job} className={job.state === "probed" ? "border-t pt-8" : section} />

      <ImportSection job={job} className={section} />

      {job.state === "probed" && (
        <ManualImportSection items={job.manualImport} className="border-t pt-8" />
      )}

      {!isSettled && progress}

      {job.state === "uploading" && job.uploads.length > 0 && (
        <section className={section}>
          <h2>{t("job.uploads.title")}</h2>
          <FileProgressList>
            {job.uploads.map((u) => (
              <FileProgress
                key={u.id}
                name={u.name}
                size={u.size}
                sent={u.offset}
                status="unfinished"
              />
            ))}
          </FileProgressList>
          <p className="max-w-[65ch] text-ink-2">{t("job.uploads.hint")}</p>
          <div>
            <Button asChild>
              <Link to="/upload">{t("job.uploads.continue")}</Link>
            </Button>
          </div>
        </section>
      )}

      <section className={section}>
        <h2>{t("job.files")}</h2>
        {items.length === 0 ? (
          <p className="text-ink-2">{t("job.files.none")}</p>
        ) : (
          <FileList files={items} />
        )}
        {archivesWithSkips.map((a) => (
          <div key={a.id} className="grid gap-2">
            <h3 translate="no" className="[overflow-wrap:anywhere]">
              {t("job.skipped.title", { name: a.name })}
            </h3>
            <p className="max-w-[65ch] text-sm text-muted-foreground">{t("job.skipped.note")}</p>
            <ul className="grid list-disc gap-1 pl-5 text-sm">
              {a.extraction?.skipped?.map((s, i) => (
                // The same name can be skipped twice in one archive: the index keeps the key unique.
                // biome-ignore lint/suspicious/noArrayIndexKey: a fixed list for one render
                <li key={`${s.name}-${i}`} className="[overflow-wrap:anywhere]">
                  {t("job.skipped.item", {
                    name: s.name,
                    reason: t(skipReasonText(s.reason).key, skipReasonText(s.reason).params),
                  })}
                </li>
              ))}
            </ul>
          </div>
        ))}
        {probes.length > 0 && (
          <div className="divide-y border-y">
            {probes.map(
              (f) =>
                f.probe && (
                  <ProbeDetails
                    key={f.id}
                    name={f.name}
                    probe={f.probe}
                    open={probes.length <= openProbes}
                  />
                ),
            )}
          </div>
        )}
      </section>

      {isSettled && progress}
    </article>
  );
}
