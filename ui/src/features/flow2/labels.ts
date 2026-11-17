import type { TFunction } from "@/i18n/translate";
import type { TrackLine } from "@/lib/job-text";

/** "Spanish “Latino” (AC3 5.1)": a track as the sync check and the merge name it. */
export function trackLabel(t: TFunction, line: TrackLine): string {
  const language = line.language ?? t("job.probe.noLanguage");
  return t("flow2.sync.track", {
    track: line.title ? `${language} “${line.title}”` : language,
    detail: line.detail,
  });
}
