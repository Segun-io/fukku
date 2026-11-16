import { useT } from "./provider";
import { isLang } from "./translate";

const names: Record<string, string> = { en: "English", es: "Español" };

/** The language switch of the header. Names are in their own language. */
export function LanguageSwitch() {
  const { lang, available, setLang, t } = useT();
  return (
    <select
      aria-label={t("shell.language")}
      value={lang}
      onChange={(e) => {
        if (isLang(e.target.value)) setLang(e.target.value);
      }}
      className="rounded-md border bg-background px-2 py-1 text-sm"
    >
      {available.map((l) => (
        <option key={l} value={l}>
          {names[l] ?? l}
        </option>
      ))}
    </select>
  );
}
