import { Link, useParams } from "@tanstack/react-router";
import { ApiError } from "@/api/client";
import { useJob } from "@/api/jobs";
import { errorKey } from "@/components/error-message";
import { BackToJob, FlowHeader } from "@/components/patterns/flow-header";
import { StatusLabel } from "@/components/patterns/status-label";
import { EmptyState, ErrorState, LoadingState } from "@/components/shell/states";
import { Button } from "@/components/ui/button";
import { useT } from "@/i18n";
import { Flow2View } from "./flow2-view";

/** A flow 2 job can be compared from the match until the user confirms. */
const comparable = ["matched", "awaiting-confirmation"];

/**
 * Flow 2 (plan §8): the film already has a file, and the upload is another language of it.
 * The page compares the two files and leads to the merge (after the sync check) or to a
 * second version.
 */
export function Flow2Page() {
  const { id } = useParams({ from: "/jobs/$id/flow2" });
  const { t } = useT();
  const job = useJob(id);
  const data = job.data;

  const toJob = (label: string) => (
    <Button asChild variant="outline">
      <Link to="/jobs/$id" params={{ id }}>
        {label}
      </Link>
    </Button>
  );

  let body: React.ReactNode;
  if (job.error) {
    const notFound = job.error instanceof ApiError && job.error.status === 404;
    body = notFound ? (
      <EmptyState as="h1" title={t("job.notFound.title")}>
        {t("job.notFound.body")}
      </EmptyState>
    ) : (
      <ErrorState as="h1" title={t("flow2.error.title")} onRetry={() => void job.refetch()}>
        {t(errorKey(job.error))}
      </ErrorState>
    );
  } else if (!data) {
    body = <LoadingState />;
  } else if (data.state === "probed") {
    // The film's file changed after the match (`library_changed`), or no title is chosen.
    body = (
      <EmptyState
        as="h1"
        title={t("flow2.probed.title")}
        action={
          <Button asChild>
            <Link to="/jobs/$id/match" params={{ id }}>
              {t("flow2.changed.action")}
            </Link>
          </Button>
        }
      >
        {t("flow2.probed.body")}
      </EmptyState>
    );
  } else if (!comparable.includes(data.state)) {
    body = (
      <EmptyState
        as="h1"
        title={t("flow2.wrongState.title")}
        action={toJob(t("flow2.wrongState.action"))}
      >
        {t("flow2.wrongState.body")}
      </EmptyState>
    );
  } else if (!data.flow2) {
    body = (
      <EmptyState
        as="h1"
        title={t("flow2.notFlow2.title")}
        action={
          <Button asChild variant="outline">
            <Link to="/jobs/$id/confirm" params={{ id }}>
              {t("job.review")}
            </Link>
          </Button>
        }
      >
        {t("flow2.notFlow2.body")}
      </EmptyState>
    );
  } else {
    const flow2 = data.flow2;
    const header = (
      <FlowHeader
        title={t("flow2.title")}
        subject={
          data.target &&
          t("flow2.subject", { title: data.target.title, year: String(data.target.year) })
        }
      >
        <p>{t("flow2.intro")}</p>
      </FlowHeader>
    );
    if (flow2.state === "pending") {
      body = (
        <>
          {header}
          <StatusLabel tone="waiting" role="status" className="text-base">
            {t("flow2.pending")}
          </StatusLabel>
          <LoadingState rows={2} />
        </>
      );
    } else if (!flow2.upload) {
      body = (
        <>
          {header}
          <EmptyState
            title={t("flow2.noUpload.title")}
            action={toJob(t("flow2.wrongState.action"))}
          >
            {t("flow2.noUpload.body")}
          </EmptyState>
        </>
      );
    } else {
      body = (
        <>
          {header}
          <Flow2View jobId={id} flow2={flow2} upload={flow2.upload} />
        </>
      );
    }
  }

  return (
    <section className="grid max-w-(--container-flow) gap-6">
      <BackToJob id={id}>{t("flow2.back")}</BackToJob>
      {body}
    </section>
  );
}
