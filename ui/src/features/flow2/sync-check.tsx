import { Hourglass, LoaderCircle } from "lucide-react";
import { useId, useState } from "react";
import { useRequestPreview } from "@/api/flow2";
import type { AudioTrack, Flow2File, Preview } from "@/api/types";
import { ErrorMessage } from "@/components/error-message";
import { NudgeField } from "@/components/patterns/nudge-field";
import { SelectField } from "@/components/patterns/select-field";
import { StatusLabel } from "@/components/patterns/status-label";
import { Button } from "@/components/ui/button";
import { useT } from "@/i18n";
import {
  audioLines,
  formatClock,
  maxOffsetMs,
  parseOffset,
  previewErrorText,
  type TrackLine,
} from "@/lib/job-text";
import { trackLabel } from "./labels";

/** The track the preview uses when none is asked for: the upload's default, else its first. */
export function defaultAudio(tracks: readonly AudioTrack[]): number | undefined {
  return (tracks.find((a) => a.default) ?? tracks[0])?.index;
}

/** What the fields show, and whether they still describe the clips that are ready. */
export function checkState(
  upload: Flow2File,
  preview: Preview | undefined,
  track: number | null,
  offsetText: string | null,
) {
  const shownTrack = track ?? preview?.audioTrack ?? defaultAudio(upload.audio) ?? 0;
  const shownOffset = offsetText ?? String(preview?.offsetMs ?? 0);
  const offset = parseOffset(shownOffset);
  const ready = preview?.state === "ready";
  // The clips were made with another track or offset than the fields say now.
  const stale = ready && (offset !== preview.offsetMs || shownTrack !== preview.audioTrack);
  return { shownTrack, shownOffset, offset, stale };
}

/** The steps of the offset buttons, in milliseconds: one the ear barely notices, one it does. */
const nudgeSteps = [50, 250] as const;

/** The server cuts three clips (start, middle, end): the places kept free while they are made. */
const clipPlaces = [1, 2, 3] as const;

type Props = {
  jobId: string;
  upload: Flow2File;
  /** The last preview of the job, as the server holds it (null while it is being read). */
  preview: Preview | undefined;
  /** The `requestedAt` of the preview the user said matches; null: none. */
  synced: string | null;
  onSynced: (requestedAt: string | null) => void;
  /** What the user typed; null until they type (the fields then show the last preview's). */
  track: number | null;
  offsetText: string | null;
  onTrack: (track: number) => void;
  onOffset: (text: string) => void;
};

/**
 * The sync check of flow 2a, in the order it is done: choose the audio track, make the clips
 * (existing picture, new audio), watch them, shift the audio if it is early or late and make
 * them again, then say that they match. The shift is a field with buttons that move it; the
 * buttons only write into the field. A new request replaces the clips, so the tick is lost
 * with them, and clips made with another track or shift than the fields say cannot be
 * confirmed.
 */
