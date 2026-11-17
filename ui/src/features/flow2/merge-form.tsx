import { Link, useNavigate } from "@tanstack/react-router";
import { CircleAlert } from "lucide-react";
import { useEffect, useId, useRef, useState } from "react";
import { ApiError } from "@/api/client";
import { useConfirmFlow2 } from "@/api/flow2";
import { useSession } from "@/api/session";
import type { Flow2File, JobFile, Preview, SubtitleChoice } from "@/api/types";
import { ErrorMessage } from "@/components/error-message";
import { ConfirmStep } from "@/components/patterns/confirm-step";
import { inputClass } from "@/components/patterns/field";
import {
  guessChoices,
  isLanguage,
  SubtitlesSection,
  subtitleFiles,
} from "@/features/confirm/subtitles-section";
import { useT } from "@/i18n";
import { audioLines, subtitleLines } from "@/lib/job-text";
import { trackLabel } from "./labels";

type Props = {
  jobId: string;
  upload: Flow2File;
  /** The ready preview the user said matches: its offset and track are the merge's. */
  preview: Preview;
  /** The job's files: its subtitle files go with the merge, as with an import. */
  files: JobFile[];
};

/** The server takes a canonical BCP 47 tag (it refuses `es_419` and rewrites `iw`). */
export function isCanonicalTag(tag: string): boolean {
  try {
    return Intl.getCanonicalLocales(tag)[0] === tag;
  } catch {
    return false;
  }
}

/**
 * The merge confirmation of flow 2a: which tracks are added, the language tag and track name,
 * and what will happen to the library file and to the title's monitoring (B6). Everything
 * the user leaves empty is left to the server's settings, which is why the fields start
 * empty and a field that was not touched is not sent.
 */
