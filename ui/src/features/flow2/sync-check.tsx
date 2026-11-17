import { CircleAlert, LoaderCircle } from "lucide-react";
import { useId, useState } from "react";
import { useRequestPreview } from "@/api/flow2";
import type { AudioTrack, Flow2File, Preview } from "@/api/types";
import { ErrorMessage } from "@/components/error-message";
import { inputClass } from "@/components/patterns/field";
import { SelectField } from "@/components/patterns/select-field";
import { StatusLabel } from "@/components/patterns/status-label";
import { Button } from "@/components/ui/button";
import { useT } from "@/i18n";
import {
  audioLines,
  formatClock,
  parseOffset,
  previewErrorText,
  type TrackLine,
} from "@/lib/job-text";
import { trackLabel } from "./labels";

/** The track the preview uses when none is asked for: the upload's default, else its first. */
export function defaultAudio(tracks: readonly AudioTrack[]): number | undefined {
  return (tracks.find((a) => a.default) ?? tracks[0])?.index;
}

type Props = {
  jobId: string;
  upload: Flow2File;
  /** The last preview of the job, as the server holds it (null while it is being read). */
  preview: Preview | undefined;
  /** The `requestedAt` of the preview the user said matches; null: none. */
  synced: string | null;
  onSynced: (requestedAt: string | null) => void;
};

/**
 * The sync check of flow 2a: the clips (existing picture, new audio) and the offset. The user
 * picks the audio track and an offset, makes the clips, watches them and says whether they
 * match. A new request replaces the clips, so the tick is lost with them.
 */
