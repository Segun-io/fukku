import { ChevronRight } from "lucide-react";
import type { Probe } from "@/api/types";
import { useT } from "@/i18n";
import { summarizeProbe, type TrackLine } from "@/lib/job-text";

export function Tracks({ lines, empty }: { lines: TrackLine[]; empty: string }) {
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
            <span key={flag} className="ml-2 text-sm text-muted-foreground [overflow-wrap:normal]">
              ({t(`job.probe.flag.${flag}`)})
            </span>
          ))}
        </li>
      ))}
    </ul>
  );
}

const row = "grid sm:col-span-2 sm:grid-cols-subgrid";
const term = "text-sm text-muted-foreground sm:pt-0.5";

/**
 * What the worker found in a video: how long it is, the picture, and every audio and
 * subtitle track with its language and title. It is a disclosure under the file list: open
 * for a film or two, closed for a season, where 24 of them would bury the page.
 */
export function ProbeDetails({
  name,
  probe,
  open = true,
}: {
  name: string;
  probe: Probe;
  open?: boolean;
}) {
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
    <section aria-label={t("job.probe.title", { name })}>
      <details open={open} className="group">
        <summary className="-ml-1 flex min-h-11 cursor-pointer list-none items-center gap-1.5 rounded-sm py-2 pl-1 [&::-webkit-details-marker]:hidden">
          <ChevronRight
            aria-hidden="true"
            className="size-4 shrink-0 text-muted-foreground transition-transform duration-150 group-open:rotate-90"
          />
          <h3 translate="no" className="[overflow-wrap:anywhere]">
            {t("job.probe.title", { name })}
          </h3>
        </summary>
        <dl className="grid grid-cols-1 gap-x-6 gap-y-3 pt-1 pb-3 pl-5 sm:grid-cols-[auto_minmax(0,1fr)] sm:gap-y-2">
          <div className={row}>
            <dt className={term}>{t("job.probe.duration")}</dt>
            <dd className="figure">{s.duration}</dd>
          </div>
          <div className={row}>
            <dt className={term}>{t("job.probe.video")}</dt>
            <dd className="figure whitespace-normal">{picture}</dd>
          </div>
          <div className={row}>
            <dt className={term}>{t("job.probe.audio")}</dt>
            <dd>
              <Tracks lines={s.audio} empty={t("job.probe.none")} />
            </dd>
          </div>
          <div className={row}>
            <dt className={term}>{t("job.probe.subtitles")}</dt>
            <dd>
              <Tracks lines={s.subtitles} empty={t("job.probe.none")} />
            </dd>
          </div>
        </dl>
      </details>
    </section>
  );
}
