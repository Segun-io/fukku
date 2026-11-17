// What the pages say of a second version (#56), by the server's codes: the label problems and
// the notices. Used by the flow 2 page and by the job page's result.

import type { TKey } from "@/i18n";

/** The label problems the UI has a sentence for (`flow2.version.problem.<reason>`). */
const problemReasons = [
  "label_empty",
  "label_invalid",
  "label_too_long",
  "label_forbidden",
  "label_extra",
  "label_quality",
  "label_unchecked",
] as const;

/** The notices of a version the UI has a sentence for (`flow2.version.notice.<code>`). */
const noticeCodes = [
  "outside_radarr",
  "regrouped_after_changes",
  "kept_on_delete",
  "subtitles_not_handed_over",
] as const;

export function problemText(
  reason: string,
  token: string,
): { key: TKey; params: Record<string, string> } {
  return (problemReasons as readonly string[]).includes(reason)
    ? { key: `flow2.version.problem.${reason}` as TKey, params: { token } }
    : { key: "flow2.version.problem.unknown", params: { code: reason } };
}

export function versionNoticeText(code: string): { key: TKey; params?: { code: string } } {
  return (noticeCodes as readonly string[]).includes(code)
    ? { key: `flow2.version.notice.${code}` as TKey }
    : { key: "flow2.version.notice.unknown", params: { code } };
}
