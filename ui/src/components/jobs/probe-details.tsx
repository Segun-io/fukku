import type { Probe } from "@/api/types";
import { useT } from "@/i18n";
import { summarizeProbe, type TrackLine } from "@/lib/job-text";

function Tracks({ lines, empty }: { lines: TrackLine[]; empty: string }) {
  const { t } = useT();
  if (lines.length === 0) return <span className="text-muted-foreground">{empty}</span>;
  return (
    <ul className="grid gap-1">
      {lines.map((line) => (
        <li key={line.key} className="[overflow-wrap:anywhere]">
          <span className="font-medium">{line.language ?? t("job.probe.noLanguage")}</span>
          {line.title && <span translate="no"> &ldquo;{line.title}&rdquo;</span>}
          <span className="figure ml-2 text-sm text-muted-foreground">{line.detail}</span>
          {line.flags.map((flag) => (
            <span key={flag} className="ml-2 text-sm text-muted-foreground">
              ({t(`job.probe.flag.${flag}`)})
            </span>
          ))}
        </li>
      ))}
    </ul>
  );
}

/**
 * What the worker found in a video: how long it is, the picture, and every audio and
 * subtitle track with its language and title.
 */
export function ProbeDetails({ name, probe }: { name: string; probe: Probe }) {
  const { t, lang, formatDuration } = useT();
  const s = summarizeProbe(probe, { formatDuration, lang });
  const picture = [
    s.resolution,
    s.videoCodec,
    s.frameRate && t("job.probe.rate", { rate: s.frameRate }),
  ]
    .filter(Boolean)
    .join(", ");

  return (
    <section aria-label={t("job.probe.title", { name })} className="grid gap-2">
      <h3 translate="no" className="[overflow-wrap:anywhere]">
        {t("job.probe.title", { name })}
      </h3>
      <dl className="grid grid-cols-1 gap-x-6 gap-y-3 sm:grid-cols-[auto_minmax(0,1fr)] sm:gap-y-2">
        <div className="grid sm:col-span-2 sm:grid-cols-subgrid">
          <dt className="text-sm text-muted-foreground sm:text-base">{t("job.probe.duration")}</dt>
          <dd className="figure">{s.duration}</dd>
        </div>
        <div className="grid sm:col-span-2 sm:grid-cols-subgrid">
          <dt className="text-sm text-muted-foreground sm:text-base">{t("job.probe.video")}</dt>
          <dd className="figure">{picture}</dd>
        </div>
        <div className="grid sm:col-span-2 sm:grid-cols-subgrid">
          <dt className="text-sm text-muted-foreground sm:text-base">{t("job.probe.audio")}</dt>
          <dd>
            <Tracks lines={s.audio} empty={t("job.probe.none")} />
          </dd>
        </div>
        <div className="grid sm:col-span-2 sm:grid-cols-subgrid">
          <dt className="text-sm text-muted-foreground sm:text-base">{t("job.probe.subtitles")}</dt>
          <dd>
            <Tracks lines={s.subtitles} empty={t("job.probe.none")} />
          </dd>
        </div>
      </dl>
    </section>
  );
}
