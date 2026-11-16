import { Link, useParams } from "@tanstack/react-router";
import { useJob } from "@/api/jobs";
import { ErrorMessage } from "@/components/error-message";
import { useT } from "@/i18n";

// Placeholder: the job detail is #13.
export function JobDetailPage() {
  const { id } = useParams({ from: "/jobs/$id" });
  const { t, formatBytes, formatDate } = useT();
  const job = useJob(id);

  return (
    <section>
      <p className="text-sm">
        <Link to="/jobs" className="underline-offset-4 hover:underline">
          {t("job.back")}
        </Link>
      </p>
      <h1 className="mt-2 text-xl font-semibold">{job.data?.name ?? t("job.title")}</h1>
      {job.isPending && <p className="mt-2 text-sm">{t("job.loading")}</p>}
      {job.isError && <ErrorMessage error={job.error} />}
      {job.isSuccess && (
        <div className="mt-2 space-y-1 text-sm">
          <p>
            {t("job.state")}: {t(`state.${job.data.state}`)}
          </p>
          <p>{t("job.size", { size: formatBytes(job.data.totalSize) })}</p>
          <p>{t("job.updated", { date: formatDate(job.data.updatedAt) })}</p>
          <h2 className="pt-2 font-medium">{t("job.files")}</h2>
          <ul>
            {job.data.files.map((f) => (
              <li key={f.id}>
                {f.name} ({formatBytes(f.size)})
              </li>
            ))}
          </ul>
        </div>
      )}
    </section>
  );
}
