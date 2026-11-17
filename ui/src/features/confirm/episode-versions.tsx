import { Info, TriangleAlert } from "lucide-react";
import { useId } from "react";
import type { EpisodeVersionsView, ProposalRow } from "@/api/proposal";
import { useSession } from "@/api/session";
import { inputClass } from "@/components/patterns/field";
import { Notice } from "@/components/shell/notice";
import { type TKey, useT } from "@/i18n";
import { problemText, rowProblemText, versionNoticeText } from "@/lib/flow2-text";

/** A refusal of the confirmation that names rows (`version_upgrade`, `version_exists`...). */
export type VersionRefusal = { code: string; rows: string[] };

const refusalCodes = [
  "version_upgrade",
  "version_unchecked",
  "version_unavailable",
  "version_exists",
];

/** Whether a refusal code is one of the season-dub version's, which name rows. */
export const isVersionRefusal = (code: string) => refusalCodes.includes(code);

type Props = {
  view: EpisodeVersionsView | undefined;
  /** The proposal's rows, for the file names. */
  rows: ProposalRow[];
  label: string;
  onLabel: (label: string) => void;
  /** The server refused the label (`version_label`). */
  labelRefused: boolean;
  refusal: VersionRefusal | null;
};

/**
 * What the policy `version` of the season-dub choice does (#58): the label of the second
 * versions (proposed, and the user may change it), the name each episode's file would get or
 * why it cannot be one, how Sonarr and the media server treat the versions, and, after a
 * refusal, the rows it names. The server judges the label and the rows, so the name and the
 * label problem are shown for the proposed label only.
 */
export function EpisodeVersionsSection({
  view,
  rows,
  label,
  onLabel,
  labelRefused,
  refusal,
}: Props) {
  const { t } = useT();
  const id = useId();
  const session = useSession();
  const admin = session.data?.user.admin === true;
  const proposed = view !== undefined && label.trim() === view.label;
  const problem = proposed ? (view?.problem ?? null) : null;
  const shown = problemText(problem?.reason ?? "", problem?.token ?? "");
  const nameOf = (rowId: string) => rows.find((r) => r.id === rowId)?.name ?? rowId;
  const problemOf = (rowId: string) => view?.rows.find((r) => r.row === rowId)?.problem ?? "";
  const pad = (n: number) => String(n).padStart(2, "0");

  return (
    <section aria-labelledby={`${id}-title`} className="grid gap-5">
      <h2 id={`${id}-title`}>{t("confirm.version.title")}</h2>

      <div className="grid gap-1 sm:max-w-md">
        <label htmlFor={`${id}-label`} className="font-medium">
          {t("confirm.version.label")}
        </label>
        <input
          id={`${id}-label`}
          name="versionLabel"
          type="text"
          autoComplete="off"
          spellCheck={false}
          translate="no"
          className={inputClass}
          value={label}
          aria-invalid={problem || labelRefused ? true : undefined}
          aria-describedby={`${id}-label-hint`}
          onChange={(e) => onLabel(e.target.value)}
        />
        <p id={`${id}-label-hint`} className="max-w-[65ch] text-sm text-muted-foreground">
          {t("confirm.version.label.hint")}
        </p>
      </div>

      {problem && (
        <Notice
          role="alert"
          tone="caution"
          icon={TriangleAlert}
          titleAs="h3"
          title={t("flow2.version.problem.title")}
        >
          <p>{t(shown.key, shown.params)}</p>
          {admin && <p className="mt-2">{t("flow2.version.problem.admin")}</p>}
        </Notice>
      )}

      {view && view.rows.length > 0 && (
        <div className="grid gap-2">
          <h3 className="font-sans text-sm font-bold tracking-normal">
            {t("confirm.version.rows")}
          </h3>
          <ul className="divide-y border-y">
            {view.rows.map((r) => (
              <li key={r.row} className="grid gap-1 py-3" data-version-row={r.row}>
                <span translate="no" className="font-medium [overflow-wrap:anywhere]">
                  {nameOf(r.row)}
                </span>
                <span className="text-sm text-ink-2">
                  {t("confirm.version.row.episode", {
                    season: r.seasonNumber,
                    episode: r.episodeNumber,
                  })}
                </span>
                {r.problem !== "" ? (
                  <span className="flex items-start gap-2 text-sm text-destructive">
                    <TriangleAlert aria-hidden="true" className="mt-0.5 size-4 shrink-0" />
                    {t(rowProblemText(r.problem).key, rowProblemText(r.problem).params)}
                  </span>
                ) : proposed && r.name !== "" ? (
                  <span translate="no" className="text-sm text-ink-2 [overflow-wrap:anywhere]">
                    {r.name}
                  </span>
                ) : (
                  <span className="text-sm text-ink-2">{t("confirm.version.row.waiting")}</span>
                )}
              </li>
            ))}
          </ul>
        </div>
      )}

      {view && view.notices.length > 0 && (
        <Notice
          role="note"
          tone="waiting"
          icon={Info}
          titleAs="h3"
          title={t("flow2.version.notices")}
        >
          <ul className="grid list-disc gap-1.5 pl-5">
            {view.notices.map((code) => (
              <li key={code}>{t(versionNoticeText(code).key, versionNoticeText(code).params)}</li>
            ))}
          </ul>
        </Notice>
      )}

      {refusal && refusal.rows.length > 0 && (
        <Notice
          role="alert"
          tone="error"
          icon={TriangleAlert}
          titleAs="h3"
          title={t("confirm.version.refused.title")}
        >
          <ul className="grid gap-2">
            {refusal.rows.map((rowId) => {
              const why = refusal.code === "version_unavailable" ? problemOf(rowId) : "";
              const row = view?.rows.find((r) => r.row === rowId);
              return (
                <li key={rowId} data-refused-row={rowId} className="grid gap-0.5">
                  <span translate="no" className="font-medium [overflow-wrap:anywhere]">
                    {nameOf(rowId)}
                    {row &&
                      ` (${t("import.episodeVersion.item", { season: pad(row.seasonNumber), episode: pad(row.episodeNumber) })})`}
                  </span>
                  <span>
                    {why !== ""
                      ? t(rowProblemText(why).key, rowProblemText(why).params)
                      : t(`confirm.version.refusal.${refusal.code}` as TKey)}
                  </span>
                </li>
              );
            })}
          </ul>
          <p className="mt-2 text-sm">{t("confirm.version.refused.hint")}</p>
        </Notice>
      )}
    </section>
  );
}
