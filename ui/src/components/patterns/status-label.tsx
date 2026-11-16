import type { LucideIcon } from "lucide-react";
import { CircleAlert, CircleCheck, Clock, TriangleAlert } from "lucide-react";
import type * as React from "react";
import { cn } from "@/lib/utils";

/** The four meanings of docs/design.md, section 3, as one line of words with their icon. */
export type StatusTone = "done" | "waiting" | "caution" | "error";

const looks: Record<StatusTone, { icon: LucideIcon; tone: string; words: string }> = {
  done: { icon: CircleCheck, tone: "text-primary", words: "text-foreground" },
  waiting: { icon: Clock, tone: "text-muted-foreground", words: "text-muted-foreground" },
  caution: { icon: TriangleAlert, tone: "text-caution-edge", words: "text-foreground" },
  error: { icon: CircleAlert, tone: "text-destructive", words: "text-destructive" },
};

type StatusLabelProps = Omit<React.ComponentProps<"span">, "children"> & {
  tone: StatusTone;
  /** Another icon of the same meaning: `Ban` for skipped, `Hourglass` for waiting on an app. */
  icon?: LucideIcon;
  /** The state in words. The icon repeats it; it never stands alone. */
  children: React.ReactNode;
};

/**
 * The state of one row in words, with the icon of its meaning: a file that is ready, one
 * that waits, one to read about, one that was refused. Put it in the same place in every
 * row, so that the states can be read down one column. For a job's state use `StateBadge`.
 */
export function StatusLabel({ tone, icon, children, className, ...props }: StatusLabelProps) {
  const look = looks[tone];
  const Icon = icon ?? look.icon;
  return (
    <span
      data-tone={tone}
      className={cn("inline-flex items-start gap-1.5 text-sm font-medium", look.words, className)}
      {...props}
    >
      <Icon aria-hidden="true" className={cn("mt-0.5 size-4 shrink-0", look.tone)} />
      <span className="min-w-0">{children}</span>
    </span>
  );
}
