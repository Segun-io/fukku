import { TriangleAlert } from "lucide-react";
import { useId, useState } from "react";
import { usePreview } from "@/api/flow2";
import type { Flow2, Flow2File, Flow2Offer, JobFile, MergeRefusal } from "@/api/types";
import { ChoiceList } from "@/components/patterns/choice-list";
import { Notice } from "@/components/shell/notice";
import { type TKey, useT } from "@/i18n";
import { offerReasonText } from "@/lib/flow2-text";
import { audioLines, mergeRefusalText } from "@/lib/job-text";
import { Comparison } from "./comparison";
import { trackLabel } from "./labels";
import { MergeForm } from "./merge-form";
import { checkState, SyncCheck } from "./sync-check";
import { VersionForm } from "./version-form";

const offerOrder: readonly Flow2Offer[] = ["merge", "version"];

/**
 * A merge the worker refused for a reason that would repeat (#95): why, which choice it was,
 * and what to do. It stays above the sync check until another merge is confirmed.
 */
function RefusedMerge({ refusal, upload }: { refusal: MergeRefusal; upload: Flow2File }) {
  const { t, lang, formatNumber } = useT();
  const why = mergeRefusalText(refusal.code);
  const lines = audioLines(upload.audio, lang);
  const track = refusal.audioTracks
    .map((i) => {
      const line = lines[upload.audio.findIndex((a) => a.index === i)];
      return line ? trackLabel(t, line) : String(i);
    })
    .join(", ");
  return (
    <Notice
      role="alert"
      tone="error"
      icon={TriangleAlert}
      titleAs="h3"
      title={t("flow2.refusal.title")}
    >
      <p>{t(why.key, why.params)}</p>
      <p>{t("flow2.refusal.choice", { track, offset: formatNumber(refusal.offsetMs) })}</p>
      <p>{t("flow2.refusal.hint")}</p>
    </Notice>
  );
}

/** The merge path: the sync check first, and the merge confirmation once the user says they match. */
function MergePanel({
  jobId,
  upload,
  files,
  refusal,
}: {
  jobId: string;
  upload: Flow2File;
  files: JobFile[];
  refusal: MergeRefusal | null;
}) {
  const preview = usePreview(jobId, true);
  const [synced, setSynced] = useState<string | null>(null);
  const [track, setTrack] = useState<number | null>(null);
  const [offsetText, setOffsetText] = useState<string | null>(null);
  const data = preview.data;
  const ready = data?.state === "ready";
  // The merge is confirmed with the clips' track and offset: when the fields say something
  // else, the clips no longer show what would be merged, and the confirmation is withdrawn.
  const { stale } = checkState(upload, data, track, offsetText);
  const isSynced = ready && !stale && synced !== null && synced === data.requestedAt;
  return (
    <div className="grid gap-8">
      {refusal && <RefusedMerge refusal={refusal} upload={upload} />}
      <SyncCheck
        jobId={jobId}
        upload={upload}
        preview={data}
        synced={synced}
        onSynced={setSynced}
        track={track}
        offsetText={offsetText}
        onTrack={setTrack}
        onOffset={setOffsetText}
      />
      {isSynced && data && <MergeForm jobId={jobId} upload={upload} preview={data} files={files} />}
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
  files,
}: {
  jobId: string;
  flow2: Flow2;
  upload: Flow2File;
  files: JobFile[];
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
              reason: t(offerReasonText(n.reason).key, offerReasonText(n.reason).params),
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
            <ChoiceList
              legend={t("flow2.offer.legend")}
              value={choice ?? ""}
              onChange={(offer) => setPicked(offer as Flow2Offer)}
              choices={offers.map((offer) => ({
                value: offer,
                label: t(`flow2.offer.${offer}` as TKey),
                body: t(`flow2.offer.${offer}.body` as TKey),
              }))}
            />
          ) : null}

          {notOffered}

          {choice === "merge" &&
            (upload.audio.length === 0 ? (
              <Notice role="alert" tone="error" icon={TriangleAlert} title={t("errors.no_audio")} />
            ) : (
              <MergePanel
                jobId={jobId}
                upload={upload}
                files={files}
                refusal={flow2.mergeRefusal}
              />
            ))}
          {choice === "version" && flow2.version && (
            <VersionForm jobId={jobId} offer={flow2.version} />
          )}
        </section>
      )}
    </div>
  );
}
