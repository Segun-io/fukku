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
  "label_episode",
] as const;

/** The notices of a version the UI has a sentence for (`flow2.version.notice.<code>`). */
const noticeCodes = [
  "outside_radarr",
  "regrouped_after_changes",
  "kept_on_delete",
  "subtitles_not_handed_over",
  "untracked_by_sonarr",
  "adopted_without_episode_file",
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

/** Why an offer is not made (`flow2.reason.<code>`): flow 2's own and the season dub's (#58). */
export const offerReasonCodes = [
  "merge_disabled",
  "versions_disabled",
  "versions_unsupported",
  "runtime_differs",
  "media_server_too_old",
  "media_server_unknown",
  "series_folder_unusable",
  "season_folder_unavailable",
  "admin_required",
] as const;

export function offerReasonText(reason: string): { key: TKey; params?: { code: string } } {
  return (offerReasonCodes as readonly string[]).includes(reason)
    ? { key: `flow2.reason.${reason}` as TKey }
    : { key: "flow2.reason.unknown", params: { code: reason } };
}

/** Why a row cannot be kept as an episode's version (`confirm.version.row.<problem>`). */
const rowProblems = [
  "double_episode",
  "shared_file",
  "no_file",
  "outside_library",
  "name_too_long",
  "version_exists",
  "name_unreadable",
  "folder_unreadable",
];

export function rowProblemText(problem: string): { key: TKey; params?: { code: string } } {
  return rowProblems.includes(problem)
    ? { key: `confirm.version.row.${problem}` as TKey }
    : { key: "confirm.version.row.unknown", params: { code: problem } };
}

/** What the placement says of a version it refused (`import.episodeVersion.refusal.<code>`). */
const placementRefusals = [
  "version_upgrade",
  "version_unchecked",
  "version_exists",
  "version_failed",
];

export function placementRefusalText(code: string): { key: TKey; params?: { code: string } } {
  return placementRefusals.includes(code)
    ? { key: `import.episodeVersion.refusal.${code}` as TKey }
    : { key: "import.episodeVersion.refusal.unknown", params: { code } };
}

/**
 * The states of an episode's version (`import.episodeVersion.status.<status>`): the record's,
 * `refused`, and the ones of the periodic re-check (#96). Another one gets a generic sentence.
 */
const episodeVersionStatuses = [
  "placing",
  "linked",
  "grouped",
  "waiting",
  "missing",
  "refused",
  "adopted",
  "adoptable",
  "episode_file_missing",
  "series_moved",
  "series_deleted",
];

export function episodeVersionStatusText(status: string): {
  key: TKey;
  params?: { status: string };
} {
  return episodeVersionStatuses.includes(status)
    ? { key: `import.episodeVersion.status.${status}` as TKey }
    : { key: "import.episodeVersion.status.unknown", params: { status } };
}
