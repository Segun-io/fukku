import { Ban, CircleAlert, CircleCheck, Hourglass, type LucideIcon, Upload } from "lucide-react";
import type { JobState } from "@/api/types";
import { useT } from "@/i18n";
import { type StateTone, stateTone } from "@/lib/job-text";
import { cn } from "@/lib/utils";

const looks: Record<StateTone, { icon: LucideIcon; tone: string }> = {
  moving: { icon: Upload, tone: "text-primary" },
  done: { icon: CircleCheck, tone: "text-primary" },
  waiting: { icon: Hourglass, tone: "text-muted-foreground" },
  error: { icon: CircleAlert, tone: "text-destructive" },
};

/** A job's state in words with the icon of its meaning (docs/design.md, "the four meanings"). */
export function StateBadge({ state, className }: { state: JobState; className?: string }) {
  const { t } = useT();
  const look =
    state === "cancelled" ? { icon: Ban, tone: "text-muted-foreground" } : looks[stateTone(state)];
  const Icon = look.icon;
  return (
    <span
      data-state={state}
      className={cn("inline-flex items-center gap-1.5 text-sm font-medium", look.tone, className)}
    >
      <Icon aria-hidden="true" className="size-4 shrink-0" />
      {t(`state.${state}`)}
    </span>
  );
}
