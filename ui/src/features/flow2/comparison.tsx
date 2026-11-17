import { Check, TriangleAlert } from "lucide-react";
import type { ReactNode } from "react";
import type { Flow2, Flow2File } from "@/api/types";
import { Tracks } from "@/components/jobs/probe-details";
import { SideBySide, type SideBySideRow } from "@/components/patterns/side-by-side";
import { Notice } from "@/components/shell/notice";
import { type TKey, useT } from "@/i18n";
import { audioLines, subtitleLines } from "@/lib/job-text";

/**
 * The two files term by term (side by side from 48rem, one under the other on a phone), and
 * what the comparison found in plain words: the same cut, a PAL speed-up, or a different cut.
 */
export function Comparison({ flow2, upload }: { flow2: Flow2; upload: Flow2File }) {
  const { t, lang, formatNumber, formatBytes, formatDuration } = useT();
  const finding = flow2.finding;
  const seconds = (ms: number) =>
    t("flow2.seconds", { value: formatNumber(Math.abs(ms) / 1000, { maximumFractionDigits: 1 }) });

  const files = [flow2.library, upload] as const;
  const unknown = <span className="text-muted-foreground">{t("flow2.unknown")}</span>;
  const both = (value: (file: Flow2File) => ReactNode): [ReactNode, ReactNode] => [
    value(files[0]),
    value(files[1]),
  ];
  // Radarr names the quality and the languages of its own file; the upload has them only
  // once it is imported. A line that neither file has is left out.
  const rows: SideBySideRow[] = [
    { term: t("flow2.file"), values: both((f) => f.name || unknown), verbatim: true },
    {
      term: t("flow2.duration"),
      figure: true,
      values: both((f) => (f.durationMs === null ? unknown : formatDuration(f.durationMs))),
    },
    { term: t("flow2.size"), figure: true, values: both((f) => formatBytes(f.size)) },
    ...(files.some((f) => f.quality)
      ? [
          {
            term: t("flow2.quality"),
            verbatim: true,
            values: both((f) => f.quality?.name ?? unknown),
          },
        ]
      : []),
    ...(files.some((f) => f.languages.length > 0)
      ? [
          {
            term: t("flow2.languages"),
            verbatim: true,
            values: both((f) =>
              f.languages.length > 0 ? f.languages.map((l) => l.name).join(", ") : unknown,
            ),
          },
        ]
      : []),
    {
      term: t("flow2.audio"),
      values: both((f) => <Tracks lines={audioLines(f.audio, lang)} empty={t("job.probe.none")} />),
    },
    {
      term: t("flow2.subtitles"),
      values: both((f) => (
        <Tracks lines={subtitleLines(f.subtitles, lang)} empty={t("job.probe.none")} />
      )),
    },
  ];

  return (
    <section aria-labelledby="flow2-compare" className="grid gap-4">
      <h2 id="flow2-compare">{t("flow2.compare.title")}</h2>
      <SideBySide titles={[t("flow2.side.library"), t("flow2.side.upload")]} rows={rows} />
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
