import { useNavigate } from "@tanstack/react-router";
import { useState } from "react";
import { type JobMutation, useJobAction } from "@/api/jobs";
import type { Job } from "@/api/types";
import { ErrorMessage } from "@/components/error-message";
import { ConfirmStep } from "@/components/patterns/confirm-step";
import { Button } from "@/components/ui/button";
import { useT } from "@/i18n";
import { visibleActions } from "@/lib/job-text";

/**
 * The buttons for what `job.actions` allows (cancel, retry, delete). Each one opens a
 * confirmation in place of the buttons; cancelling and deleting carry a warning that has
 * to be ticked, because they cannot be undone. Cancelling a job whose import may still be
 * running in the app (`importMayRun`; only an admin can) also says that the app may still
 * finish it.
 */
export function JobActions({ job, importMayRun = false }: { job: Job; importMayRun?: boolean }) {
  const { t, formatBytes } = useT();
  const navigate = useNavigate();
  const run = useJobAction(job.id);
  const [asking, setAsking] = useState<JobMutation | null>(null);
  const available = visibleActions(job.actions);
  if (available.length === 0) return null;

  function leave() {
    setAsking(null);
    run.reset();
  }

  if (asking && available.includes(asking)) {
    const warnings =
      asking === "cancel"
        ? [
            t("job.confirm.cancel.warning"),
            ...(importMayRun ? [t("job.confirm.cancel.importWarning")] : []),
          ]
        : asking === "delete"
          ? [t("job.confirm.delete.warning")]
          : [];
    return (
      <div className="grid gap-3">
        <ConfirmStep
          title={t(`job.confirm.${asking}.title`)}
          facts={[
            {
              term: t("job.confirm.upload"),
              value: <span translate="no">{job.name ?? t("jobs.unnamed")}</span>,
            },
            {
              term: t("job.confirm.files"),
              value: t("jobs.files", { count: job.fileCount }),
              figure: true,
            },
            { term: t("job.confirm.size"), value: formatBytes(job.totalSize), figure: true },
          ]}
          warnings={warnings}
          confirmLabel={t(`job.action.${asking}`)}
          busy={run.isPending}
          onConfirm={() =>
            run.mutate(asking, {
              onSuccess: () => {
                if (asking === "delete") void navigate({ to: "/jobs", replace: true });
                else leave();
              },
            })
          }
          cancelLabel={t("job.confirm.back")}
          onCancel={leave}
        />
        {run.isError && <ErrorMessage error={run.error} />}
      </div>
    );
  }

  return (
    <section aria-label={t("job.actions")} className="flex flex-wrap gap-2">
      {available.map((action) => (
        <Button
          key={action}
          type="button"
          variant={action === "retry" ? "default" : "outline"}
          onClick={() => setAsking(action)}
        >
          {t(`job.action.${action}`)}
        </Button>
      ))}
    </section>
  );
}
