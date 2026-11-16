// Pure translation and formatting, no React: the provider in `index.tsx` binds these to the
// current language.

import en from "./en";
import es from "./es";

export const languages = ["en", "es"] as const;
export type Lang = (typeof languages)[number];

export const catalogues: Record<Lang, Record<string, string>> = { en, es };

type PluralSuffix = "zero" | "one" | "two" | "few" | "many" | "other";
type Keys = keyof typeof en;
type PluralBase<K> = K extends `${infer B}.${PluralSuffix}` ? B : never;

/** Every key `t()` accepts: the catalogue's keys, and the base of each plural group. */
export type TKey = Keys | PluralBase<Keys>;
export type TParams = Record<string, string | number>;
export type TFunction = (key: TKey, params?: TParams) => string;

export function isLang(value: string): value is Lang {
  return (languages as readonly string[]).includes(value);
}

/** Under test a missing key is a bug to fail on; in the app it shows the key instead. */
const strict = import.meta.env.MODE === "test";

export function translate(lang: Lang, key: string, params?: TParams): string {
  const cat = catalogues[lang];
  let text: string | undefined;
  if (typeof params?.count === "number") {
    const rule = new Intl.PluralRules(lang).select(params.count);
    text = cat[`${key}.${rule}`] ?? cat[`${key}.other`];
  }
  text ??= cat[key];
  if (text === undefined) {
    if (strict) throw new Error(`i18n: no "${key}" in the ${lang} catalogue`);
    console.warn(`i18n: no "${key}" in the ${lang} catalogue`);
    return key;
  }
  return text.replace(/\{(\w+)\}/g, (whole, name: string) => {
    const value = params?.[name];
    if (value === undefined) {
      if (strict) throw new Error(`i18n: "${key}" needs the parameter "${name}"`);
      return whole;
    }
    return typeof value === "number" ? formatNumber(value, lang) : value;
  });
}

// ---- Formatting (Intl) ----------------------------------------------------------------

export function formatNumber(n: number, lang: Lang, options?: Intl.NumberFormatOptions): string {
  return new Intl.NumberFormat(lang, options).format(n);
}

const byteUnits = ["byte", "kilobyte", "megabyte", "gigabyte", "terabyte", "petabyte"] as const;

/** Sizes in decimal units (1 GB = 1000 MB), as file managers and download pages show them. */
export function formatBytes(bytes: number, lang: Lang): string {
  let value = Math.max(0, bytes);
  let i = 0;
  while (value >= 1000 && i < byteUnits.length - 1) {
    value /= 1000;
    i++;
  }
  return new Intl.NumberFormat(lang, {
    style: "unit",
    unit: byteUnits[i],
    unitDisplay: "short",
    maximumFractionDigits: i === 0 || value >= 100 ? 0 : value >= 10 ? 1 : 2,
  }).format(value);
}

/** "1 h 30 min", "45 s": the two largest non-zero units of a duration in milliseconds. */
export function formatDuration(ms: number, lang: Lang): string {
  const total = Math.max(0, Math.round(ms / 1000));
  const parts: [number, "day" | "hour" | "minute" | "second"][] = [
    [Math.floor(total / 86400), "day"],
    [Math.floor((total % 86400) / 3600), "hour"],
    [Math.floor((total % 3600) / 60), "minute"],
    [total % 60, "second"],
  ];
  const shown = parts.filter(([n]) => n > 0).slice(0, 2);
  if (shown.length === 0) shown.push([0, "second"]);
  return shown
    .map(([n, unit]) =>
      new Intl.NumberFormat(lang, { style: "unit", unit, unitDisplay: "narrow" }).format(n),
    )
    .join(" ");
}

/** A date and time in the user's language and time zone, from an RFC 3339 string or a Date. */
export function formatDate(value: string | Date, lang: Lang): string {
  const date = typeof value === "string" ? new Date(value) : value;
  if (Number.isNaN(date.getTime())) return "";
  return new Intl.DateTimeFormat(lang, { dateStyle: "medium", timeStyle: "short" }).format(date);
}

// ---- Choosing the language ------------------------------------------------------------

export const langStorageKey = "fukku.lang";

/**
 * The saved choice, else the first of the browser's languages we have, else the first of the
 * server's `uiLanguages`, else English. Only languages in `allowed` are considered.
 */
export function resolveLanguage(input: {
  saved: string | null;
  browser: readonly string[];
  allowed: readonly Lang[];
  serverFirst?: string;
}): Lang {
  const { saved, browser, allowed } = input;
  if (saved && isLang(saved) && allowed.includes(saved)) return saved;
  for (const tag of browser) {
    const primary = tag.toLowerCase().split("-")[0] ?? "";
    if (isLang(primary) && allowed.includes(primary)) return primary;
  }
  if (input.serverFirst && isLang(input.serverFirst) && allowed.includes(input.serverFirst)) {
    return input.serverFirst;
  }
  return allowed[0] ?? "en";
}