export function SyncCheck({
  jobId,
  upload,
  preview,
  synced,
  onSynced,
  track,
  offsetText,
  onTrack,
  onOffset,
}: Props) {
  const { t, lang, formatNumber } = useT();
  const id = useId();
  const request = useRequestPreview(jobId);
  const tracks = audioLines(upload.audio, lang);

  const [invalid, setInvalid] = useState(false);
  const { shownTrack, shownOffset, offset, stale } = checkState(upload, preview, track, offsetText);

  const state = request.isPending ? "pending" : (preview?.state ?? "none");
  const ready = state === "ready" && preview !== undefined;
  const isSynced = ready && synced !== null && synced === preview.requestedAt;

  const label = (line: TrackLine): string => trackLabel(t, line);
  const madeWith = tracks.find((x) => x.key === `a${preview?.audioTrack}`);

  // The shift in words and in seconds, next to the field in milliseconds.
  const seconds = (ms: number) =>
    t("flow2.seconds", { value: formatNumber(Math.abs(ms) / 1000, { maximumFractionDigits: 3 }) });
  const readout =
    offset === null
      ? ""
      : offset === 0
        ? t("flow2.sync.offset.say.none")
        : t(offset > 0 ? "flow2.sync.offset.say.later" : "flow2.sync.offset.say.earlier", {
            seconds: seconds(offset),
          });

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
    <section aria-labelledby={`${id}-title`} className="grid gap-5">
      <div className="grid gap-2">
        <h3 id={`${id}-title`}>{t("flow2.sync.title")}</h3>
        <p className="max-w-[65ch] text-ink-2">{t("flow2.sync.intro")}</p>
      </div>

      <form noValidate onSubmit={make} className="grid gap-5 sm:max-w-lg">
        <SelectField
          label={t("flow2.sync.audio")}
          name="audioTrack"
          value={String(shownTrack)}
          onChange={(e) => onTrack(Number(e.target.value))}
        >
          {tracks.map((x, i) => (
            <option key={x.key} value={String(upload.audio[i]?.index)}>
              {label(x)}
            </option>
          ))}
        </SelectField>

        {/* One region that stays in the page, so that a screen reader hears each change: the
            clips are being made, they are ready, they failed, the user said they match. */}
        <div role="status" aria-live="polite" className="grid gap-2 empty:hidden">
          {state === "pending" && (
            <StatusLabel tone="waiting" icon={Hourglass} className="text-base">
              {t("flow2.sync.pending")}
            </StatusLabel>
          )}
          {state === "failed" && preview && (
            <>
              <StatusLabel tone="error" className="text-base">
                {t("flow2.sync.failed.title")}
              </StatusLabel>
              <p className="max-w-[65ch] text-ink-2">
                {t(
                  previewErrorText(preview.error?.code ?? "").key,
                  previewErrorText(preview.error?.code ?? "").params,
                )}
              </p>
            </>
          )}
          {ready && (
            <p className="max-w-[65ch] text-ink-2">
              {t("flow2.sync.ready", {
                track: madeWith ? label(madeWith) : String(preview.audioTrack),
                offset: formatNumber(preview.offsetMs ?? 0),
              })}
            </p>
          )}
          {ready && isSynced && !stale && (
            <StatusLabel tone="done" className="text-base">
              {t("flow2.sync.confirmed", { offset: formatNumber(preview.offsetMs ?? 0) })}
            </StatusLabel>
          )}
        </div>

        {state === "pending" && (
          // The places of the clips, kept while they are made, so the controls under them stay put.
          <div aria-hidden="true" className="grid gap-5">
            {clipPlaces.map((n) => (
              <div key={n} className="grid gap-2">
                <div className="h-5" />
                <div className="aspect-video rounded-md border border-dashed border-input bg-muted" />
              </div>
            ))}
          </div>
        )}

        {ready && (
          <ul aria-label={t("flow2.sync.clips")} className="grid gap-5">
            {preview.clips.map((clip) => {
              const name = t("flow2.sync.clip", {
                n: clip.n,
                total: preview.clips.length,
                at: formatClock(clip.startMs),
              });
              return (
                <li key={clip.n} className="grid content-start gap-2">
                  <p className="text-sm leading-5 font-medium">{name}</p>
                  {/* biome-ignore lint/a11y/useMediaCaption: the clips are cut from the film and the new audio on the server and have no caption track; they are short, and are there to judge the timing of the sound against the picture. */}
                  <video
                    controls
                    playsInline
                    preload="metadata"
                    src={clip.url}
                    aria-label={name}
                    className="aspect-video w-full rounded-md border bg-screen"
                  >
                    {t("flow2.sync.clip.fallback")}
                  </video>
                </li>
              );
            })}
          </ul>
        )}

        <NudgeField
          id={`${id}-offset`}
          label={t("flow2.sync.offset")}
          value={shownOffset}
          onChange={(text) => {
            onOffset(text);
            setInvalid(false);
          }}
          parse={parseOffset}
          min={-maxOffsetMs}
          max={maxOffsetMs}
          unit={t("flow2.sync.offset.unit")}
          input={{ name: "offsetMs", inputMode: "text", pattern: "[+\\-\\u2212]?[0-9]+" }}
          groups={[
            {
              caption: t("flow2.sync.offset.late"),
              steps: nudgeSteps.map((ms) => ({
                by: -ms,
                label: t("flow2.sync.offset.earlier", { ms }),
              })),
            },
            {
              caption: t("flow2.sync.offset.early"),
              steps: nudgeSteps.map((ms) => ({
                by: ms,
                label: t("flow2.sync.offset.later", { ms }),
              })),
            },
          ]}
          readout={readout}
          hint={t("flow2.sync.offset.hint")}
          error={invalid && offset === null ? t("flow2.sync.offset.invalid") : undefined}
        />

        {stale && (
          <p role="note" className="max-w-[65ch]">
            <StatusLabel tone="caution" className="text-base font-normal">
              {t("flow2.sync.stale")}
            </StatusLabel>
          </p>
        )}

        <div>
          <Button
            type="submit"
            variant={ready && !stale ? "outline" : "default"}
            size="lg"
            className="w-full sm:w-auto"
            aria-disabled={request.isPending}
            aria-busy={request.isPending}
          >
            {request.isPending && <LoaderCircle aria-hidden="true" className="animate-spin" />}
            {t(ready || state === "failed" ? "flow2.sync.again" : "flow2.sync.make")}
          </Button>
        </div>
      </form>

      {request.isError && <ErrorMessage error={request.error} />}

      {ready && !stale && !isSynced && (
        <div className="grid gap-3 border-t pt-5">
          <p className="font-medium">{t("flow2.sync.question")}</p>
          <div>
            <Button
              type="button"
              size="lg"
              className="w-full sm:w-auto"
              onClick={() => onSynced(preview.requestedAt)}
            >
              {t("flow2.sync.match")}
            </Button>
          </div>
          <div className="grid max-w-[65ch] gap-1 text-sm text-ink-2">
            <p>{t("flow2.sync.nomatch")}</p>
            <p>{t("flow2.sync.drift")}</p>
          </div>
        </div>
      )}
    </section>
  );
}
