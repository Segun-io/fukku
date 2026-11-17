// Warnings and hints of the proposal (#38, #39) by code, in plain words.

import type { ProposalRow } from "@/api/proposal";
import type { TKey } from "@/i18n";

/** Warning codes the UI has a sentence for (`confirm.warning.<code>`). */
export const warningCodes = [
  "not_offered",
  "existing_file",
  "double_episode",
  "runtime_mismatch",
  "duplicate_episode",
  "duplicate_film",
  "language",
] as const;

/** Hint codes (`confirm.hint.<code>`). */
export const hintCodes = ["dvd_order"] as const;

export type NoticeText = { key: TKey; params?: { code: string } };

/** An unknown code (a newer server) gets a generic sentence that carries the code. */
export function warningText(code: string): NoticeText {
  if ((warningCodes as readonly string[]).includes(code)) {
    return { key: `confirm.warning.${code}` as TKey };
  }
  return { key: "confirm.warning.unknown", params: { code } };
}

export function hintText(code: string): NoticeText {
  if ((hintCodes as readonly string[]).includes(code)) {
    return { key: `confirm.hint.${code}` as TKey };
  }
  return { key: "confirm.hint.unknown", params: { code } };
}

/** Whether the app or Fukku has something to say against importing the row. */
/**
 * Whether importing the row despite its problems needs an admin while the server says so
 * (`notOffered` override `admin_required`, #116): a refusal of Radarr or Sonarr, or a file
 * the library already has. Fukku's own warnings on a new file stay the user's to agree to.
 */
export const overrideNeedsAdmin = (row: ProposalRow) =>
  row.rejections.length > 0 || row.warnings.some((w) => w.code === "existing_file");

export function hasIssues(row: ProposalRow): boolean {
  return row.rejections.length > 0 || row.warnings.length > 0;
}

/**
 * What the user has agreed to: a tick belongs to the issues it was given for, so a re-check
 * that brings a new rejection or warning asks again.
 */
export function issueSignature(row: ProposalRow): string {
  return [...row.rejections.map((r) => `r:${r.reason}`), ...row.warnings.map((w) => `w:${w.code}`)]
    .sort()
    .join("|");
}
