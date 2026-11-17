import { TriangleAlert } from "lucide-react";
import { useId, useState } from "react";
import { usePreview } from "@/api/flow2";
import type { Flow2, Flow2File, Flow2Offer } from "@/api/types";
import { Notice } from "@/components/shell/notice";
import { type TKey, useT } from "@/i18n";
import { cn } from "@/lib/utils";
import { Comparison } from "./comparison";
import { MergeForm } from "./merge-form";
import { SyncCheck } from "./sync-check";
import { VersionForm } from "./version-form";

const offerOrder: readonly Flow2Offer[] = ["merge", "version"];

/** The merge path: the sync check first, and the merge confirmation once the user says they match. */
function MergePanel({ jobId, upload }: { jobId: string; upload: Flow2File }) {
  const preview = usePreview(jobId, true);
  const [synced, setSynced] = useState<string | null>(null);
  const data = preview.data;
  const ready = data?.state === "ready";
  const isSynced = ready && synced !== null && synced === data.requestedAt;
  return (
    <div className="grid gap-8">
      <SyncCheck
        jobId={jobId}
        upload={upload}
        preview={data}
        synced={synced}
        onSynced={setSynced}
      />
      {isSynced && data && <MergeForm jobId={jobId} upload={upload} preview={data} />}
    </div>
  );
}

/**
 * The flow 2 page's body: the comparison, then what can be done with it. With one offer the
 * path is shown straight away; with both the user chooses. With none the reasons are listed.
 */
export function Flow2View({
  jobId,
  flow2,
  upload,
}: {
  jobId: string;
  flow2: Flow2;
  upload: Flow2File;
}) {
  const { t } = useT();
  const legend = useId();
  const offers = offerOrder.filter((o) => flow2.offers.includes(o));
  const [picked, setPicked] = useState<Flow2Offer | null>(null);
  const choice = picked && offers.includes(picked) ? picked : (offers[0] ?? null);

  // With an offer, the missing one is explained under the choice, except the cut that the
  // finding above already described.
  const missing = flow2.notOffered.filter(
    (n) => offers.length === 0 || n.reason !== "runtime_differs",
  );
  const reason = (code: string) =>
    (
      ["merge_disabled", "versions_disabled", "versions_unsupported", "runtime_differs"] as const
    ).includes(code as never)
      ? t(`flow2.reason.${code}` as TKey)
      : t("flow2.reason.unknown", { code });

  const notOffered = missing.length > 0 && (
    <section aria-labelledby={`${legend}-missing`} className="grid gap-1 text-sm text-ink-2">
      <h3 id={`${legend}-missing`} className="font-sans text-sm font-bold tracking-normal">
        {t("flow2.notOffered.title")}
      </h3>
      <ul className="grid list-disc gap-1 pl-5">
        {missing.map((n) => (
          <li key={n.offer} className="max-w-[65ch]">
            {t("flow2.notOffered.item", {
              offer: t(`flow2.offer.${n.offer}` as TKey),
              reason: reason(n.reason),
            })}
          </li>
        ))}
      </ul>
    </section>
  );

  return (
    <div className="grid gap-10">
      <Comparison flow2={flow2} upload={upload} />

      {offers.length === 0 ? (
        <div className="grid gap-4">
          <Notice
            role="note"
            tone="caution"
            icon={TriangleAlert}
            titleAs="h2"
            title={t("flow2.nothing.title")}
          >
            {t("flow2.nothing.body")}
          </Notice>
          {notOffered}
        </div>
      ) : (
        <section aria-labelledby={`${legend}-options`} className="grid gap-6 border-t pt-8">
          <h2 id={`${legend}-options`}>{t("flow2.options.title")}</h2>

          {offers.length > 1 ? (
            <fieldset className="grid gap-3">
              <legend className="mb-1 font-medium">{t("flow2.offer.legend")}</legend>
              <div className="grid gap-3 md:grid-cols-2">
                {offers.map((offer) => (
                  <label
                    key={offer}
                    className={cn(
                      "flex cursor-pointer items-start gap-3 rounded-lg border p-4 hover:bg-accent/60",
                      "has-[:focus-visible]:outline-2 has-[:focus-visible]:outline-offset-2 has-[:focus-visible]:outline-ring",
                      choice === offer && "border-primary bg-primary-soft hover:bg-primary-soft",
                    )}
                  >
                    <input
                      type="radio"
                      name={`${legend}-offer`}
                      className="mt-0.5 size-5 shrink-0 accent-primary outline-none"
                      checked={choice === offer}
                      onChange={() => setPicked(offer)}
                    />
                    <span className="grid gap-1">
                      <span className="font-medium">{t(`flow2.offer.${offer}` as TKey)}</span>
                      <span className="max-w-[65ch] text-sm text-ink-2">
                        {t(`flow2.offer.${offer}.body` as TKey)}
                      </span>
                    </span>
                  </label>
                ))}
              </div>
            </fieldset>
          ) : null}

          {notOffered}

          {choice === "merge" &&
            (upload.audio.length === 0 ? (
              <Notice role="alert" tone="error" icon={TriangleAlert} title={t("errors.no_audio")} />
            ) : (
              <MergePanel jobId={jobId} upload={upload} />
            ))}
          {choice === "version" && flow2.version && (
            <VersionForm jobId={jobId} offer={flow2.version} />
          )}
        </section>
      )}
    </div>
  );
}
