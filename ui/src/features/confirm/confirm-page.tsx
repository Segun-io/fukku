import { Link, useParams } from "@tanstack/react-router";
import { ArrowLeft, LoaderCircle } from "lucide-react";
import { ApiError } from "@/api/client";
import { useJob } from "@/api/jobs";
import { useProposal } from "@/api/proposal";
import { useSession } from "@/api/session";
import type { JobDetail } from "@/api/types";
import { errorKey } from "@/components/error-message";
import { EmptyState, ErrorState, LoadingState } from "@/components/shell/states";
import { Button } from "@/components/ui/button";
import { useT } from "@/i18n";
import { ConfirmView } from "./confirm-view";

/** The proposal exists from the matched step on, and is editable while awaiting confirmation. */
const reviewable = ["matched", "awaiting-confirmation"];

const appNames = { radarr: "Radarr", sonarr: "Sonarr" } as const;

function Back({ id }: { id: string }) {
  const { t } = useT();
  return (
    <p>
      <Link
        to="/jobs/$id"
        params={{ id }}
        className="inline-flex min-h-11 items-center gap-1.5 text-sm underline-offset-4 hover:underline"
      >
        <ArrowLeft aria-hidden="true" className="size-4" />
        {t("confirm.back")}
      </Link>
    </p>
  );
}

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
      <EmptyState as="h1" title={t("confirm.wrongState.title")}>
        {t("confirm.wrongState.body")}
        <span className="mt-3 block">
          <Button asChild variant="outline">
            <Link to="/jobs/$id" params={{ id }}>
              {t("confirm.wrongState.action")}
            </Link>
          </Button>
        </span>
      </EmptyState>
    );
  } else {
    body = <Review job={data} />;
  }

  return (
    <section className="grid max-w-(--container-flow) gap-6">
      <Back id={id} />
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
        <h1>{t("confirm.title")}</h1>
        <p role="status" className="flex items-center gap-2 text-ink-2">
          <LoaderCircle aria-hidden="true" className="size-5 animate-spin" />
          {t("confirm.pending", { app })}
        </p>
      </>
    );
  }
  return (
    <>
      <h1>{t("confirm.title")}</h1>
      <p translate="no" className="text-ink-2 [overflow-wrap:anywhere]">
        {job.name ?? t("jobs.unnamed")}
      </p>
      <ConfirmView
        job={job}
        proposal={data}
        subtitleManager={session.data?.subtitleManager === true}
      />
    </>
  );
}