export function SyncCheck({ jobId, upload, preview, synced, onSynced }: Props) {
  const { t, lang } = useT();
  const id = useId();
  const request = useRequestPreview(jobId);
  const tracks = audioLines(upload.audio, lang);

  // What the user typed; until they type, the fields show the last preview's values.
  const [track, setTrack] = useState<number | null>(null);
  const [offsetText, setOffsetText] = useState<string | null>(null);
  const [invalid, setInvalid] = useState(false);

  const shownTrack = track ?? preview?.audioTrack ?? defaultAudio(upload.audio) ?? 0;
  const shownOffset = offsetText ?? String(preview?.offsetMs ?? 0);
  const offset = parseOffset(shownOffset);

  const state = request.isPending ? "pending" : (preview?.state ?? "none");
  const ready = state === "ready" && preview !== undefined;
  const stale = ready && (offset !== preview.offsetMs || shownTrack !== preview.audioTrack);
  const isSynced = ready && synced !== null && synced === preview.requestedAt;

  const label = (line: TrackLine): string => trackLabel(t, line);
  const madeWith = tracks.find((x) => x.key === `a${preview?.audioTrack}`);

  function make(e: React.FormEvent) {
    e.preventDefault();
    if (request.isPending) return;
    if (offset === null) {
      setInvalid(true);
      document.getElementById(`${id}-offset`)?.focus();
      return;
    }
    setInvalid(false);
    onSynced(null);
    request.mutate({ offsetMs: offset, audioTrack: shownTrack });
  }

  return (
    <section aria-labelledby={`${id}-title`} className="grid gap-4">
      <h3 id={`${id}-title`}>{t("flow2.sync.title")}</h3>
      <p className="max-w-[65ch] text-ink-2">{t("flow2.sync.intro")}</p>

      <form noValidate onSubmit={make} className="grid gap-4 sm:max-w-md">
        <SelectField
          label={t("flow2.sync.audio")}
          value={String(shownTrack)}
          onChange={(e) => setTrack(Number(e.target.value))}
        >
          {tracks.map((x, i) => (
            <option key={x.key} value={String(upload.audio[i]?.index)}>
              {label(x)}
            </option>
          ))}
        </SelectField>

        <div className="grid gap-1">
          <label htmlFor={`${id}-offset`} className="text-sm text-muted-foreground">
            {t("flow2.sync.offset")}
          </label>
          <input
            id={`${id}-offset`}
            name="offsetMs"
            type="text"
            inputMode="numeric"
            autoComplete="off"
            spellCheck={false}
            className={inputClass}
            value={shownOffset}
            aria-invalid={invalid && offset === null ? true : undefined}
            aria-describedby={`${id}-offset-hint`}
            onChange={(e) => {
              setOffsetText(e.target.value);
              setInvalid(false);
            }}
          />
          <p id={`${id}-offset-hint`} className="max-w-[65ch] text-sm text-muted-foreground">
            {t("flow2.sync.offset.hint")}
          </p>
          {invalid && offset === null && (
            <p role="alert" className="flex items-start gap-2 text-sm text-destructive">
              <CircleAlert aria-hidden="true" className="mt-0.5 size-4 shrink-0" />
              {t("flow2.sync.offset.invalid")}
            </p>
          )}
        </div>

        <div>
          <Button
            type="submit"
            variant={ready ? "outline" : "default"}
            size="lg"
            aria-disabled={request.isPending}
            aria-busy={request.isPending}
          >
            {request.isPending && <LoaderCircle aria-hidden="true" className="animate-spin" />}
            {t(ready || state === "failed" ? "flow2.sync.again" : "flow2.sync.make")}
          </Button>
        </div>
      </form>

      {request.isError && <ErrorMessage error={request.error} />}

      {state === "pending" && (
        <p role="status" aria-busy="true" className="flex items-start gap-2 text-ink-2">
          <LoaderCircle aria-hidden="true" className="mt-0.5 size-5 shrink-0 animate-spin" />
          {t("flow2.sync.pending")}
        </p>
      )}

      {state === "failed" && preview && (
        <div role="alert" className="grid gap-1">
          <StatusLabel tone="error" className="text-base">
            {t("flow2.sync.failed.title")}
          </StatusLabel>
          <p className="max-w-[65ch] text-ink-2">
            {t(
              previewErrorText(preview.error?.code ?? "").key,
              previewErrorText(preview.error?.code ?? "").params,
            )}
          </p>
        </div>
      )}

      {ready && (
        <div className="grid gap-4">
          <p role="status" className="max-w-[65ch] text-ink-2">
            {t("flow2.sync.ready", {
              track: madeWith ? label(madeWith) : String(preview.audioTrack),
              offset: String(preview.offsetMs),
            })}
          </p>
          <ul aria-label={t("flow2.sync.clips")} className="grid gap-5 md:grid-cols-3">
            {preview.clips.map((clip) => {
              const name = t("flow2.sync.clip", {
                n: clip.n,
                total: preview.clips.length,
                at: formatClock(clip.startMs),
              });
              return (
                <li key={clip.n} className="grid content-start gap-2">
                  <p className="text-sm font-medium">{name}</p>
                  {/* biome-ignore lint/a11y/useMediaCaption: the clips are for judging whether the sound lines up with the picture; there is no speech to caption. */}
                  <video
                    controls
                    preload="metadata"
                    src={clip.url}
                    aria-label={name}
                    className="aspect-video w-full rounded-md bg-foreground"
                  >
                    {t("flow2.sync.clip.fallback")}
                  </video>
                </li>
              );
            })}
          </ul>

          {stale ? (
            <p role="note" className="max-w-[65ch] text-sm text-ink-2">
              {t("flow2.sync.stale")}
            </p>
          ) : isSynced ? (
            <StatusLabel tone="done" role="status" className="text-base">
              {t("flow2.sync.confirmed", { offset: String(preview.offsetMs) })}
            </StatusLabel>
          ) : (
            <div className="grid gap-2">
              <div>
                <Button type="button" size="lg" onClick={() => onSynced(preview.requestedAt)}>
                  {t("flow2.sync.match")}
                </Button>
              </div>
              <p className="max-w-[65ch] text-sm text-muted-foreground">
                {t("flow2.sync.nomatch")}
              </p>
            </div>
          )}
        </div>
      )}
    </section>
  );
}
