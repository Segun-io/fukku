import { describe, expect, it } from "vitest";
import type { JobFile } from "@/api/types";
import { choicesFor } from "./subtitles-section";

const subtitle = (id: number, name: string): JobFile => ({
  id,
  path: `files/${name}`,
  name,
  kind: "subtitle",
  size: 1700,
  status: "ok",
  reason: null,
  from: null,
  probe: null,
  extraction: null,
});

describe("the choice for each subtitle file", () => {
  it("keeps the user's choice and guesses the rest from the name", () => {
    const files = [subtitle(1, "Show.S01E01.es.srt"), subtitle(2, "Show.S01E03.en.srt")];
    const mine = { fileId: 1, language: "fr", forced: true, hearingImpaired: false };
    expect(choicesFor(files, [mine])).toEqual([
      mine,
      { fileId: 2, language: "en", forced: false, hearingImpaired: false },
    ]);
  });

  it("gives a file that arrives after the page opened a choice at once", () => {
    // The page opened before the job listed this file: nothing was chosen for it yet.
    expect(choicesFor([], [])).toEqual([]);
    expect(choicesFor([subtitle(2, "Show.S01E03.en.srt")], [])).toHaveLength(1);
  });
});
