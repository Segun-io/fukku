import { useNavigate } from "@tanstack/react-router";
import { CircleAlert, FilePlus2, Info, TriangleAlert } from "lucide-react";
import { useId, useRef, useState } from "react";
import { isKnownErrorCode, type Session } from "@/api/types";
import { ErrorMessage } from "@/components/error-message";
import { FileProgress, FileProgressList } from "@/components/patterns/file-progress";
import { SignInAgain } from "@/components/shell/banners";
import { Notice } from "@/components/shell/notice";
import { Button } from "@/components/ui/button";
import { type TFunction, useT } from "@/i18n";
import { cn } from "@/lib/utils";
import { acceptedExtensions, awaitingFiles, type Entry, secondsLeft, showLargeHint } from "./queue";
import { useUpload } from "./use-upload";

/** The reason a file failed or was refused, in the user's language. */
function reasonText(t: TFunction, code: string | undefined): string {
  if (code === "file_mismatch") return t("upload.reason.file_mismatch");
  if (code && isKnownErrorCode(code)) return t(`errors.${code}`);
  return t("errors.unknown");
}

type PickerProps = { accept: string; onFiles: (files: File[]) => void; disabled?: boolean };

/** Choose files with the picker or drop them here. */
function Picker({ accept, onFiles, disabled }: PickerProps) {
  const { t } = useT();
  const inputId = useId();
  const input = useRef<HTMLInputElement>(null);
  const [over, setOver] = useState(false);

  const take = (list: FileList | null) => {
    if (list && list.length > 0) onFiles([...list]);
    if (input.current) input.current.value = "";
  };

  return (
    <section
      aria-labelledby={`${inputId}-title`}
      onDragOver={(e) => {
        if (disabled) return;
        e.preventDefault();
        setOver(true);
      }}
      onDragLeave={() => setOver(false)}
      onDrop={(e) => {
        e.preventDefault();
        setOver(false);
        if (!disabled) take(e.dataTransfer.files);
      }}
      className={cn(
        "rounded-xl border border-dashed border-input px-5 py-8 text-center sm:px-8",
        over && "border-primary bg-primary-soft",
      )}
    >
      <h2 id={`${inputId}-title`} className="text-lg leading-tight tracking-[-0.02em]">
        {t("upload.drop.title")}
      </h2>
      <p className="mx-auto mt-2 max-w-[52ch] text-ink-2">{t("upload.drop.hint")}</p>
      <div className="mt-5 flex justify-center">
        <label
          htmlFor={inputId}
          className={cn(
            "inline-flex h-10 cursor-pointer items-center justify-center gap-2 rounded-md bg-primary px-6 text-sm font-medium text-primary-foreground hover:bg-primary/90 has-[:focus-visible]:ring-[3px] has-[:focus-visible]:ring-ring/50",
            disabled && "pointer-events-none opacity-50",
          )}
        >
          <FilePlus2 aria-hidden="true" className="size-4" />
          {t("upload.choose")}
          <input
            ref={input}
            id={inputId}
            type="file"
            multiple
            accept={accept}
            disabled={disabled}
            className="sr-only"
            onChange={(e) => take(e.target.files)}
          />
        </label>
      </div>
      <p className="mt-4 text-sm text-muted-foreground">
        {t("upload.drop.accepted", { types: accept.split(",").join(" ") })}
      </p>
    </section>
  );
}

function rowFor(
  entry: Entry,
  up: ReturnType<typeof useUpload>,
  { t, formatBytes }: Pick<ReturnType<typeof useT>, "t" | "formatBytes">,
  confirmRemove: (entry: Entry) => boolean,
) {
  const { state } = up;
  const choosing = state.phase === "choosing";
  const status = entry.status === "awaiting" ? "paused" : entry.status;
  const note =
    entry.status === "failed"
      ? reasonText(t, entry.code)
      : entry.status === "uploading" && entry.resumedFrom
        ? t("patterns.progress.hint.resumed", { sent: formatBytes(entry.resumedFrom, entry.size) })
        : undefined;
  return (
    <FileProgress
      key={entry.key}
      name={entry.name}
      size={entry.size}
      sent={entry.sent}
      status={status}
      bytesPerSecond={entry.speed}
      secondsLeft={secondsLeft(entry)}
      note={note}
      onPause={choosing ? undefined : () => up.pause(entry.key)}
      onResume={() => up.resume(entry.key)}
      onRetry={() => up.retry(entry.key)}
      onRemove={() => {
        if (confirmRemove(entry)) up.remove(entry.key);
      }}
    />
  );
}

