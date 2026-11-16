import { useNavigate } from "@tanstack/react-router";
import { CircleAlert, CircleCheck, FilePlus2, TriangleAlert } from "lucide-react";
import { isKnownErrorCode, type Session } from "@/api/types";
import { ErrorMessage } from "@/components/error-message";
import { FilePicker } from "@/components/patterns/file-picker";
import { FileProgress, FileProgressList } from "@/components/patterns/file-progress";
import { SignInAgain } from "@/components/shell/banners";
import { Notice } from "@/components/shell/notice";
import { Button } from "@/components/ui/button";
import { type TFunction, useT } from "@/i18n";
import { acceptedExtensions, awaitingFiles, type Entry, secondsLeft, showLargeHint } from "./queue";
import { useUpload } from "./use-upload";

/** The reason a file failed or was refused, in the user's language. */
function reasonText(t: TFunction, code: string | undefined): string {
  if (code === "file_mismatch") return t("upload.reason.file_mismatch");
  if (code && isKnownErrorCode(code)) return t(`errors.${code}`);
  return t("errors.unknown");
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

/**
 * The upload page, once the session (and so the server's rules) is known.
 *
 * From top to bottom: what needs the user before anything can move (sign in again, an
 * upload that cannot continue, an interrupted upload waiting for its files), the picker,
 * what the picker refused, the strip of file labels, and last the one action of the page.
 * Once there are files, the picker shrinks to one line, so the files are what the page shows.
 */
export function UploadView({ config }: { config: Session["upload"] }) {
  const { t, formatBytes } = useT();
  const navigate = useNavigate();
  const up = useUpload(config, (jobId) => {
    void navigate({ to: "/jobs/$id", params: { id: jobId } });
  });
  const { state } = up;
  const accept = acceptedExtensions(state.rules).join(",");
  const awaiting = awaitingFiles(state);
  const listed = state.entries.filter((e) => e.status !== "awaiting");
  const busy = state.phase === "submitting" || state.phase === "submitted";
  const done = state.entries.filter((e) => e.status === "done").length;

  const confirmRemove = (entry: Entry) =>
    entry.sent === 0 || window.confirm(t("upload.remove.confirm", { name: entry.name }));

  // Discarding deletes what the server has of this upload: it asks first, as removing one
  // file does, when this page knows that bytes were sent.
  const discard = () => {
    const sent = state.entries.some((e) => e.sent > 0);
    if (!sent || window.confirm(t("upload.discard.confirm"))) void up.discard();
  };

  if (up.restoring) {
    return (
      <p role="status" className="text-ink-2">
        {t("upload.restoring")}
      </p>
    );
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
          icon={FilePlus2}
          title={t("upload.resume.title")}
          action={
            <Button type="button" variant="outline" onClick={discard}>
              {t("upload.discard")}
            </Button>
          }
        >
          <p>{t("upload.resume.body")}</p>
          <ul className="mt-3 divide-y divide-caution-edge/40 border-y border-caution-edge/40 text-foreground">
            {awaiting.map((e) => (
              <li
                key={e.key}
                className="grid gap-x-4 py-2 sm:grid-cols-[minmax(0,1fr)_auto] sm:items-baseline"
              >
                <span translate="no" className="min-w-0 font-medium [overflow-wrap:anywhere]">
                  {e.name}
                </span>
                <span className="figure text-ink-2">{formatBytes(e.size)}</span>
              </li>
            ))}
          </ul>
        </Notice>
      )}

      <FilePicker
        title={t("upload.drop.title")}
        hint={t("upload.drop.hint")}
        chooseLabel={t("upload.choose")}
        accepted={t("upload.drop.accepted", { types: accept.split(",").join(" ") })}
        accept={accept}
        onFiles={up.addFiles}
        disabled={busy || state.jobError !== null}
        size={state.entries.length > 0 ? "compact" : "full"}
        primary={listed.length === 0 || awaiting.length > 0}
      />

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
          <ul className="grid gap-2">
            {state.rejected.map((r, i) => (
              // biome-ignore lint/suspicious/noArrayIndexKey: the same name can be refused twice
              <li key={`${r.name}-${i}`}>
                <span
                  translate="no"
                  className="font-medium text-foreground [overflow-wrap:anywhere]"
                >
                  {r.name}
                </span>
                : {reasonText(t, r.code)}
              </li>
            ))}
          </ul>
        </Notice>
      )}

      {listed.length > 0 && (
        <section aria-labelledby="upload-files" className="grid gap-3">
          <div className="flex flex-wrap items-baseline justify-between gap-x-4 gap-y-1">
            <h2 id="upload-files">{t("upload.files.title")}</h2>
            {state.phase !== "choosing" && (
              <p aria-live="polite" className="text-sm text-muted-foreground">
                {t("upload.summary", { done, count: state.entries.length })}
              </p>
            )}
          </div>
          <FileProgressList>
            {listed.map((e) => rowFor(e, up, { t, formatBytes }, confirmRemove))}
          </FileProgressList>
        </section>
      )}

      {showLargeHint(state) && (
        <Notice tone="caution" icon={TriangleAlert} title={t("upload.large.title")}>
          {t("upload.large.body")}
        </Notice>
      )}

      {state.phase === "choosing" && state.entries.length > 0 && (
        <div className="grid gap-1">
          <Button
            type="button"
            size="lg"
            className="w-full sm:w-auto sm:justify-self-start"
            disabled={up.creating}
            onClick={() => void up.start()}
          >
            {up.creating ? t("upload.starting") : t("upload.start")}
          </Button>
          {up.startError && <ErrorMessage error={up.startError} />}
        </div>
      )}

      {state.phase === "submitting" && (
        <Notice role="status" tone="done" icon={CircleCheck} title={t("upload.submitting")} />
      )}

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
        <div className="border-t pt-5">
          <Button type="button" variant="outline" onClick={discard}>
            {t("upload.discard")}
          </Button>
        </div>
      )}
    </div>
  );
}
