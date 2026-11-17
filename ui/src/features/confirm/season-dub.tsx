import { CircleAlert, TriangleAlert } from "lucide-react";
import { useId } from "react";
import type { ProposalRow, SeasonDub } from "@/api/proposal";
import { ChoiceList } from "@/components/patterns/choice-list";
import { Notice } from "@/components/shell/notice";
import { type TKey, useT } from "@/i18n";
import { offerReasonText } from "@/lib/flow2-text";

/** The policies this page has text for (`confirm.dub.<policy>`). */
const knownPolicies = ["replace", "subtitlesOnly", "version"];

/** The refusals of the app that choosing `replace` answers (the same prefixes as the server's). */
const upgradeRefusals = [
  "not an upgrade for existing",
  "not a custom format upgrade for existing",
  "not a quality revision upgrade",
];

/** Whether the row's episodes already have a file: the server leaves such a row out unless a policy takes it. */
export const hasExistingFile = (row: ProposalRow) =>
  row.quality !== null && row.warnings.some((w) => w.code === "existing_file");

/**
 * The row as it stands when the policy is `replace`: it is in, and the choice answers the
 * existing-file warning and the app's refusal to replace a file it has. Any other problem of
 * the row stays and still needs its own agreement.
 */
export function replacedRow(row: ProposalRow): ProposalRow {
  return {
    ...row,
    skip: false,
    warnings: row.warnings.filter((w) => w.code !== "existing_file"),
    rejections: row.rejections.filter(
      (r) => !upgradeRefusals.some((p) => r.reason.trim().toLowerCase().startsWith(p)),
    ),
  };
}

/** The text of a policy: its label and what it does. An unknown one (a newer server) is named. */
export function policyText(policy: string): { label: TKey; body: TKey; known: boolean } {
  return knownPolicies.includes(policy)
    ? {
        label: `confirm.dub.${policy}` as TKey,
        body: `confirm.dub.${policy}.body` as TKey,
        known: true,
      }
    : { label: "confirm.dub.unknown", body: "confirm.dub.unknown.body", known: false };
}

/** The policy's name in a sentence (`confirm.dub.name.<policy>`). */
function policyName(policy: string): TKey {
  return ["replace", "subtitlesOnly", "version"].includes(policy)
    ? (`confirm.dub.name.${policy}` as TKey)
    : "confirm.dub.unknown";
}

type Props = {
  dub: SeasonDub;
  app: string;
  choice: string;
  onChoice: (policy: string) => void;
  /** Pressing "Import" found no choice. */
  missing: boolean;
  /** Rows the server says hold other episodes too (`shared_file`), and the ones agreed to. */
  shared: ProposalRow[];
  agreed: string[];
  onAgree: (rowId: string, value: boolean) => void;
};

/**
 * What to do with the episodes of the upload that already have a file in the library (#57):
 * the seasons and counts, then, when the server asks, the options that apply, each with its
 * consequence. A policy fixed by the server's settings is only told.
 */
export function SeasonDubSection({
  dub,
  app,
  choice,
  onChoice,
  missing,
  shared,
  agreed,
  onAgree,
}: Props) {
  const { t } = useT();
  const id = useId();
  const decided = dub.decided ? policyText(dub.decided) : null;
  return (
    <section aria-labelledby={`${id}-title`} className="grid gap-4">
      <h2 id={`${id}-title`}>{t("confirm.dub.title")}</h2>
      <p className="max-w-[65ch] text-ink-2">{t("confirm.dub.intro")}</p>
      <ul className="grid gap-1">
        {dub.seasons
          .filter((s) => s.existing > 0)
          .map((s) => (
            <li key={s.season} className="max-w-[65ch] font-medium">
              {t("confirm.dub.season", {
                season: s.season,
                existing: s.existing,
                count: s.episodes,
              })}
            </li>
          ))}
      </ul>

      {dub.ask && (
        <ChoiceList
          legend={t("confirm.dub.legend")}
          aria-describedby={missing ? `${id}-missing` : undefined}
          value={choice}
          onChange={onChoice}
          choices={dub.offers.map((policy) => {
            const text = policyText(policy);
            return {
              value: policy,
              label: t(text.label, { policy }),
              body: t(text.body, { app }),
              input: { "data-policy": policy },
            };
          })}
        >
          {missing && choice === "" && (
            <p
              id={`${id}-missing`}
              role="alert"
              className="flex items-start gap-2 text-destructive"
            >
              <CircleAlert aria-hidden="true" className="mt-0.5 size-5 shrink-0" />
              {t("confirm.dub.missing")}
            </p>
          )}
        </ChoiceList>
      )}

      {decided && dub.decided && (
        <p role="note" className="max-w-[65ch]">
          {t("confirm.dub.decided", { option: t(decided.label, { policy: dub.decided }) })}
          <span className="mt-1 block text-sm text-ink-2">{t(decided.body, { app })}</span>
        </p>
      )}

      {!dub.ask && !dub.decided && (
        <p className="max-w-[65ch] text-ink-2">{t("confirm.dub.none")}</p>
      )}

      {dub.notOffered && dub.notOffered.length > 0 && (
        <div className="grid gap-1 text-sm text-ink-2">
          <h3 className="font-sans text-sm font-bold tracking-normal">
            {t("flow2.notOffered.title")}
          </h3>
          <ul className="grid list-disc gap-1 pl-5">
            {dub.notOffered.map((n) => (
              <li key={n.offer} className="max-w-[65ch]">
                {t("flow2.notOffered.item", {
                  offer: t(policyName(n.offer), { policy: n.offer }),
                  reason: t(offerReasonText(n.reason).key, offerReasonText(n.reason).params),
                })}
              </li>
            ))}
          </ul>
        </div>
      )}

      {shared.length > 0 && (
        <Notice
          role="alert"
          tone="caution"
          icon={TriangleAlert}
          titleAs="h3"
          title={t("confirm.dub.shared.title")}
        >
          <p>{t("confirm.dub.shared.body")}</p>
          <div className="mt-2 grid gap-1">
            {shared.map((row) => (
              <label key={row.id} className="flex min-h-11 items-center gap-3 font-medium">
                <input
                  type="checkbox"
                  name={`shared-${row.id}`}
                  className="size-5 shrink-0 accent-primary"
                  checked={agreed.includes(row.id)}
                  onChange={(e) => onAgree(row.id, e.target.checked)}
                />
                <span translate="no" className="[overflow-wrap:anywhere]">
                  {t("confirm.dub.shared.accept", { name: row.name })}
                </span>
              </label>
            ))}
          </div>
        </Notice>
      )}
    </section>
  );
}