/** The upload page, once the session (and so the server's rules) is known. */
export function UploadView({ config }: { config: Session["upload"] }) {
  const { t, formatBytes } = useT();
  const navigate = useNavigate();
  const up = useUpload(config, (jobId) => {
    void navigate({ to: "/jobs/$id", params: { id: jobId } });
  });
  const { state } = up;
  const accept = acceptedExtensions(state.rules).join(",");
  const awaiting = awaitingFiles(state);
  const busy = state.phase === "submitting" || state.phase === "submitted";
  const done = state.entries.filter((e) => e.status === "done").length;

  const confirmRemove = (entry: Entry) =>
    entry.sent === 0 || window.confirm(t("upload.remove.confirm", { name: entry.name }));

  if (up.restoring) {
    return <p role="status">{t("upload.restoring")}</p>;
  }

  return (
    <div className="grid gap-6">
      <p className="max-w-[65ch] text-ink-2">{t("upload.intro")}</p>

      {state.signedOut && <SignInAgain />}

      {state.jobError && (
        <Notice
          role="alert"
          tone="error"
          icon={CircleAlert}
          title={t("upload.jobGone.title")}
          action={
            <Button type="button" variant="outline" onClick={() => void up.discard()}>
              {t("upload.startOver")}
            </Button>
          }
        >
          {t("upload.jobGone.body", { reason: reasonText(t, state.jobError) })}
        </Notice>
      )}

      {awaiting.length > 0 && (
        <Notice
          tone="caution"
          icon={Info}
          title={t("upload.resume.title")}
          action={
            <Button type="button" variant="outline" onClick={() => void up.discard()}>
              {t("upload.discard")}
            </Button>
          }
        >
          <p>{t("upload.resume.body")}</p>
          <ul className="mt-2 grid gap-1">
            {awaiting.map((e) => (
              <li key={e.key}>
                <span translate="no" className="font-medium [overflow-wrap:anywhere]">
                  {e.name}
                </span>{" "}
                <span className="figure">({formatBytes(e.size)})</span>
              </li>
            ))}
          </ul>
        </Notice>
      )}

      {showLargeHint(state) && (
        <Notice tone="caution" icon={TriangleAlert} title={t("upload.large.title")}>
          {t("upload.large.body")}
        </Notice>
      )}

      {state.rejected.length > 0 && (
        <Notice
          role="alert"
          tone="error"
          icon={CircleAlert}
          title={t("upload.rejected.title")}
          action={
            <Button type="button" variant="outline" size="sm" onClick={up.dismissRejected}>
              {t("upload.rejected.dismiss")}
            </Button>
          }
        >
          <ul className="grid gap-1">
            {state.rejected.map((r, i) => (
              // biome-ignore lint/suspicious/noArrayIndexKey: the same name can be refused twice
              <li key={`${r.name}-${i}`}>
                <span translate="no" className="font-medium [overflow-wrap:anywhere]">
                  {r.name}
                </span>
                : {reasonText(t, r.code)}
              </li>
            ))}
          </ul>
        </Notice>
      )}

      <Picker accept={accept} onFiles={up.addFiles} disabled={busy || state.jobError !== null} />

      {state.entries.some((e) => e.status !== "awaiting") && (
        <section aria-labelledby="upload-files" className="grid gap-3">
          <h2 id="upload-files" className="text-lg leading-tight tracking-[-0.02em]">
            {t("upload.files.title")}
          </h2>
          {state.phase !== "choosing" && (
            <p aria-live="polite" className="text-sm text-ink-2">
              {t("upload.summary", { done, count: state.entries.length })}
            </p>
          )}
          <FileProgressList>
            {state.entries
              .filter((e) => e.status !== "awaiting")
              .map((e) => rowFor(e, up, { t, formatBytes }, confirmRemove))}
          </FileProgressList>
        </section>
      )}

      {state.phase === "choosing" && state.entries.length > 0 && (
        <div className="flex flex-wrap items-center gap-3">
          <Button type="button" size="lg" disabled={up.creating} onClick={() => void up.start()}>
            {up.creating ? t("upload.starting") : t("upload.start")}
          </Button>
          {up.startError && <ErrorMessage error={up.startError} />}
        </div>
      )}

      {state.phase === "submitting" && <p role="status">{t("upload.submitting")}</p>}

      {state.submitError && (
        <Notice
          role="alert"
          tone="error"
          icon={CircleAlert}
          title={t("upload.submitFailed.title")}
          action={
            <Button type="button" variant="outline" onClick={() => void up.submit()}>
              {t("shell.error.retry")}
            </Button>
          }
        >
          {reasonText(t, state.submitError)}
        </Notice>
      )}

      {state.phase === "uploading" && state.entries.length > 0 && awaiting.length === 0 && (
        <div>
          <Button type="button" variant="ghost" onClick={() => void up.discard()}>
            {t("upload.discard")}
          </Button>
        </div>
      )}
    </div>
  );
}
