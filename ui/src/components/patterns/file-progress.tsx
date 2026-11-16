import {
  CircleAlert,
  CircleCheck,
  Clock,
  type LucideIcon,
  Pause,
  Play,
  RotateCw,
  Upload,
  WifiOff,
  X,
} from "lucide-react";
import type * as React from "react";
import { Button } from "@/components/ui/button";
import { type TKey, useT } from "@/i18n";
import { cn } from "@/lib/utils";

/**
 * Where one file's upload stands.
 *
 *   queued        chosen, waiting for one of the parallel slots
 *   uploading     bytes are moving
 *   paused        the user paused it
 *   reconnecting  the connection dropped; it continues by itself
 *   failed        the server refused it or gave up; it needs the user
 *   done          every byte is on the server
 */
export type UploadStatus = "queued" | "uploading" | "paused" | "reconnecting" | "failed" | "done";

export type FileProgressProps = {
  name: string;
  /** The file's size in bytes. */
  size: number;
  /** Bytes the server has confirmed. */
  sent: number;
  status: UploadStatus;
  /** While uploading: the current speed in bytes per second. */
  bytesPerSecond?: number;
  /** While uploading: the estimated seconds left. */
  secondsLeft?: number;
  /**
   * One more sentence under the figures, already translated: why it failed, or one of
   * `patterns.progress.hint.slow` and `patterns.progress.hint.resumed`.
   */
  note?: React.ReactNode;
  onPause?: () => void;
  onResume?: () => void;
  onRetry?: () => void;
  onRemove?: () => void;
  className?: string;
};

/**
 * A time left, in milliseconds, rounded so that it does not flicker on every update: to five
 * minutes above an hour, to a minute above a minute.
 */
function steady(seconds: number): number {
  const s = Math.max(0, seconds);
  const step = s >= 3600 ? 300 : s >= 60 ? 60 : 1;
  return Math.round(s / step) * step * 1000;
}

const statusLook: Record<
  UploadStatus,
  { icon: LucideIcon; tone: string; tray: "moving" | "waiting" | "failed" }
> = {
  queued: { icon: Clock, tone: "text-muted-foreground", tray: "waiting" },
  uploading: { icon: Upload, tone: "text-primary", tray: "moving" },
  paused: { icon: Pause, tone: "text-muted-foreground", tray: "waiting" },
  reconnecting: { icon: WifiOff, tone: "text-muted-foreground", tray: "waiting" },
  failed: { icon: CircleAlert, tone: "text-destructive", tray: "failed" },
  done: { icon: CircleCheck, tone: "text-primary", tray: "moving" },
};

/**
 * The file label: one file of an upload, its name over its figures and the tray it fills.
 *
 * Nothing moves while the numbers change. The figures are set in the figure face with
 * fixed-width digits and a fixed number of decimals, each one has its own place, and the
 * speed line keeps its height while a file uploads, with or without a speed to show. Render the rows inside
 * <FileProgressList>.
 */