export function MergeForm({ jobId, upload, preview, files }: Props) {
  const { t, lang } = useT();
  const id = useId();
  const navigate = useNavigate();
  const confirm = useConfirmFlow2(jobId);

  const [subtitleTracks, setSubtitleTracks] = useState<number[]>([]);
  const [tag, setTag] = useState("");
  const [name, setName] = useState("");
  // Shown as the plan's default and always sent as shown, so the page never says one thing and does another.
  const [keepDefault, setKeepDefault] = useState(true);
  const [tagInvalid, setTagInvalid] = useState(false);
  const session = useSession();
  // The subtitle files of the upload are handed to the subtitle manager, for the merged file.
  const subtitleManager = session.data?.subtitleManager === true;
  const subFiles = subtitleManager ? subtitleFiles(files) : [];
  const [choices, setChoices] = useState<SubtitleChoice[]>(() => guessChoices(subFiles));
  const [badLanguage, setBadLanguage] = useState(false);
  const heading = useRef<HTMLHeadingElement>(null);
  // "They match" opened this form: the focus goes to it, where the next step is.
  useEffect(() => heading.current?.focus(), []);

  const subs = subtitleLines(upload.subtitles, lang);
  const audio = audioLines(upload.audio, lang).find((a) => a.key === `a${preview.audioTrack}`);
  const audioLine = audio ? trackLabel(t, audio) : String(preview.audioTrack);
  const offset = preview.offsetMs ?? 0;
  const fallback = t("flow2.merge.fact.default");

  function submit() {
    if (tag.trim() !== "" && !isCanonicalTag(tag.trim())) {
      setTagInvalid(true);
      document.getElementById(`${id}-tag`)?.focus();
      return;
    }
    if (subtitleManager && choices.some((c) => !isLanguage(c.language))) {
      setBadLanguage(true);
      return;
    }
    confirm.mutate(
      {
        flow2: "merge",
        merge: {
          audioTracks: preview.audioTrack === null ? undefined : [preview.audioTrack],
          subtitleTracks,
          offsetMs: offset,
          ...(tag.trim() !== "" ? { languageTag: tag.trim() } : {}),
          ...(name.trim() !== "" ? { trackName: name.trim() } : {}),
          keepOriginalDefault: keepDefault,
        },
        ...(subtitleManager && subFiles.length > 0 ? { subtitles: choices } : {}),
      },
      { onSuccess: () => void navigate({ to: "/jobs/$id", params: { id: jobId } }) },
    );
  }

  const err = confirm.error;
  const changed = err instanceof ApiError && err.code === "library_changed";

  return (
    <section aria-labelledby={`${id}-title`} className="grid gap-6">
      <div className="grid gap-2">
        <h3 id={`${id}-title`} ref={heading} tabIndex={-1}>
          {t("flow2.merge.title")}
        </h3>
        <p className="max-w-[65ch] text-ink-2">{t("flow2.merge.intro")}</p>
      </div>

      <div className="grid gap-5 sm:max-w-md">
        <fieldset className="grid gap-1">
          <legend className="font-medium">{t("flow2.merge.subtitles")}</legend>
          {subs.length === 0 ? (
            <p className="text-ink-2">{t("flow2.merge.subtitles.none")}</p>
          ) : (
            <>
              {subs.map((s, i) => {
                const index = upload.subtitles[i]?.index ?? i;
                const language = s.language ?? t("job.probe.noLanguage");
                return (
                  <label key={s.key} className="flex min-h-11 items-center gap-3">
                    <input
                      type="checkbox"
                      name="subtitleTracks"
                      className="size-5 shrink-0 accent-primary"
                      checked={subtitleTracks.includes(index)}
                      onChange={(e) =>
                        setSubtitleTracks((cur) =>
                          e.target.checked ? [...cur, index] : cur.filter((x) => x !== index),
                        )
                      }
                    />
                    <span>
                      {language}
                      {s.title && <span translate="no"> “{s.title}”</span>}
                      <span className="figure ml-2 text-sm text-muted-foreground">{s.detail}</span>
                    </span>
                  </label>
                );
              })}
              <p className="text-sm text-muted-foreground">{t("flow2.merge.subtitles.hint")}</p>
            </>
          )}
        </fieldset>

        <div className="grid gap-1">
          <label htmlFor={`${id}-tag`} className="text-sm text-muted-foreground">
            {t("flow2.merge.tag")}
          </label>
          <input
            id={`${id}-tag`}
            name="languageTag"
            type="text"
            autoComplete="off"
            spellCheck={false}
            className={inputClass}
            value={tag}
            aria-invalid={tagInvalid ? true : undefined}
            aria-describedby={`${id}-tag-hint`}
            onChange={(e) => {
              setTag(e.target.value);
              setTagInvalid(false);
            }}
          />
          {tagInvalid && (
            <p role="alert" className="flex items-start gap-2 text-sm text-destructive">
              <CircleAlert aria-hidden="true" className="mt-0.5 size-4 shrink-0" />
              {t("flow2.merge.tag.invalid")}
            </p>
          )}
          <p id={`${id}-tag-hint`} className="text-sm text-muted-foreground">
            {t("flow2.merge.tag.hint")}
          </p>
        </div>

        <div className="grid gap-1">
          <label htmlFor={`${id}-name`} className="text-sm text-muted-foreground">
            {t("flow2.merge.name")}
          </label>
          <input
            id={`${id}-name`}
            name="trackName"
            type="text"
            autoComplete="off"
            className={inputClass}
            value={name}
            aria-describedby={`${id}-name-hint`}
            onChange={(e) => setName(e.target.value)}
          />
          <p id={`${id}-name-hint`} className="text-sm text-muted-foreground">
            {t("flow2.merge.name.hint")}
          </p>
        </div>

        <label className="flex min-h-11 items-center gap-3">
          <input
            type="checkbox"
            name="keepOriginalDefault"
            className="size-5 shrink-0 accent-primary"
            checked={keepDefault}
            onChange={(e) => setKeepDefault(e.target.checked)}
          />
          {t("flow2.merge.keepDefault")}
        </label>
      </div>

      {subFiles.length > 0 && (
        <SubtitlesSection files={subFiles} choices={choices} onChange={setChoices} />
      )}
      {badLanguage && choices.some((c) => !isLanguage(c.language)) && (
        <p role="alert" className="flex items-start gap-2 text-destructive">
          <CircleAlert aria-hidden="true" className="mt-0.5 size-5 shrink-0" />
          {t("confirm.subtitles.invalid")}
        </p>
      )}

      <ConfirmStep
        title={t("flow2.merge.confirm.title")}
        facts={[
          { term: t("flow2.merge.fact.audio"), value: audioLine },
          {
            term: t("flow2.merge.fact.offset"),
            value: t("import.merge.fact.offset.value", { offset: String(offset) }),
            figure: true,
          },
          {
            term: t("flow2.merge.fact.subtitles"),
            value:
              subtitleTracks.length === 0
                ? t("flow2.merge.fact.subtitles.none")
                : String(subtitleTracks.length),
            figure: true,
          },
          { term: t("flow2.merge.fact.tag"), value: tag.trim() || fallback },
          { term: t("flow2.merge.fact.name"), value: name.trim() || fallback },
          { term: t("flow2.merge.fact.file"), value: t("flow2.merge.fact.file.value") },
          { term: t("flow2.merge.fact.title"), value: t("flow2.merge.fact.title.value") },
        ]}
        warnings={[t("flow2.merge.warning.replace"), t("flow2.merge.warning.search")]}
        confirmLabel={t("flow2.merge.submit")}
        onConfirm={submit}
        busy={confirm.isPending}
      />

      {err && (
        <div>
          <ErrorMessage error={err} />
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
