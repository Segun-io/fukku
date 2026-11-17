import { describe, expect, it } from "vitest";
import generated from "@/api/error-codes.generated.json";
import { isKnownErrorCode } from "@/api/types";
import { warningCodes } from "@/features/confirm/notices";
import {
  archiveRefusalCodes,
  fileRefusalCodes,
  jobErrorCodes,
  mergeRefusalCodes,
  previewErrorCodes,
  skipReasonCodes,
} from "@/lib/job-text";
import en from "./en";
import es from "./es";

// `codes` is written by `go test ./internal/errorcodes -update` from the Go source: every code
// the server can send. Each one needs its own sentence in both languages; when the server
// gains a code, this test fails until the UI has text for it.
const { prefixes, codes } = generated as {
  prefixes: Record<string, string>;
  codes: Record<string, string[]>;
};

/** The keys of `codes` that a catalogue has no text for, as "family: key". */
function missing(catalogue: Record<string, string>, list: Record<string, string[]>): string[] {
  const out: string[] = [];
  for (const [family, items] of Object.entries(list)) {
    for (const code of items) {
      const key = `${prefixes[family]}${code}`;
      if (!catalogue[key]?.trim()) out.push(`${family}: ${key}`);
    }
  }
  return out;
}

describe("a sentence for every code the server can send", () => {
  it("lists the codes of the Go source", () => {
    expect(Object.keys(codes).length).toBeGreaterThan(10);
    expect(codes.errors).toContain("not_found");
    expect(codes.jobErrors).toContain("worker_unavailable");
  });

  it("English has text for each", () => {
    expect(missing(en, codes)).toEqual([]);
  });

  it("Spanish has text for each", () => {
    expect(missing(es, codes)).toEqual([]);
  });

  it("fails when a code has no text", () => {
    const withNew = { ...codes, errors: [...(codes.errors ?? []), "a_code_from_the_future"] };
    expect(missing(en, withNew)).toContain("errors: errors.a_code_from_the_future");
    expect(missing(es, withNew)).toContain("errors: errors.a_code_from_the_future");
  });

  it("the UI recognises each code, so that it shows the sentence and not the generic one", () => {
    for (const code of codes.errors ?? []) expect(isKnownErrorCode(code), code).toBe(true);
    const lists: [string, readonly string[]][] = [
      ["jobErrors", jobErrorCodes],
      ["mergeRefusals", mergeRefusalCodes],
      ["previewErrors", previewErrorCodes],
      ["skipReasons", skipReasonCodes],
      ["warnings", warningCodes],
    ];
    for (const [family, list] of lists) {
      for (const code of codes[family] ?? []) expect(list, `${family}: ${code}`).toContain(code);
    }
    const refusals = [...archiveRefusalCodes, ...fileRefusalCodes];
    for (const code of [...(codes.archiveRefusals ?? []), ...(codes.fileRefusals ?? [])]) {
      expect(refusals, `refusal: ${code}`).toContain(code);
    }
  });
});
