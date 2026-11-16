import {
  Captions,
  CircleCheck,
  CircleX,
  Clock,
  FileArchive,
  Film,
  type LucideIcon,
} from "lucide-react";
import { useT } from "@/i18n";
import { cn } from "@/lib/utils";

export type FileKind = "video" | "subtitle" | "archive";
export type FileStatus = "pending" | "ok" | "refused";

/** One file of a job, as in `JobDetail.files` (issue #11). */
export type FileListItem = {
  id: string | number;
  name: string;
  kind: FileKind;
  /** Bytes. */
  size: number;
  status: FileStatus;
  /** For a refused file: why, in the user's language (translated from the refusal code). */
  reason?: string;
  /** For a file taken out of an archive: the archive's name. */
  from?: string;
};

const kindIcon: Record<FileKind, LucideIcon> = {
  video: Film,
  subtitle: Captions,
  archive: FileArchive,
};

const statusLook: Record<FileStatus, { icon: LucideIcon; tone: string }> = {
  ok: { icon: CircleCheck, tone: "text-primary" },
  pending: { icon: Clock, tone: "text-muted-foreground" },
  refused: { icon: CircleX, tone: "text-destructive" },
};

/**
 * The file list: what a job contains and what the checks made of each file. A refused
 * file stays in the list, with the reason under its name in words, so the user sees what
 * did not go in and why. It reads top to bottom on a phone and as columns above 40rem.
 */
export function FileList({ files, className }: { files: FileListItem[]; className?: string }) {
  const { t, formatBytes } = useT();
  const anyRefused = files.some((file) => file.status === "refused");

  return (
    <div className={className}>
      <ul aria-label={t("patterns.files.label")} className="divide-y border-y">
        {files.map((file) => {
          const KindIcon = kindIcon[file.kind];
          const look = statusLook[file.status];
          const StatusIcon = look.icon;
          const refused = file.status === "refused";
          return (
            <li
              key={file.id}
              data-status={file.status}
              className="grid grid-cols-[auto_minmax(0,1fr)] gap-x-3 gap-y-1 py-3 [contain-intrinsic-size:auto_5rem] [content-visibility:auto] sm:grid-cols-[auto_minmax(0,1fr)_5.5rem_8.5rem]"
            >
              <KindIcon aria-hidden="true" className="mt-1 size-5 text-muted-foreground" />
              <div className="min-w-0">
                <p
                  translate="no"
                  className={cn("font-medium [overflow-wrap:anywhere]", refused && "text-ink-2")}
                >
                  {file.name}
                </p>
                <p className="flex gap-x-3 text-sm text-muted-foreground">
                  <span className="shrink-0">{t(`patterns.files.kind.${file.kind}`)}</span>
                  {file.from && (
                    // The one name that is cut to a line: the archive is in the list in full,
                    // and a season repeats it on every row.
                    <span translate="no" title={file.from} className="min-w-0 truncate">
                      {t("patterns.files.from", { archive: file.from })}
                    </span>
                  )}
                </p>
              </div>
              <p className="figure col-start-2 text-sm text-ink-2 sm:col-start-3 sm:pt-0.5 sm:text-right">
                {formatBytes(file.size)}
              </p>
              <p
                className={cn(
                  "col-start-2 flex items-center gap-1.5 self-start text-sm font-medium sm:col-start-4 sm:pt-0.5",
                  look.tone,
                )}
              >
                <StatusIcon aria-hidden="true" className="size-4 shrink-0" />
                {t(`patterns.files.status.${file.status}`)}
              </p>
              {refused && file.reason && (
                <p className="col-start-2 mt-1 max-w-[65ch] rounded-md bg-destructive-soft px-3 py-2 text-sm sm:col-end-5">
                  {file.reason}
                </p>
              )}
            </li>
          );
        })}
      </ul>
      {anyRefused && (
        <p className="mt-3 text-sm text-muted-foreground">{t("patterns.files.refusedNote")}</p>
      )}
    </div>
  );
}
