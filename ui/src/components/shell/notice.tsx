import type { LucideIcon } from "lucide-react";
import type * as React from "react";
import { cn } from "@/lib/utils";

/**
 * The four tones a message can take. Colour never carries the meaning alone: every notice
 * has an icon and says in words what happened.
 *
 *   waiting  grey    something is paused or waiting and continues by itself
 *   caution  yellow  read this before you decide
 *   error    red     it stopped and needs a person
 *   done     green   it worked
 */
export type NoticeTone = "waiting" | "caution" | "error" | "done";

const tones: Record<NoticeTone, { box: string; icon: string }> = {
  waiting: { box: "border-border bg-muted", icon: "text-muted-foreground" },
  caution: { box: "border-caution-edge bg-caution", icon: "text-caution-foreground" },
  error: { box: "border-destructive bg-destructive-soft", icon: "text-destructive" },
  done: { box: "border-primary bg-primary-soft", icon: "text-primary" },
};

type NoticeProps = Omit<React.ComponentProps<"div">, "title"> & {
  tone: NoticeTone;
  icon: LucideIcon;
  title: React.ReactNode;
  /** Set when the notice opens a part of the page and its title belongs in the outline. */
  titleAs?: "p" | "h2" | "h3" | "h4";
  /** One or two buttons or links, shown under the text. */
  action?: React.ReactNode;
};

/** A message inside a page: an icon, a title in one line of plain words, and what to do. */
export function Notice({
  tone,
  icon: Icon,
  title,
  titleAs: Title = "p",
  action,
  children,
  className,
  ...props
}: NoticeProps) {
  const style = tones[tone];
  return (
    <div
      data-tone={tone}
      className={cn(
        "grid grid-cols-[auto_minmax(0,1fr)] gap-x-3 rounded-lg border p-4 text-foreground",
        style.box,
        className,
      )}
      {...props}
    >
      <Icon aria-hidden="true" className={cn("mt-0.5 size-5", style.icon)} />
      <div className="min-w-0">
        <Title className="font-sans text-base leading-[1.55] font-bold tracking-normal">
          {title}
        </Title>
        {children && <div className="mt-1 max-w-[65ch] text-sm text-ink-2">{children}</div>}
        {action && <div className="mt-3 flex flex-wrap items-center gap-2">{action}</div>}
      </div>
    </div>
  );
}
