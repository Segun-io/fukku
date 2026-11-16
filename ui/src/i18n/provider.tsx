import {
  createContext,
  type ReactNode,
  useCallback,
  useContext,
  useEffect,
  useMemo,
  useState,
} from "react";
import { useSession } from "@/api/session";
import {
  formatBytes,
  formatDate,
  formatDuration,
  formatNumber,
  isLang,
  type Lang,
  langStorageKey,
  languages,
  resolveLanguage,
  type TFunction,
  translate,
} from "./translate";

type I18n = {
  lang: Lang;
  /** The languages the user can switch to (the server's `uiLanguages` that we have). */
  available: readonly Lang[];
  setLang: (lang: Lang) => void;
  t: TFunction;
  formatBytes: (bytes: number) => string;
  formatDuration: (ms: number) => string;
  formatDate: (value: string | Date) => string;
  formatNumber: (n: number, options?: Intl.NumberFormatOptions) => string;
};

const I18nContext = createContext<I18n | null>(null);

function readSaved(): string | null {
  try {
    return localStorage.getItem(langStorageKey);
  } catch {
    return null; // storage blocked: the choice lasts until the page is closed
  }
}

/** Must be inside the `QueryClientProvider`: the server's `uiLanguages` come from the session. */
export function I18nProvider({ children }: { children: ReactNode }) {
  const session = useSession();
  const [saved, setSaved] = useState<string | null>(readSaved);

  const serverLangs = session.data?.uiLanguages;
  const available = useMemo(() => {
    const offered = serverLangs?.filter(isLang) ?? [];
    return offered.length > 0 ? offered : languages;
  }, [serverLangs]);

  const lang = resolveLanguage({
    saved,
    browser: navigator.languages?.length ? navigator.languages : [navigator.language],
    allowed: available,
    serverFirst: serverLangs?.[0],
  });

  useEffect(() => {
    document.documentElement.lang = lang;
  }, [lang]);

  const setLang = useCallback((next: Lang) => {
    setSaved(next);
    try {
      localStorage.setItem(langStorageKey, next);
    } catch {
      // not saved, still applied
    }
  }, []);

  const value = useMemo<I18n>(
    () => ({
      lang,
      available,
      setLang,
      t: (key, params) => translate(lang, key, params),
      formatBytes: (bytes) => formatBytes(bytes, lang),
      formatDuration: (ms) => formatDuration(ms, lang),
      formatDate: (v) => formatDate(v, lang),
      formatNumber: (n, options) => formatNumber(n, lang, options),
    }),
    [lang, available, setLang],
  );

  return <I18nContext.Provider value={value}>{children}</I18nContext.Provider>;
}

export function useT(): I18n {
  const ctx = useContext(I18nContext);
  if (!ctx) throw new Error("useT must be used inside <I18nProvider>");
  return ctx;
}
