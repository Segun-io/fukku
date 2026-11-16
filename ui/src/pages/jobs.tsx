import { Link } from "@tanstack/react-router";
import { useState } from "react";
import { useJobs } from "@/api/jobs";
import { useSession } from "@/api/session";
import { type JobState, jobStates } from "@/api/types";
import { errorKey } from "@/components/error-message";
import { StateBadge } from "@/components/jobs/state-badge";
import { SelectField } from "@/components/patterns/select-field";
import { EmptyState, ErrorState, LoadingState } from "@/components/shell/states";
import { Button } from "@/components/ui/button";
import { useT } from "@/i18n";
import { formatAge, listFilter } from "@/lib/job-text";

const pageSize = 50;

/**
 * The list of uploads: what each one is, where it stands, and when it will be deleted.
 *
 * A row is the upload's name, its state in words, then its figures. On a phone they read
 * in that order; above 40rem the state stands at the right edge, so the states of all
 * rows can be read down one column. The whole row opens the upload.
 */
export function JobsPage() {
  const { t, lang, formatBytes, formatDate } = useT();
  const session = useSession();
  const admin = session.data?.user.admin === true;

  const [state, setState] = useState<JobState | "">("");
  const [mine, setMine] = useState(false);
  const [limit, setLimit] = useState(pageSize);
  const jobs = useJobs(listFilter(state, admin && mine, limit));
  const filtered = state !== "";
  const now = new Date();

  return (
    <section className="grid gap-6">
      <h1>{t("jobs.title")}</h1>

      <div className="flex flex-wrap items-end gap-x-4 gap-y-3">
        <SelectField
          label={t("jobs.filter.state")}
          name="state"
          value={state}
          onChange={(e) => {
            setState(e.target.value as JobState | "");
            setLimit(pageSize);
          }}
          className="max-sm:flex-1"
        >
          <option value="">{t("jobs.filter.allStates")}</option>
          {jobStates.map((s) => (
            <option key={s} value={s}>
              {t(`state.${s}`)}
            </option>
          ))}
        </SelectField>
        {admin && (
          <SelectField
            label={t("jobs.filter.scope")}
            name="scope"
            value={mine ? "mine" : "everyone"}
            onChange={(e) => {
              setMine(e.target.value === "mine");
              setLimit(pageSize);
            }}
            className="max-sm:flex-1"
          >
            <option value="everyone">{t("jobs.scope.everyone")}</option>
            <option value="mine">{t("jobs.scope.mine")}</option>
          </SelectField>
        )}
      </div>

      {jobs.isPending && <LoadingState />}
      {jobs.isError && (
        <ErrorState title={t("jobs.error.title")} onRetry={() => void jobs.refetch()}>
          {t(errorKey(jobs.error))}
        </ErrorState>
      )}

      {jobs.isSuccess && jobs.data.jobs.length === 0 && !filtered && (
        <EmptyState
          title={t("jobs.empty.title")}
          action={
            <Button asChild>
              <Link to="/upload">{t("jobs.empty.action")}</Link>
            </Button>
          }
        >
          {t("jobs.empty.body")}
        </EmptyState>
      )}
      {jobs.isSuccess && jobs.data.jobs.length === 0 && filtered && (
        <EmptyState title={t("jobs.emptyFiltered.title")}>
          {t("jobs.emptyFiltered.body")}
        </EmptyState>
      )}

      {jobs.isSuccess && jobs.data.jobs.length > 0 && (
        <div>
          <p className="text-sm text-muted-foreground" aria-live="polite">
            {t("jobs.count", { count: jobs.data.jobs.length })}
          </p>
          <ul aria-label={t("jobs.list")} className="mt-2 divide-y border-y">
            {jobs.data.jobs.map((job) => (
              <li
                key={job.id}
                className="group relative grid grid-cols-1 gap-x-6 gap-y-1.5 py-4 [contain-intrinsic-size:auto_5.5rem] [content-visibility:auto] has-[a:focus-visible]:outline-2 has-[a:focus-visible]:outline-offset-2 has-[a:focus-visible]:outline-ring sm:grid-cols-[minmax(0,1fr)_auto] sm:items-baseline"
              >
                <Link
                  to="/jobs/$id"
                  params={{ id: job.id }}
                  translate="no"
                  className="font-medium no-underline underline-offset-4 [overflow-wrap:anywhere] after:absolute after:inset-0 group-hover:underline focus-visible:outline-none"
                >
                  {job.name ?? t("jobs.unnamed")}
                </Link>
                <StateBadge
                  state={job.state}
                  className="sm:col-start-2 sm:row-start-1 sm:justify-self-end"
                />
                <p className="flex flex-wrap items-baseline gap-x-4 gap-y-1 text-sm text-muted-foreground sm:col-start-1">
                  <span className="figure">
                    {t("jobs.files", { count: job.fileCount })}, {formatBytes(job.totalSize)}
                  </span>
                  {admin && <span translate="no">{t("jobs.owner", { owner: job.owner })}</span>}
                  <span>{t("jobs.started", { age: formatAge(job.createdAt, now, lang) })}</span>
                  {job.expiresAt && (
                    <span>{t("jobs.expires", { date: formatDate(job.expiresAt) })}</span>
                  )}
                </p>
              </li>
            ))}
          </ul>
          {jobs.data.jobs.length >= limit && (
            <Button
              type="button"
              variant="outline"
              className="mt-4"
              onClick={() => setLimit(limit + pageSize)}
            >
              {t("jobs.more")}
            </Button>
          )}
        </div>
      )}
    </section>
  );
}
