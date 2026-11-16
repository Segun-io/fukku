import { Ban, Check, CircleAlert, Hourglass } from "lucide-react";
import type * as React from "react";
import { useT } from "@/i18n";
import { cn } from "@/lib/utils";

/** The states a job passes through on its way to done, in order (plan §9, issue #1). */
export const jobPath = [
  "uploading",
  "uploaded",
  "probing",
  "probed",
  "matched",
  "awaiting-confirmation",
  "importing",
  "imported",
  "post-processing",
  "done",
] as const;

export type PathState = (typeof jobPath)[number];
/** The three states off the path: failed needs a person, stalled resumes by itself. */
export type OffPathState = "failed" | "stalled" | "cancelled";
export type JobState = PathState | OffPathState;

/** One entry of `JobDetail.history` (issue #11). */
export type JobEvent = { at: string; from: JobState | null; to: JobState };

function isPathState(state: JobState | null): state is PathState {
  return state !== null && (jobPath as readonly string[]).includes(state);
}

/**
 * Where on the path a job stands. A failed, stalled or cancelled job stands at the step it
 * was in when that happened: the `from` of its last event.
 */
export function standingAt(state: JobState, history: readonly JobEvent[]): PathState {
  if (isPathState(state)) return state;
  for (let i = history.length - 1; i >= 0; i--) {
    const event = history[i];
    if (event && event.to === state && isPathState(event.from)) return event.from;
  }
  for (let i = history.length - 1; i >= 0; i--) {
    const event = history[i];
    if (event && isPathState(event.to)) return event.to;
  }
  return "uploading";
}

export type StatusTimelineProps = {
  /** `job.state`. */
  state: JobState;
  /** `jobDetail.history`, oldest first. */
  history: readonly JobEvent[];
  /**
   * The steps to show. The default is the whole path; a page for a milestone that ends
   * earlier passes the part it has, for example `jobPath.slice(0, 4)`.
   */
  steps?: readonly PathState[];
  /** For a failed or stalled job: what happened, translated from `job.error.code`. */
  detail?: React.ReactNode;
  /** For a failed or stalled job: the buttons that `job.actions` allows. */
  actions?: React.ReactNode;
  className?: string;
};

/**
 * The status timeline: every step of a job from top to bottom, with the time each one was
 * reached. Done steps carry a tick, the current one is marked "now", the rest wait. A
 * failed, stalled or cancelled job shows that at the step where it happened, in words.
 */
export function StatusTimeline({
  state,
  history,
  steps = jobPath,
  detail,
  actions,
  className,
}: StatusTimelineProps) {
  const { t, formatDate } = useT();
  const off: OffPathState | null = isPathState(state) ? null : state;
  const at = standingAt(state, history);
  const currentIndex = Math.max(0, steps.indexOf(at));
  const finished = state === "done";
  // What will never happen is not listed.
  const shown = off === "cancelled" ? steps.slice(0, currentIndex + 1) : steps;

  const reachedAt = new Map<JobState, string>();
  for (const event of history) reachedAt.set(event.to, event.at);

  return (
    <ol aria-label={t("patterns.timeline.label")} className={cn("grid", className)}>
      {shown.map((step, index) => {
        const phase =
          index < currentIndex || finished
            ? "done"
            : index === currentIndex
              ? "current"
              : "upcoming";
        const isCurrent = phase === "current";
        const last = index === shown.length - 1;
        const time = phase === "upcoming" ? undefined : reachedAt.get(step);
        const offHere = isCurrent ? off : null;
        const offTime = offHere ? reachedAt.get(offHere) : undefined;

        return (
          <li
            key={step}
            aria-current={isCurrent ? "step" : undefined}
            className="grid grid-cols-[1.5rem_minmax(0,1fr)] gap-x-3"
          >
            <div className="flex flex-col items-center">
              <Marker phase={phase} off={offHere} />
              {!last && (
                <span
                  aria-hidden="true"
                  className={cn(
                    "w-0.5 flex-1 rounded-full",
                    phase === "done" ? "bg-primary" : "bg-border",
                  )}
                />
              )}
            </div>

            <div className={cn("min-w-0", last ? "pb-0" : "pb-5")}>
              <p className="flex flex-wrap items-baseline justify-between gap-x-4">
                <span
                  className={cn(
                    phase === "upcoming" && "text-muted-foreground",
                    isCurrent && "font-bold",
                  )}
                >
                  <span className="sr-only">{t(`patterns.timeline.step.${phase}`)}: </span>
                  {t(`patterns.state.${step}`)}
                </span>
                {time && (
                  <time dateTime={time} className="figure text-sm text-muted-foreground">
                    {formatDate(time, "short")}
                  </time>
                )}
              </p>

              {offHere && (
                <div
                  role={offHere === "failed" ? "alert" : "status"}
                  className={cn(
                    "mt-2 rounded-lg border p-3 text-sm",
                    offHere === "failed"
                      ? "border-destructive bg-destructive-soft"
                      : "border-border bg-muted",
                  )}
                >
                  <p className="flex flex-wrap items-baseline justify-between gap-x-4 font-bold">
                    <span>{t(`state.${offHere}`)}</span>
                    {offTime && (
                      <time
                        dateTime={offTime}
                        className="figure text-sm font-normal text-muted-foreground"
                      >
                        {formatDate(offTime, "short")}
                      </time>
                    )}
                  </p>
                  <p className="mt-1 max-w-[65ch] text-ink-2">
                    {t(`patterns.timeline.${offHere}`)}
                  </p>
                  {detail && <p className="mt-1 max-w-[65ch] text-ink-2">{detail}</p>}
                  {actions && <div className="mt-3 flex flex-wrap gap-2">{actions}</div>}
                </div>
              )}
            </div>
          </li>
        );
      })}
    </ol>
  );
}

function Marker({
  phase,
  off,
}: {
  phase: "done" | "current" | "upcoming";
  off: OffPathState | null;
}) {
  const base = "flex size-6 shrink-0 items-center justify-center rounded-full";
  if (off === "failed") {
    return (
      <span aria-hidden="true" className={cn(base, "bg-destructive text-destructive-foreground")}>
        <CircleAlert className="size-4" />
      </span>
    );
  }
  if (off === "stalled" || off === "cancelled") {
    const Icon = off === "stalled" ? Hourglass : Ban;
    return (
      <span aria-hidden="true" className={cn(base, "bg-accent text-muted-foreground")}>
        <Icon className="size-3.5" />
      </span>
    );
  }
  if (phase === "done") {
    return (
      <span aria-hidden="true" className={cn(base, "bg-primary text-primary-foreground")}>
        <Check className="size-4" strokeWidth={3} />
      </span>
    );
  }
  if (phase === "current") {
    return (
      <span aria-hidden="true" className={cn(base, "border-2 border-primary bg-card")}>
        <span className="size-2.5 rounded-full bg-primary" />
      </span>
    );
  }
  return <span aria-hidden="true" className={cn(base, "border-2 border-dashed border-input")} />;
}
