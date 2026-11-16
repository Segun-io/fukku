import { ChevronDown, Globe } from "lucide-react";
import { useId } from "react";
import { useT } from "./provider";
import { isLang } from "./translate";

const names: Record<string, string> = { en: "English", es: "Español" };

/**
 * The language switch of the header: a native select, so it works with a keyboard, a screen
 * reader and the phone's own picker, and still fits when a third language arrives. Names are
 * in their own language. Its look is described in docs/design.md.
 */
export function LanguageSwitch() {
  const { lang, available, setLang, t } = useT();
  const id = useId();
  return (
    <div className="relative inline-flex items-center text-foreground">
      <label htmlFor={id} className="sr-only">
        {t("shell.language")}
      </label>
      <Globe
        aria-hidden="true"
        className="pointer-events-none absolute left-2.5 size-4 text-muted-foreground"
      />
      <select
        id={id}
        name="language"
        autoComplete="off"
        value={lang}
        onChange={(e) => {
          if (isLang(e.target.value)) setLang(e.target.value);
        }}
        className="h-9 cursor-pointer appearance-none rounded-md border border-input bg-card pr-8 pl-8 text-base font-medium text-foreground shadow-xs hover:bg-accent pointer-coarse:h-11 sm:text-sm"
      >
        {available.map((l) => (
          <option key={l} value={l} lang={l}>
            {names[l] ?? l}
          </option>
        ))}
      </select>
      <ChevronDown
        aria-hidden="true"
        className="pointer-events-none absolute right-2.5 size-4 text-muted-foreground"
      />
    </div>
  );
}
