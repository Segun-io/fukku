import { Link, useNavigate } from "@tanstack/react-router";
import { Ban, CircleAlert, ListTree, LoaderCircle } from "lucide-react";
import { useRef, useState } from "react";
import { ApiError } from "@/api/client";
import { useConfirm, useEditRow } from "@/api/confirm";
import type { Proposal } from "@/api/proposal";
import type { JobDetail, SubtitleChoice } from "@/api/types";
import { ErrorMessage } from "@/components/error-message";
import { StatusLabel } from "@/components/patterns/status-label";
import { Notice } from "@/components/shell/notice";
import { Button } from "@/components/ui/button";
import { useT } from "@/i18n";
import { EpisodeVersionsSection, isVersionRefusal, type VersionRefusal } from "./episode-versions";
import { hasIssues, hintText, issueSignature } from "./notices";
import { RowCard } from "./row-card";
import { hasExistingFile, replacedRow, SeasonDubSection } from "./season-dub";
import { guessChoices, isLanguage, SubtitlesSection, subtitleFiles } from "./subtitles-section";

const appName: Record<Proposal["app"], string> = {
  radarr: "Radarr",
  sonarr: "Sonarr",
  "": "Radarr or Sonarr",
};

type Props = { job: JobDetail; proposal: Proposal; subtitleManager: boolean };

/**
 * The confirmation of flow 1: each file next to the title it goes to, what the app thinks
 * of it, what is against importing it, and the button. A row with a rejection or a warning
 * needs its own "import anyway" tick; the request lists those rows in `overrides`.
 */
