import { Link, useNavigate } from "@tanstack/react-router";
import { LoaderCircle, TriangleAlert } from "lucide-react";
import { useRef, useState } from "react";
import { ApiError } from "@/api/client";
import { useConfirm, useEditRow } from "@/api/confirm";
import type { Proposal } from "@/api/proposal";
import type { JobDetail, SubtitleChoice } from "@/api/types";
import { ErrorMessage } from "@/components/error-message";
import { Button } from "@/components/ui/button";
import { useT } from "@/i18n";
import { hasIssues, hintText, issueSignature } from "./notices";
import { RowCard } from "./row-card";
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

  const app = appName[proposal.app];
  const titleText = proposal.target
    ? t("confirm.title.name", { title: proposal.target.title, year: String(proposal.target.year) })
    : (job.target?.title ?? "");
  const rows = proposal.rows;
  const leftIn = rows.filter((r) => !r.skip);
  const overridable = (id: string) => {
    const row = rows.find((r) => r.id === id);
    return !!row && !row.skip && hasIssues(row) && agreed[id] === issueSignature(row);
  };
  const needTick = leftIn.filter((r) => hasIssues(r) && !overridable(r.id));
  const checkingId = edit.isPending ? edit.variables?.rowId : undefined;

  function submit(e: React.FormEvent) {
    e.preventDefault();
    if (confirm.isPending || edit.isPending || leftIn.length === 0) return;
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
        overrides: leftIn.filter((r) => hasIssues(r)).map((r) => r.id),
        ...(subtitleManager ? { subtitles: choices } : {}),
      },
      { onSuccess: () => void navigate({ to: "/jobs/$id", params: { id: job.id } }) },
    );
  }

  const stale = confirm.error instanceof ApiError && confirm.error.code === "needs_override";

  return (
    <form
      ref={form}
      noValidate
      onSubmit={submit}
      className="grid gap-8"
      aria-busy={confirm.isPending}
    >
      <p className="max-w-[65ch] text-ink-2">{t("confirm.intro", { app })}</p>

      <section aria-labelledby="confirm-target" className="grid gap-1">
        <h2 id="confirm-target" className="text-sm font-normal text-muted-foreground">
          {t("confirm.target", { app })}
        </h2>
        <p translate="no" className="text-xl font-medium [overflow-wrap:anywhere]">
          {titleText}
        </p>
      </section>

      {proposal.hints.map((h) => (
        <p key={h.code} role="note" className="max-w-[65ch] border-l-2 border-primary pl-4">
          {t(hintText(h.code).key, hintText(h.code).params)}
        </p>
      ))}

      <section aria-labelledby="confirm-rows" className="grid gap-1">
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

      <div className="grid gap-3">
        {leftIn.length === 0 && (
          <p role="status" className="max-w-[65ch] text-ink-2">
            {t("confirm.nothing")}
          </p>
        )}
        {missing.length > 0 && needTick.length > 0 && (
          <p role="alert" className="flex items-center gap-2 text-destructive">
            <TriangleAlert aria-hidden="true" className="size-5 shrink-0" />
            {t("confirm.override.missing")}
          </p>
        )}
        <div className="flex flex-col gap-2 sm:flex-row sm:flex-wrap">
          <Button
            type="submit"
            size="lg"
            disabled={leftIn.length === 0}
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
          <p role="alert" className="flex items-center gap-2 text-destructive">
            <TriangleAlert aria-hidden="true" className="size-5 shrink-0" />
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
