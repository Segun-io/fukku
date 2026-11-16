import { Link } from "@tanstack/react-router";
import { useJobs } from "@/api/jobs";
import { ErrorMessage } from "@/components/error-message";
import { useT } from "@/i18n";

// Placeholder: the job list is #13. It only proves the data path end to end.
export function JobsPage() {
  const { t, formatBytes } = useT();
  const jobs = useJobs();

  return (
    <section>
      <h1 className="text-xl font-semibold">{t("jobs.title")}</h1>
      {jobs.isPending && <p className="mt-2 text-sm">{t("jobs.loading")}</p>}
      {jobs.isError && <ErrorMessage error={jobs.error} />}
      {jobs.isSuccess && jobs.data.jobs.length === 0 && (
        <p className="mt-2 text-sm">{t("jobs.empty")}</p>
      )}
      {jobs.isSuccess && jobs.data.jobs.length > 0 && (
        <>
          <p className="mt-2 text-sm text-muted-foreground">
            {t("jobs.count", { count: jobs.data.jobs.length })}
          </p>
          <ul className="mt-4 divide-y rounded-md border">
            {jobs.data.jobs.map((job) => (
              <li key={job.id} className="flex flex-wrap items-baseline gap-x-4 px-3 py-2">
                <Link
                  to="/jobs/$id"
                  params={{ id: job.id }}
                  className="font-medium underline-offset-4 hover:underline"
                >
                  {job.name ?? t("jobs.unnamed")}
                </Link>
                <span className="text-sm">{t(`state.${job.state}`)}</span>
                <span className="text-sm text-muted-foreground">
                  {t("jobs.files", { count: job.fileCount })}, {formatBytes(job.totalSize)}
                </span>
              </li>
            ))}
          </ul>
        </>
      )}
    </section>
  );
}
