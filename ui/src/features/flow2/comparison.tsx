import { Check, TriangleAlert } from "lucide-react";
import { useId } from "react";
import type { Flow2, Flow2File } from "@/api/types";
import { Tracks } from "@/components/jobs/probe-details";
import { Notice } from "@/components/shell/notice";
import { type TKey, useT } from "@/i18n";
import { audioLines, subtitleLines } from "@/lib/job-text";

const row = "grid sm:col-span-2 sm:grid-cols-subgrid";
const term = "text-sm text-muted-foreground sm:pt-0.5";

/** One side of the comparison: what is known of a file, as a list of terms. */
function Side({ title, file }: { title: string; file: Flow2File }) {
  const { t, lang, formatBytes, formatDuration } = useT();
  const id = useId();
  const unknown = t("flow2.unknown");
  return (
    <section aria-labelledby={id} className="grid content-start gap-3 rounded-lg border p-4">
      <h3 id={id}>{title}</h3>
      <dl className="grid grid-cols-1 gap-x-4 gap-y-3 sm:grid-cols-[auto_minmax(0,1fr)] sm:gap-y-2">
        <div className={row}>
          <dt className={term}>{t("flow2.file")}</dt>
          <dd translate="no" className="[overflow-wrap:anywhere]">
            {file.name || unknown}
          </dd>
        </div>
        <div className={row}>
          <dt className={term}>{t("flow2.size")}</dt>
          <dd className="figure">{formatBytes(file.size)}</dd>
        </div>
        {file.quality && (
          <div className={row}>
            <dt className={term}>{t("flow2.quality")}</dt>
            <dd translate="no">{file.quality.name}</dd>
          </div>
        )}
        {file.languages.length > 0 && (
          <div className={row}>
            <dt className={term}>{t("flow2.languages")}</dt>
            <dd translate="no">{file.languages.map((l) => l.name).join(", ")}</dd>
          </div>
        )}
        <div className={row}>
          <dt className={term}>{t("flow2.duration")}</dt>
          <dd className="figure">
            {file.durationMs === null ? unknown : formatDuration(file.durationMs)}
          </dd>
        </div>
        <div className={row}>
          <dt className={term}>{t("flow2.audio")}</dt>
          <dd>
            <Tracks lines={audioLines(file.audio, lang)} empty={t("job.probe.none")} />
          </dd>
        </div>
        <div className={row}>
          <dt className={term}>{t("flow2.subtitles")}</dt>
          <dd>
            <Tracks lines={subtitleLines(file.subtitles, lang)} empty={t("job.probe.none")} />
          </dd>
        </div>
      </dl>
    </section>
  );
}

/**
 * The two files side by side (stacked on a phone), and what the comparison found in plain
 * words: the same cut, a PAL speed-up, or a different cut.
 */
export function Comparison({ flow2, upload }: { flow2: Flow2; upload: Flow2File }) {
  const { t, formatNumber } = useT();
  const finding = flow2.finding;
  const seconds = (ms: number) =>
    t("flow2.seconds", { value: formatNumber(Math.abs(ms) / 1000, { maximumFractionDigits: 1 }) });
  return (
    <section aria-labelledby="flow2-compare" className="grid gap-4">
      <h2 id="flow2-compare">{t("flow2.compare.title")}</h2>
      <div className="grid gap-4 md:grid-cols-2">
        <Side title={t("flow2.side.library")} file={flow2.library} />
        <Side title={t("flow2.side.upload")} file={upload} />
      </div>
      {finding && (
        <Notice
          role="status"
          tone={finding === "match" ? "done" : "caution"}
          icon={finding === "match" ? Check : TriangleAlert}
          titleAs="h3"
          title={t(`flow2.finding.${finding}.title` as TKey)}
        >
          {t(`flow2.finding.${finding}.body` as TKey, {
            delta: seconds(flow2.deltaMs ?? 0),
            ratio: formatNumber(flow2.ratio ?? 1, { maximumFractionDigits: 4 }),
          })}
        </Notice>
      )}
    </section>
  );
}