export function FileProgress({
  name,
  size,
  sent,
  status,
  bytesPerSecond,
  secondsLeft,
  note,
  onPause,
  onResume,
  onRetry,
  onRemove,
  className,
}: FileProgressProps) {
  const { t, formatBytes, formatRate, formatDuration, formatPercent } = useT();
  const look = statusLook[status];
  const Icon = look.icon;
  const fraction = size > 0 ? Math.min(1, Math.max(0, sent / size)) : 0;
  const percent = formatPercent(fraction);
  const sentText = formatBytes(sent, size);

  let second: React.ReactNode = null;
  if (status === "uploading") {
    second = (
      <>
        <span className="figure">
          {bytesPerSecond === undefined ? "" : formatRate(bytesPerSecond)}
        </span>
        <span className="figure text-right">
          {secondsLeft === undefined
            ? ""
            : t("patterns.progress.left", { time: formatDuration(steady(secondsLeft)) })}
        </span>
      </>
    );
  } else if (status === "paused") {
    second = <span>{t("patterns.progress.hint.paused", { sent: sentText })}</span>;
  } else if (status === "reconnecting") {
    second = <span>{t("patterns.progress.hint.reconnecting", { sent: sentText })}</span>;
  } else if (status === "failed" && note) {
    second = <span className="text-destructive">{note}</span>;
  }

  return (
    <li
      data-status={status}
      className={cn(
        "grid grid-cols-[minmax(0,1fr)_auto] items-start gap-x-4 gap-y-2.5 px-4 py-4 sm:px-5",
        className,
      )}
    >
      <div className="col-span-2 min-w-0 sm:col-span-1">
        <p translate="no" className="font-medium [overflow-wrap:anywhere]">
          {name}
        </p>
        <p aria-live="polite" className={cn("mt-0.5 flex items-center gap-1.5 text-sm", look.tone)}>
          <Icon aria-hidden="true" className="size-4 shrink-0" />
          <span className="font-medium">{t(`patterns.progress.status.${status}`)}</span>
        </p>
      </div>

      <div
        role="progressbar"
        aria-label={name}
        aria-valuemin={0}
        aria-valuemax={100}
        aria-valuenow={Math.round(fraction * 100)}
        aria-valuetext={t("patterns.progress.valueText", { percent })}
        data-state={look.tray}
        className="tray col-span-2"
        style={{ "--filled": fraction * 100 } as React.CSSProperties}
      >
        <span />
      </div>

      <p className="col-span-2 flex items-baseline justify-between gap-4 text-sm">
        <span className="figure">
          {t("patterns.progress.of", { sent: sentText, total: formatBytes(size, size) })}
        </span>
        <span className="figure min-w-[5ch] text-right font-medium">{percent}</span>
      </p>

      {second && (
        <p
          className="col-span-2 flex min-h-[1lh] max-w-[65ch] items-baseline justify-between gap-4 text-sm text-muted-foreground data-[figures]:max-w-none"
          data-figures={status === "uploading" ? "" : undefined}
        >
          {second}
        </p>
      )}

      {note && status !== "failed" && (
        <p className="col-span-2 max-w-[65ch] text-sm text-ink-2">{note}</p>
      )}

      <RowActions
        name={name}
        status={status}
        onPause={onPause}
        onResume={onResume}
        onRetry={onRetry}
        onRemove={onRemove}
      />
    </li>
  );
}

type RowActionsProps = Pick<
  FileProgressProps,
  "name" | "status" | "onPause" | "onResume" | "onRetry" | "onRemove"
>;

/** The one action the state allows, then Remove. Last in the row on a phone, top right above 40rem. */
function RowActions({ name, status, onPause, onResume, onRetry, onRemove }: RowActionsProps) {
  const { t } = useT();
  const main: { key: TKey; label: TKey; icon: LucideIcon; run: () => void } | null =
    status === "uploading" && onPause
      ? {
          key: "patterns.progress.pause",
          label: "patterns.progress.pause.label",
          icon: Pause,
          run: onPause,
        }
      : status === "paused" && onResume
        ? {
            key: "patterns.progress.resume",
            label: "patterns.progress.resume.label",
            icon: Play,
            run: onResume,
          }
        : status === "failed" && onRetry
          ? {
              key: "patterns.progress.retry",
              label: "patterns.progress.retry.label",
              icon: RotateCw,
              run: onRetry,
            }
          : null;
  const removable = onRemove && status !== "done";
  if (!main && !removable) return null;
  const MainIcon = main?.icon;

  return (
    <div className="col-span-2 flex flex-wrap gap-2 max-sm:[&>[data-variant=ghost]:first-child]:-ml-2.5 sm:col-span-1 sm:col-start-2 sm:row-start-1 sm:justify-end">
      {main && MainIcon && (
        <Button
          type="button"
          variant="outline"
          size="sm"
          aria-label={t(main.label, { name })}
          onClick={main.run}
        >
          <MainIcon aria-hidden="true" />
          {t(main.key)}
        </Button>
      )}
      {removable && (
        <Button
          type="button"
          variant="ghost"
          size="sm"
          aria-label={t("patterns.progress.remove.label", { name })}
          onClick={onRemove}
        >
          <X aria-hidden="true" />
          {t("patterns.progress.remove")}
        </Button>
      )}
    </div>
  );
}

/** The strip of file labels: one raised surface, the rows torn apart by dashed rules. */
export function FileProgressList({ className, ...props }: React.ComponentProps<"ul">) {
  return (
    <ul
      className={cn(
        "divide-y divide-dashed divide-input rounded-xl border bg-card shadow-sm",
        className,
      )}
      {...props}
    />
  );
}
