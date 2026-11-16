// What a subtitle file's name says about it: "Film (1999).es.forced.srt" is Spanish, forced.
// This is a guess for the form's starting point; the user can change every part of it.

export type SubtitleGuess = { language: string; forced: boolean; hearingImpaired: boolean };

const forcedTags = ["forced", "forzado", "forzados"];
const hearingTags = ["hi", "sdh", "cc"];

/** A language tag: "es", "eng", "pt-br", "es-419". */
const languageTag = /^[a-z]{2,3}(-[a-z0-9]{2,4})?$/;

export function guessSubtitle(fileName: string): SubtitleGuess {
  const parts = fileName.toLowerCase().split(".");
  // Without the extension; the first part is the title.
  const tags = parts.slice(1, -1);
  let forced = false;
  let hearingImpaired = false;
  let language = "";
  // The tags nearest the extension come last: read from the end.
  for (let i = tags.length - 1; i >= 0; i--) {
    const tag = tags[i] ?? "";
    if (forcedTags.includes(tag)) forced = true;
    else if (hearingTags.includes(tag)) hearingImpaired = true;
    else if (language === "" && languageTag.test(tag)) language = tag;
  }
  return { language, forced, hearingImpaired };
}

const threeToTwo: Record<string, string> = {
  eng: "en",
  spa: "es",
  fre: "fr",
  fra: "fr",
  ger: "de",
  deu: "de",
  ita: "it",
  por: "pt",
  rus: "ru",
  jpn: "ja",
  zho: "zh",
  chi: "zh",
  kor: "ko",
  ara: "ar",
  dut: "nl",
  nld: "nl",
  pol: "pl",
  tur: "tr",
  swe: "sv",
  nor: "no",
  dan: "da",
  fin: "fi",
  cat: "ca",
  ces: "cs",
  cze: "cs",
  ell: "el",
  gre: "el",
  heb: "he",
  hin: "hi",
  hun: "hu",
  ron: "ro",
  rum: "ro",
  ukr: "uk",
  vie: "vi",
  tha: "th",
  ind: "id",
  bul: "bg",
  hrv: "hr",
  slk: "sk",
  slo: "sk",
};

/**
 * The server takes ISO 639-1 codes. "es" stays, "pt-br" becomes "pt", a known three-letter
 * code is mapped, and anything else is "" (the user types it).
 */
export function twoLetterLanguage(tag: string): string {
  const base = tag.split("-")[0] ?? "";
  if (/^[a-z]{2}$/.test(base)) return base;
  return threeToTwo[base] ?? "";
}
