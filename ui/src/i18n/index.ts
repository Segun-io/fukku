// Translations and formatting. `useT()` gives `t`, the formatters bound to the current
// language, and `setLang`. See `en.ts` for how keys, placeholders and plurals work.
export { I18nProvider, useT } from "./provider";
export {
  formatBytes,
  formatDate,
  formatDuration,
  formatNumber,
  formatPercent,
  formatRate,
  isLang,
  type Lang,
  langStorageKey,
  languages,
  resolveLanguage,
  type TFunction,
  type TKey,
  type TParams,
  translate,
} from "./translate";
