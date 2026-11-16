import { Link, useNavigate } from "@tanstack/react-router";
import { useMatchAction } from "@/api/match";
import type { JobDetail } from "@/api/types";
import { ErrorMessage } from "@/components/error-message";
import { Button } from "@/components/ui/button";
import { useT } from "@/i18n";
import { cn } from "@/lib/utils";

const appName = { radarr: "Radarr", sonarr: "Sonarr" } as const;

/**
 * Where the match step starts and where it can be taken back: a `probed` job gets the button
 * to the match page (the manual fallback stays below it); a matched one shows its title and
 * "Choose a different title", which returns the job to `probed` (`DELETE …/match`) and opens the match page.
 */
export function MatchSection({ job, className }: { job: JobDetail; className?: string }) {
  const { t } = useT();
  const undo = useMatchAction(job.id);
  const navigate = useNavigate();

  if (job.state === "probed") {
    return (
      <section className={cn("grid gap-3", className)}>
        <h2>{t("job.match")}</h2>
        <div>
          <Button asChild size="lg">
            <Link to="/jobs/$id/match" params={{ id: job.id }}>
              {t("job.match")}
            </Link>
          </Button>
        </div>
      </section>
    );
  }

  if ((job.state === "matched" || job.state === "awaiting-confirmation") && job.target) {
    return (
      <section className={cn("grid gap-3", className)}>
        <h2>{t("job.target.title")}</h2>
        <p translate="no" className="[overflow-wrap:anywhere]">
          {t("job.target.body", {
            title: job.target.title,
            year: String(job.target.year),
            app: appName[job.target.app],
          })}
        </p>
        <div className="flex flex-wrap gap-2">
          <Button asChild>
            <Link to="/jobs/$id/mapping" params={{ id: job.id }}>
              {t("job.mapping")}
            </Link>
          </Button>
          <Button asChild variant="outline">
            <Link to="/jobs/$id/confirm" params={{ id: job.id }}>
              {t("job.review")}
            </Link>
          </Button>
          <Button
            type="button"
            variant="outline"
            disabled={undo.isPending}
            onClick={() =>
              undo.mutate(null, {
                onSuccess: () => void navigate({ to: "/jobs/$id/match", params: { id: job.id } }),
              })
            }
          >
            {t("job.target.change")}
          </Button>
        </div>
        {undo.isError && <ErrorMessage error={undo.error} />}
      </section>
    );
  }

  return null;
}
