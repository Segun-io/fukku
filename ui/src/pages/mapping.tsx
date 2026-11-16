import { Link, useParams } from "@tanstack/react-router";
import { ApiError } from "@/api/client";
import { useProposal } from "@/api/proposal";
import { errorKey } from "@/components/error-message";
import { BackToJob, FlowHeader } from "@/components/patterns/flow-header";
import { EmptyState, ErrorState, LoadingState } from "@/components/shell/states";
import { Button } from "@/components/ui/button";
import { MappingView } from "@/features/mapping/mapping-view";
import { useT } from "@/i18n";

const appNames = { radarr: "Radarr", sonarr: "Sonarr", "": "" } as const;

export function MappingPage() {
  const { id } = useParams({ from: "/jobs/$id/mapping" });
  const { t } = useT();
  const proposal = useProposal(id);

  const back = <BackToJob id={id}>{t("mapping.back")}</BackToJob>;

  const data = proposal.data;
  let body: React.ReactNode;
  if (proposal.error) {
    const err = proposal.error;
    if (err instanceof ApiError && err.status === 404) {
      body = (
        <EmptyState as="h1" title={t("job.notFound.title")}>
          {t("job.notFound.body")}
        </EmptyState>
      );
    } else if (err instanceof ApiError && err.status === 409) {
      body = (
        <EmptyState
          as="h1"
          title={t("mapping.wrongState.title")}
          action={
            <Button asChild variant="outline">
              <Link to="/jobs/$id" params={{ id }}>
                {t("mapping.openJob")}
              </Link>
            </Button>
          }
        >
          {t("mapping.wrongState.body")}
        </EmptyState>
      );
    } else {
      body = (
        <ErrorState as="h1" title={t("job.error.title")} onRetry={() => void proposal.refetch()}>
          {t(errorKey(err))}
        </ErrorState>
      );
    }
  } else if (!data) {
    body = <LoadingState />;
  } else if (data.state === "pending") {
    body = (
      <>
        <FlowHeader
          step="mapping"
          title={t("mapping.pending.title", { app: appNames[data.app] || "Radarr, Sonarr" })}
        >
          <p role="status">{t("mapping.pending.body")}</p>
        </FlowHeader>
        <LoadingState rows={2} className="max-w-(--container-flow)" />
      </>
    );
  } else {
    const app = appNames[data.app];
    body = (
      <>
        <FlowHeader
          step="mapping"
          title={t("mapping.title")}
          subject={
            data.target &&
            t("mapping.target", {
              title: data.target.title,
              year: String(data.target.year),
              app,
            })
          }
        >
          <p>{t("mapping.intro", { app })}</p>
          {data.app === "sonarr" && <p>{t("mapping.fromNames", { app })}</p>}
        </FlowHeader>
        <MappingView jobId={id} proposal={data} />
      </>
    );
  }

  return (
    <section className="grid gap-6">
      {back}
      {body}
    </section>
  );
}
