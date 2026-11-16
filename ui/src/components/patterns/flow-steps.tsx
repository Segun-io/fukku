import { Check } from "lucide-react";
import { useT } from "@/i18n";
import { cn } from "@/lib/utils";

/** The decisions between "the files are checked" and "it is in the library", in order. */
export const flowSteps = ["match", "mapping", "confirm"] as const;
export type FlowStep = (typeof flowSteps)[number];

/**
 * Where the user is in the three decisions of an import: choose the title, check the files,
 * confirm. It only says where they are: the way on is the page's one green button and the
 * way back is the link above. The markers are the ones of the status timeline. On a phone
 * only the current step is written out; the others are read out, not shown.
 */
export function FlowSteps({ current, className }: { current: FlowStep; className?: string }) {
  const { t } = useT();
  const at = flowSteps.indexOf(current);
  return (
    <ol
      aria-label={t("patterns.flow.label")}
      className={cn("flex flex-wrap items-center gap-x-2 gap-y-1 text-sm", className)}
    >
      {flowSteps.map((step, index) => {
        const phase = index < at ? "done" : index === at ? "current" : "upcoming";
        return (
          <li
            key={step}
            aria-current={phase === "current" ? "step" : undefined}
            className="flex items-center gap-2"
          >
            {index > 0 && (
              <span
                aria-hidden="true"
                className={cn(
                  "h-0.5 w-4 rounded-full sm:w-6",
                  phase === "upcoming" ? "bg-border" : "bg-primary",
                )}
              />
            )}
            <span
              aria-hidden="true"
              className={cn(
                "flex size-6 shrink-0 items-center justify-center rounded-full",
                phase === "done" && "bg-primary text-primary-foreground",
                phase === "current" && "border-2 border-primary bg-card",
                phase === "upcoming" && "border-2 border-dashed border-input",
              )}
            >
              {phase === "done" && <Check className="size-4" strokeWidth={3} />}
              {phase === "current" && <span className="size-2.5 rounded-full bg-primary" />}
            </span>
            <span
              className={cn(
                phase === "current" ? "font-bold" : "max-sm:sr-only",
                phase === "upcoming" && "text-muted-foreground",
              )}
            >
              <span className="sr-only">{t(`patterns.timeline.step.${phase}`)}: </span>
              {t(`patterns.flow.step.${step}`)}
            </span>
          </li>
        );
      })}
    </ol>
  );
}
