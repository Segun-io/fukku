import { describe, expect, it } from "vitest";
import { guessSubtitle, twoLetterLanguage } from "./subtitle-name";

describe("guessSubtitle", () => {
  it.each([
    ["Film (1999).es.srt", { language: "es", forced: false, hearingImpaired: false }],
    ["Film (1999).es.forced.srt", { language: "es", forced: true, hearingImpaired: false }],
    ["Film (1999).en.hi.srt", { language: "en", forced: false, hearingImpaired: true }],
    ["Film.2001.eng.sdh.ass", { language: "eng", forced: false, hearingImpaired: true }],
    ["Film (1999).pt-BR.srt", { language: "pt-br", forced: false, hearingImpaired: false }],
    ["Film (1999).srt", { language: "", forced: false, hearingImpaired: false }],
    ["Film (1999).forced.srt", { language: "", forced: true, hearingImpaired: false }],
  ])("%s", (name, expected) => {
    expect(guessSubtitle(name)).toEqual(expected);
  });
});

describe("twoLetterLanguage", () => {
  it.each([
    ["es", "es"],
    ["eng", "en"],
    ["pt-br", "pt"],
    ["xyz", ""],
    ["", ""],
  ])("%s", (tag, expected) => {
    expect(twoLetterLanguage(tag)).toBe(expected);
  });
});
