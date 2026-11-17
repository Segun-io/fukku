import { Link, useNavigate } from "@tanstack/react-router";
import { Info, TriangleAlert } from "lucide-react";
import { useId, useRef, useState } from "react";
import { ApiError } from "@/api/client";
import { useConfirmFlow2 } from "@/api/flow2";
import { useSession } from "@/api/session";
import type { VersionOffer } from "@/api/types";
import { ErrorMessage } from "@/components/error-message";
import { ConfirmStep } from "@/components/patterns/confirm-step";
import { inputClass } from "@/components/patterns/field";
import { Notice } from "@/components/shell/notice";
import { useT } from "@/i18n";
import { problemText, versionNoticeText } from "@/lib/flow2-text";

/**
 * The second-version confirmation of flow 2b: the label (proposed, and the user may change
 * it), the naming problem of the proposed label with its fix, how Radarr and the media server
 * treat the version, and the button. The server judges the label (it asks Radarr), so a
 * refusal (`version_label`) shows under the field.
 */
export function VersionForm({ jobId, offer }: { jobId: string; offer: VersionOffer }) {
  const { t } = useT();
  const id = useId();
  const navigate = useNavigate();
  const session = useSession();
  const confirm = useConfirmFlow2(jobId);
  const [label, setLabel] = useState(offer.label);
  const field = useRef<HTMLInputElement>(null);

  const proposed = label.trim() === offer.label;
  const problem = proposed ? offer.problem : null;
  const shown = problemText(problem?.reason ?? "", problem?.token ?? "");
  const admin = session.data?.user.admin === true;
  const err = confirm.error;
  const refused = err instanceof ApiError && err.code === "version_label";
  const changed = err instanceof ApiError && err.code === "library_changed";

  function submit() {
    const chosen = label.trim();
    confirm.mutate(
      { flow2: "version", version: chosen === "" ? {} : { label: chosen } },
      {
        onSuccess: () => void navigate({ to: "/jobs/$id", params: { id: jobId } }),
        onError: (e) => {
          if (e instanceof ApiError && e.code === "version_label") field.current?.focus();
        },
      },
    );
  }

  return (
    <section aria-labelledby={`${id}-title`} className="grid gap-6">
      <div className="grid gap-2">
        <h3 id={`${id}-title`}>{t("flow2.version.title")}</h3>
        <p className="max-w-[65ch] text-ink-2">{t("flow2.version.intro")}</p>
      </div>

      <div className="grid gap-1 sm:max-w-md">
        <label htmlFor={`${id}-label`} className="font-medium">
          {t("flow2.version.label")}
        </label>
        <input
          ref={field}
          id={`${id}-label`}
          name="label"
          type="text"
          autoComplete="off"
          spellCheck={false}
          translate="no"
          className={inputClass}
          value={label}
          aria-invalid={problem || refused ? true : undefined}
          aria-describedby={[
            `${id}-label-hint`,
            problem ? `${id}-problem` : "",
            refused ? `${id}-label-error` : "",
          ]
            .filter(Boolean)
            .join(" ")}
          onChange={(e) => {
            setLabel(e.target.value);
            confirm.reset();
          }}
        />
        <p id={`${id}-label-hint`} className="max-w-[65ch] text-sm text-muted-foreground">
          {t("flow2.version.label.hint")}
        </p>
        {proposed && offer.name && (
          <p translate="no" className="text-sm text-ink-2 [overflow-wrap:anywhere]">
            {t("flow2.version.name", { name: offer.name })}
          </p>
        )}
      </div>

      {problem && (
        <Notice
          id={`${id}-problem`}
          role="alert"
          tone="caution"
          icon={TriangleAlert}
          titleAs="h4"
          title={t("flow2.version.problem.title")}
        >
          <p>{t(shown.key, shown.params)}</p>
          {admin && <p className="mt-2">{t("flow2.version.problem.admin")}</p>}
        </Notice>
      )}

      {offer.notices.length > 0 && (
        <Notice
          role="note"
          tone="waiting"
          icon={Info}
          titleAs="h4"
          title={t("flow2.version.notices")}
        >
          <ul className="grid list-disc gap-1.5 pl-5">
            {offer.notices.map((code) => (
              <li key={code}>{t(versionNoticeText(code).key, versionNoticeText(code).params)}</li>
            ))}
          </ul>
        </Notice>
      )}

      <ConfirmStep
        as="h4"
        title={t("flow2.version.confirm.title")}
        facts={[
          {
            term: t("flow2.version.fact.label"),
            value: <span translate="no">{label.trim() || "—"}</span>,
          },
          ...(proposed && offer.name
            ? [
                {
                  term: t("flow2.version.fact.name"),
                  value: <span translate="no">{offer.name}</span>,
                },
              ]
            : []),
          { term: t("flow2.version.fact.where"), value: t("flow2.version.fact.where.value") },
          { term: t("flow2.version.fact.server"), value: t("flow2.version.fact.server.value") },
        ]}
        confirmLabel={t("flow2.version.submit")}
        onConfirm={submit}
        busy={confirm.isPending}
      />

      {err && (
        <div>
          <ErrorMessage error={err} id={refused ? `${id}-label-error` : undefined} />
          {changed && (
            <p>
              <Link
                to="/jobs/$id/match"
                params={{ id: jobId }}
                className="inline-flex min-h-11 items-center underline underline-offset-4"
              >
                {t("flow2.changed.action")}
              </Link>
            </p>
          )}
        </div>
      )}
    </section>
  );
}
