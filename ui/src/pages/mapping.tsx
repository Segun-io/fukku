import { Link, useParams } from "@tanstack/react-router";
import { ArrowLeft } from "lucide-react";
import { ApiError } from "@/api/client";
import { useProposal } from "@/api/proposal";
import { errorKey } from "@/components/error-message";
import { EmptyState, ErrorState, LoadingState } from "@/components/shell/states";
import { Button } from "@/components/ui/button";
import { MappingView } from "@/features/mapping/mapping-view";
import { useT } from "@/i18n";

const appNames = { radarr: "Radarr", sonarr: "Sonarr", "": "" } as const;

export function MappingPage() {
  const { id } = useParams({ from: "/jobs/$id/mapping" });
  const { t } = useT();
  const proposal = useProposal(id);

  const back = (
    <p>
      <Link
        to="/jobs/$id"
        params={{ id }}
        className="inline-flex min-h-11 items-center gap-1.5 text-sm underline-offset-4 hover:underline"
      >
        <ArrowLeft aria-hidden="true" className="size-4" />
        {t("mapping.back")}
      </Link>
    </p>
  );

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
      <div className="grid gap-4">
        <h1>{t("mapping.pending.title", { app: appNames[data.app] || "Radarr, Sonarr" })}</h1>
        <p className="text-ink-2">{t("mapping.pending.body")}</p>
        <LoadingState rows={2} />
      </div>
    );
  } else {
    body = (
      <>
        <h1>{t("mapping.title")}</h1>
        {data.target && (
          <p translate="no" className="text-ink-2 [overflow-wrap:anywhere]">
            {t("mapping.target", {
              title: data.target.title,
              year: String(data.target.year),
              app: appNames[data.app],
            })}
          </p>
        )}
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
