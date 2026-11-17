import { Link, useParams } from "@tanstack/react-router";
import { ApiError } from "@/api/client";
import { useJob } from "@/api/jobs";
import { useProposal } from "@/api/proposal";
import { useSession } from "@/api/session";
import type { JobDetail } from "@/api/types";
import { errorKey } from "@/components/error-message";
import { BackToJob, FlowHeader } from "@/components/patterns/flow-header";
import { StatusLabel } from "@/components/patterns/status-label";
import { EmptyState, ErrorState, LoadingState } from "@/components/shell/states";
import { Button } from "@/components/ui/button";
import { useT } from "@/i18n";
import { ConfirmView } from "./confirm-view";

/** The proposal exists from the matched step on, and is editable while awaiting confirmation. */
const reviewable = ["matched", "awaiting-confirmation"];

const appNames = { radarr: "Radarr", sonarr: "Sonarr" } as const;

export function ConfirmPage() {
  const { id } = useParams({ from: "/jobs/$id/confirm" });
  const { t } = useT();
  const job = useJob(id);
  const data = job.data;

  let body: React.ReactNode;
  if (job.error) {
    const notFound = job.error instanceof ApiError && job.error.status === 404;
    body = notFound ? (
      <EmptyState as="h1" title={t("job.notFound.title")}>
        {t("job.notFound.body")}
      </EmptyState>
    ) : (
      <ErrorState as="h1" title={t("confirm.error.title")} onRetry={() => void job.refetch()}>
        {t(errorKey(job.error))}
      </ErrorState>
    );
  } else if (!data) {
    body = <LoadingState />;
  } else if (!reviewable.includes(data.state)) {
    body = (
      <EmptyState
        as="h1"
        title={t("confirm.wrongState.title")}
        action={
          <Button asChild variant="outline">
            <Link to="/jobs/$id" params={{ id }}>
              {t("confirm.wrongState.action")}
            </Link>
          </Button>
        }
      >
        {t("confirm.wrongState.body")}
      </EmptyState>
    );
  } else {
    body = <Review job={data} />;
  }

  return (
    <section className="grid max-w-(--container-flow) gap-6">
      <BackToJob id={id}>{t("confirm.back")}</BackToJob>
      {body}
    </section>
  );
}

/** The proposal of a job that can be reviewed: pending, then the rows. */
function Review({ job }: { job: JobDetail }) {
  const { t } = useT();
  const session = useSession();
  const proposal = useProposal(job.id);

  if (proposal.error) {
    return (
      <ErrorState as="h1" title={t("confirm.error.title")} onRetry={() => void proposal.refetch()}>
        {t(errorKey(proposal.error))}
      </ErrorState>
    );
  }
  const data = proposal.data;
  if (!data) return <LoadingState />;
  if (data.state === "pending") {
    const app = job.target ? appNames[job.target.app] : "Radarr";
    return (
      <>
        <FlowHeader step="confirm" title={t("confirm.title")} />
        <StatusLabel tone="waiting" role="status" className="text-base">
          {t("confirm.pending", { app })}
        </StatusLabel>
        <LoadingState rows={2} />
      </>
    );
  }
  return (
    <>
      <FlowHeader step="confirm" title={t("confirm.title")} subject={job.name ?? t("jobs.unnamed")}>
        <p>
          {t("confirm.intro", { app: data.app === "" ? "Radarr or Sonarr" : appNames[data.app] })}
        </p>
      </FlowHeader>
      <ConfirmView
        job={job}
        proposal={data}
        subtitleManager={session.data?.subtitleManager === true}
        refreshing={proposal.isFetching}
      />
    </>
  );
}
