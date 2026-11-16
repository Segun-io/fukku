import { useParams } from "@tanstack/react-router";
import { ApiError } from "@/api/client";
import { useJob } from "@/api/jobs";
import { useApps } from "@/api/match";
import { errorKey } from "@/components/error-message";
import { BackToJob, FlowHeader } from "@/components/patterns/flow-header";
import { EmptyState, ErrorState, LoadingState } from "@/components/shell/states";
import { MatchView } from "@/features/match/match-view";
import { useT } from "@/i18n";

/** A title can be (re)chosen in these states (#37). */
const matchable = ["probed", "matched", "awaiting-confirmation"];

export function MatchPage() {
  const { id } = useParams({ from: "/jobs/$id/match" });
  const { t } = useT();
  const job = useJob(id);
  const apps = useApps();

  const back = <BackToJob id={id}>{t("match.back")}</BackToJob>;

  const jobData = job.data;
  const appsData = apps.data;
  // An app that did not answer (`unavailable`) is not offered.
  const usable = appsData && {
    radarr: appsData.radarr?.unavailable ? undefined : appsData.radarr,
    sonarr: appsData.sonarr?.unavailable ? undefined : appsData.sonarr,
  };
  const failed = job.error ?? apps.error;
  let body: React.ReactNode;
  if (failed) {
    const notFound = failed instanceof ApiError && failed.status === 404;
    body = notFound ? (
      <EmptyState as="h1" title={t("job.notFound.title")}>
        {t("job.notFound.body")}
      </EmptyState>
    ) : (
      <ErrorState
        as="h1"
        title={t("job.error.title")}
        onRetry={() => {
          void job.refetch();
          void apps.refetch();
        }}
      >
        {t(errorKey(failed))}
      </ErrorState>
    );
  } else if (!jobData || !appsData || !usable) {
    body = <LoadingState />;
  } else if (!matchable.includes(jobData.state)) {
    body = (
      <EmptyState as="h1" title={t("match.wrongState.title")}>
        {t("match.wrongState.body")}
      </EmptyState>
    );
  } else if (!usable.radarr && !usable.sonarr) {
    const down = appsData.radarr?.unavailable || appsData.sonarr?.unavailable;
    body = down ? (
      <ErrorState as="h1" title={t("match.unavailable.title")} onRetry={() => void apps.refetch()}>
        {t("errors.arr_unavailable")}
      </ErrorState>
    ) : (
      <EmptyState as="h1" title={t("match.noApps.title")}>
        {t("match.noApps.body")}
      </EmptyState>
    );
  } else {
    body = (
      <>
        <FlowHeader
          step="match"
          title={t("match.title")}
          subject={jobData.name ?? t("jobs.unnamed")}
        >
          <p>{t("match.intro")}</p>
        </FlowHeader>
        <MatchView job={jobData} apps={usable} />
      </>
    );
  }

  return (
    <section className="grid max-w-(--container-flow) gap-6">
      {back}
      {body}
    </section>
  );
}
