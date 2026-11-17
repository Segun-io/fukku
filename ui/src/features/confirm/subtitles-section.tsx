import type { JobFile, SubtitleChoice } from "@/api/types";
import { inputClass } from "@/components/patterns/field";
import { useT } from "@/i18n";
import { guessSubtitle, twoLetterLanguage } from "@/lib/subtitle-name";

/** The server takes ISO 639-1 codes: two letters. */
export const isLanguage = (code: string) => /^[a-z]{2}$/.test(code);

/** The uploaded subtitle files that are usable, in the order of the job. */
export function subtitleFiles(files: readonly JobFile[]): JobFile[] {
  return files.filter((f) => f.kind === "subtitle" && f.status === "ok");
}

/** The starting point for each file: what its name says. */
export function guessChoices(files: readonly JobFile[]): SubtitleChoice[] {
  return files.map((f) => {
    const g = guessSubtitle(f.name);
    return {
      fileId: f.id,
      language: twoLetterLanguage(g.language),
      forced: g.forced,
      hearingImpaired: g.hearingImpaired,
    };
  });
}

/**
 * The choice for each file: the user's own where there is one, else what the name says. A file
 * that arrived after the page opened (the job is read again while it is open) gets its guess,
 * so every file shown has a choice.
 */
export function choicesFor(
  files: readonly JobFile[],
  edited: readonly SubtitleChoice[],
): SubtitleChoice[] {
  return files.flatMap((f) => {
    const mine = edited.find((c) => c.fileId === f.id);
    return mine ? [mine] : guessChoices([f]);
  });
}

type Props = {
  files: JobFile[];
  choices: SubtitleChoice[];
  onChange: (choices: SubtitleChoice[]) => void;
};

/**
 * The subtitle files uploaded with the video, each with its language and flags. The name
 * of the file gave the first guess; the user can change it. Shown only when a subtitle
 * manager is configured, because only then are the files handed on.
 */
export function SubtitlesSection({ files, choices, onChange }: Props) {
  const { t } = useT();
  if (files.length === 0) return null;

  function update(fileId: number, patch: Partial<SubtitleChoice>) {
    onChange(choices.map((c) => (c.fileId === fileId ? { ...c, ...patch } : c)));
  }

  return (
    <section aria-labelledby="confirm-subtitles" className="grid gap-3">
      <h2 id="confirm-subtitles">{t("confirm.subtitles.title")}</h2>
      <p className="max-w-[65ch] text-ink-2">{t("confirm.subtitles.intro")}</p>
      <ul className="divide-y border-y">
        {files.map((f) => {
          const choice = choices.find((c) => c.fileId === f.id);
          if (!choice) return null;
          return (
            <li
              key={f.id}
              className="grid gap-3 py-4 sm:grid-cols-[minmax(0,1fr)_auto] sm:items-end"
            >
              <div className="grid gap-2">
                <p translate="no" className="font-medium [overflow-wrap:anywhere]">
                  {f.name}
                </p>
                <label className="grid max-w-48 gap-1 text-sm text-muted-foreground">
                  {t("confirm.subtitles.language", { name: f.name })}
                  <input
                    type="text"
                    className={inputClass}
                    value={choice.language}
                    maxLength={2}
                    autoCapitalize="none"
                    autoComplete="off"
                    spellCheck={false}
                    aria-describedby={`sub-${f.id}-hint`}
                    onChange={(e) =>
                      update(f.id, {
                        language: e.target.value.toLowerCase().replace(/[^a-z]/g, ""),
                      })
                    }
                  />
                </label>
                <p id={`sub-${f.id}-hint`} className="text-sm text-muted-foreground">
                  {isLanguage(choice.language)
                    ? t("confirm.subtitles.guessHint")
                    : t("confirm.subtitles.noLanguage")}
                </p>
              </div>
              <div className="flex flex-wrap gap-x-6">
                <label className="flex min-h-11 items-center gap-2">
                  <input
                    type="checkbox"
                    className="size-5 accent-primary"
                    checked={choice.forced}
                    aria-label={t("confirm.subtitles.forced", { name: f.name })}
                    onChange={(e) => update(f.id, { forced: e.target.checked })}
                  />
                  {t("confirm.subtitles.forced.short")}
                </label>
                <label className="flex min-h-11 items-center gap-2">
                  <input
                    type="checkbox"
                    className="size-5 accent-primary"
                    checked={choice.hearingImpaired}
                    aria-label={t("confirm.subtitles.hi", { name: f.name })}
                    onChange={(e) => update(f.id, { hearingImpaired: e.target.checked })}
                  />
                  {t("confirm.subtitles.hi.short")}
                </label>
              </div>
            </li>
          );
        })}
      </ul>
    </section>
  );
}