export function ConfirmView({ job, proposal, subtitleManager }: Props) {
  const { t } = useT();
  const navigate = useNavigate();
  const edit = useEditRow(job.id);
  const confirm = useConfirm(job.id);
  const form = useRef<HTMLFormElement>(null);

  const subs = subtitleManager ? subtitleFiles(job.files) : [];
  const [choices, setChoices] = useState<SubtitleChoice[]>(() => guessChoices(subs));
  // The issues each tick was given for (`issueSignature`).
  const [agreed, setAgreed] = useState<Record<string, string>>({});
  const [missing, setMissing] = useState<string[]>([]);
  const [badLanguage, setBadLanguage] = useState(false);
  // The season-dub choice (#57): asked when the server's policy is `ask`; else it is the server's.
  const dub = proposal.seasonDub;
  const [choice, setChoice] = useState("");
  const [dubMissing, setDubMissing] = useState(false);
  // Rows whose file holds episodes outside the upload (`shared_file`), and the ones agreed to.
  const [sharedRows, setSharedRows] = useState<string[]>([]);
  const [sharedAgreed, setSharedAgreed] = useState<string[]>([]);
  // The label of the second versions (`version`, #58): the proposed one until the user types.
  const [label, setLabel] = useState(dub?.version?.label ?? "");
  const [refusal, setRefusal] = useState<VersionRefusal | null>(null);

  const app = appName[proposal.app];
  const titleText = proposal.target
    ? t("confirm.title.name", { title: proposal.target.title, year: String(proposal.target.year) })
    : (job.target?.title ?? "");
  const policy = dub?.ask ? choice : (dub?.decided ?? "");
  // With `replace`, the rows that have a file are in, and the choice answers what the app says
  // of replacing it. With `subtitlesOnly` they stay out and the subtitles are all that moves.
  const rows = proposal.rows.map((r) =>
    policy === "replace" && hasExistingFile(r) ? replacedRow(r) : r,
  );
  const leftIn = rows.filter((r) => !r.skip);
  const subtitlesOnly = policy === "subtitlesOnly";
  // With `version` the rows that have a file stay out of the import and are kept next to their
  // episode's file: the server lists them in `seasonDub.version`. They count as a result too.
  const isVersion = policy === "version";
  const versionRows = isVersion ? (dub?.version?.rows ?? []) : [];
  const nothingNew = subtitlesOnly || isVersion;
  const overridable = (id: string) => {
    const row = rows.find((r) => r.id === id);
    return !!row && !row.skip && hasIssues(row) && agreed[id] === issueSignature(row);
  };
  const needTick = leftIn.filter((r) => hasIssues(r) && !overridable(r.id));
  const checkingId = edit.isPending ? edit.variables?.rowId : undefined;

  function submit(e: React.FormEvent) {
    e.preventDefault();
    if (confirm.isPending || edit.isPending || (leftIn.length === 0 && !nothingNew)) return;
    if (dub?.ask && choice === "") {
      setDubMissing(true);
      form.current?.querySelector<HTMLInputElement>("[data-policy]")?.focus();
      return;
    }
    if (subtitleManager && choices.some((c) => !isLanguage(c.language))) {
      setBadLanguage(true);
      form.current?.querySelector<HTMLInputElement>('input[type="text"]')?.focus();
      return;
    }
    if (needTick.length > 0) {
      setMissing(needTick.map((r) => r.id));
      const first = needTick[0];
      form.current?.querySelector<HTMLInputElement>(`[data-override="${first?.id}"]`)?.focus();
      return;
    }
    confirm.mutate(
      {
        overrides: [
          ...leftIn.filter((r) => hasIssues(r)).map((r) => r.id),
          ...sharedAgreed.filter((id) => leftIn.some((r) => r.id === id && !hasIssues(r))),
        ],
        ...(subtitleManager ? { subtitles: choices } : {}),
        ...(dub?.ask ? { seasonDub: choice } : {}),
        ...(isVersion ? { version: label.trim() === "" ? {} : { label: label.trim() } } : {}),
      },
      {
        onSuccess: () => void navigate({ to: "/jobs/$id", params: { id: job.id } }),
        onError: (err) => {
          if (!(err instanceof ApiError)) return;
          if (err.code === "shared_file") setSharedRows(err.rows);
          if (isVersionRefusal(err.code)) setRefusal({ code: err.code, rows: err.rows });
        },
      },
    );
  }

  const stale = confirm.error instanceof ApiError && confirm.error.code === "needs_override";

  return (
    <form
      ref={form}
      noValidate
      onSubmit={submit}
      className="grid gap-10"
      aria-busy={confirm.isPending}
    >
      <section aria-labelledby="confirm-target" className="grid gap-1 border-y py-4">
        <h2
          id="confirm-target"
          className="font-sans text-sm font-normal tracking-normal text-muted-foreground"
        >
          {t("confirm.target", { app })}
        </h2>
        <p translate="no" className="text-lg font-medium [overflow-wrap:anywhere]">
          {titleText}
        </p>
      </section>

      {proposal.hints.map((h) =>
        h.code === "dvd_order" ? (
          <Notice
            key={h.code}
            role="note"
            tone="caution"
            icon={ListTree}
            title={t("mapping.hint.dvd_order.title")}
          >
            {t(hintText(h.code).key, hintText(h.code).params)}
          </Notice>
        ) : (
          <Notice
            key={h.code}
            role="note"
            tone="caution"
            icon={ListTree}
            title={t(hintText(h.code).key, hintText(h.code).params)}
          />
        ),
      )}

      {dub && (
        <SeasonDubSection
          dub={dub}
          app={app}
          choice={choice}
          onChoice={(p) => {
            setChoice(p);
            setDubMissing(false);
            setSharedRows([]);
            setSharedAgreed([]);
            setRefusal(null);
            confirm.reset();
          }}
          missing={dubMissing}
          shared={rows.filter((r) => sharedRows.includes(r.id))}
          agreed={sharedAgreed}
          onAgree={(id, on) =>
            setSharedAgreed((cur) => (on ? [...cur, id] : cur.filter((x) => x !== id)))
          }
        />
      )}

      {isVersion && (
        <EpisodeVersionsSection
          view={dub?.version}
          rows={proposal.rows}
          label={label}
          onLabel={(value) => {
            setLabel(value);
            setRefusal(null);
            confirm.reset();
          }}
          labelRefused={confirm.error instanceof ApiError && confirm.error.code === "version_label"}
          refusal={refusal}
        />
      )}

      <section aria-labelledby="confirm-rows" className="grid gap-3">
        <h2 id="confirm-rows">{t("confirm.rows.title", { count: rows.length })}</h2>
        <ul className="divide-y border-y">
          {rows.map((row) => (
            <RowCard
              key={row.id}
              row={row}
              proposal={proposal}
              file={job.files.find((f) => f.id === row.fileId)}
              title={titleText}
              checking={checkingId === row.id}
              overridden={overridable(row.id)}
              missing={missing.includes(row.id)}
              onOverride={(value) => {
                setAgreed((a) => {
                  const next = { ...a };
                  if (value) next[row.id] = issueSignature(row);
                  else delete next[row.id];
                  return next;
                });
                setMissing((m) => m.filter((id) => id !== row.id));
              }}
              onEdit={(patch) => edit.mutate({ rowId: row.id, edit: patch })}
            />
          ))}
        </ul>
        {edit.isError && <ErrorMessage error={edit.error} />}
      </section>

      {subtitleManager && <SubtitlesSection files={subs} choices={choices} onChange={setChoices} />}

      <div className="grid gap-4">
        {leftIn.length === 0 ? (
          <p role="status" className="max-w-[65ch] text-ink-2">
            {isVersion
              ? t("confirm.version.only", { count: versionRows.length })
              : t(subtitlesOnly ? "confirm.dub.subtitlesOnly.count" : "confirm.nothing")}
          </p>
        ) : (
          <ul aria-label={t("confirm.count.label")} className="flex flex-wrap gap-x-6 gap-y-1">
            <li>
              <StatusLabel tone="done" className="text-base">
                {t("confirm.count.in", { count: leftIn.length })}
              </StatusLabel>
            </li>
            {versionRows.length > 0 && (
              <li>
                <StatusLabel tone="done" className="text-base">
                  {t("confirm.version.count", { count: versionRows.length })}
                </StatusLabel>
              </li>
            )}
            {rows.length - versionRows.length > leftIn.length && (
              <li>
                <StatusLabel tone="waiting" icon={Ban} className="text-base">
                  {t("confirm.count.out", {
                    count: rows.length - versionRows.length - leftIn.length,
                  })}
                </StatusLabel>
              </li>
            )}
          </ul>
        )}
        {missing.length > 0 && needTick.length > 0 && (
          <p role="alert" className="flex items-start gap-2 text-destructive">
            <CircleAlert aria-hidden="true" className="mt-0.5 size-5 shrink-0" />
            {t("confirm.override.missing")}
          </p>
        )}
        <div className="flex flex-col gap-2 sm:flex-row sm:flex-wrap">
          <Button
            type="submit"
            size="lg"
            disabled={leftIn.length === 0 && !nothingNew}
            aria-disabled={confirm.isPending}
            aria-busy={confirm.isPending}
          >
            {confirm.isPending && <LoaderCircle aria-hidden="true" className="animate-spin" />}
            {t("confirm.submit")}
          </Button>
          <Button asChild size="lg" variant="ghost">
            <Link to="/jobs/$id/match" params={{ id: job.id }}>
              {t("confirm.change")}
            </Link>
          </Button>
        </div>
        {badLanguage && subtitleManager && choices.some((c) => !isLanguage(c.language)) && (
          <p role="alert" className="flex items-start gap-2 text-destructive">
            <CircleAlert aria-hidden="true" className="mt-0.5 size-5 shrink-0" />
            {t("confirm.subtitles.invalid")}
          </p>
        )}
        {confirm.isError && (
          <div>
            <ErrorMessage error={confirm.error} />
            {stale && <p className="text-sm text-ink-2">{t("confirm.override.stale")}</p>}
          </div>
        )}
      </div>
    </form>
  );
}
