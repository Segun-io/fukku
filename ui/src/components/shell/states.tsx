import { Link } from "@tanstack/react-router";
import { CircleAlert, RotateCw } from "lucide-react";
import type * as React from "react";
import { Button } from "@/components/ui/button";
import { useT } from "@/i18n";
import { cn } from "@/lib/utils";

/** The states every region that loads data has, besides "full": loading, empty, error. */

const headingLook = "text-lg leading-tight tracking-[-0.02em]";

type EmptyStateProps = {
  title: string;
  /** The heading level that fits the page's outline. */
  as?: "h1" | "h2" | "h3";
  /** What belongs here, in one sentence. */
  children?: React.ReactNode;
  /** The one action that fills this place. */
  action?: React.ReactNode;
  className?: string;
};

/** Nothing here yet: a dashed outline where the content will be, and the way to fill it. */
export function EmptyState({
  title,
  as: Heading = "h2",
  children,
  action,
  className,
}: EmptyStateProps) {
  return (
    <div
      className={cn("rounded-xl border border-dashed border-input px-5 py-8 sm:px-8", className)}
    >
      <Heading className={headingLook}>{title}</Heading>
      {children && <p className="mt-2 max-w-[52ch] text-ink-2">{children}</p>}
      {action && <div className="mt-5 flex flex-wrap gap-2">{action}</div>}
    </div>
  );
}

type ErrorStateProps = {
  as?: "h1" | "h2" | "h3";
  /** What broke, in plain words. Defaults to "This page could not load". */
  title?: string;
  /** What to do about it. Defaults to checking the connection. */
  children?: React.ReactNode;
  /** The technical message, for the person who is asked for help. Never the only text. */
  detail?: string;
  onRetry?: () => void;
  className?: string;
};

/** Loading failed: what broke, what to do, and the detail behind a disclosure. */
export function ErrorState({
  title,
  as: Heading = "h2",
  children,
  detail,
  onRetry,
  className,
}: ErrorStateProps) {
  const { t } = useT();
  return (
    <div role="alert" className={cn("grid grid-cols-[auto_minmax(0,1fr)] gap-x-3", className)}>
      <CircleAlert aria-hidden="true" className="mt-1 size-5 text-destructive" />
      <div className="min-w-0">
        <Heading className={headingLook}>{title ?? t("shell.error.title")}</Heading>
        <p className="mt-2 max-w-[52ch] text-ink-2">{children ?? t("shell.error.body")}</p>
        {onRetry && (
          <Button type="button" variant="outline" className="mt-5" onClick={onRetry}>
            <RotateCw aria-hidden="true" />
            {t("shell.error.retry")}
          </Button>
        )}
        {detail && (
          <details className="mt-5 text-sm text-muted-foreground">
            <summary className="inline-flex min-h-11 cursor-pointer items-center rounded-sm underline underline-offset-4 hover:text-foreground">
              {t("shell.error.detail")}
            </summary>
            <p className="font-mono text-sm break-words text-ink-2">{detail}</p>
          </details>
        )}
      </div>
    </div>
  );
}

/** Loading: quiet bars in the shape of the rows that are coming. */
export function LoadingState({ rows = 3, className }: { rows?: number; className?: string }) {
  const { t } = useT();
  return (
    <div role="status" className={cn("grid gap-4", className)}>
      <span className="sr-only">{t("shell.loading")}</span>
      {Array.from({ length: rows }, (_, row) => (
        // biome-ignore lint/suspicious/noArrayIndexKey: identical placeholders, never reordered
        <div key={row} aria-hidden="true" className="grid animate-pulse gap-2.5">
          <div className="h-4 w-3/5 rounded-sm bg-accent" />
          <div className="h-2.5 rounded-full bg-accent" />
          <div className="h-3 w-2/5 rounded-sm bg-accent" />
        </div>
      ))}
    </div>
  );
}

/** No such page. */
export function NotFound() {
  const { t } = useT();
  return (
    <EmptyState
      as="h1"
      title={t("shell.notFound.title")}
      action={
        <Button asChild variant="outline">
          <Link to="/">{t("shell.notFound.action")}</Link>
        </Button>
      }
    >
      {t("shell.notFound.body")}
    </EmptyState>
  );
}
