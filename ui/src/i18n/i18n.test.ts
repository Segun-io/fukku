import { describe, expect, it } from "vitest";
import en from "./en";
import es from "./es";
import {
  formatBytes,
  formatDate,
  formatDuration,
  formatNumber,
  resolveLanguage,
  translate,
} from "./translate";

const placeholders = (s: string) => [...s.matchAll(/\{(\w+)\}/g)].map((m) => m[1]).sort();

describe("catalogues", () => {
  it("en and es have the same keys", () => {
    // The type already refuses a missing key; this also catches an extra one and a cast.
    expect(Object.keys(es).sort()).toEqual(Object.keys(en).sort());
  });

  it("every key has the same placeholders in both languages", () => {
    for (const key of Object.keys(en) as (keyof typeof en)[]) {
      expect(placeholders(es[key]), key).toEqual(placeholders(en[key]));
    }
  });

  it("no translation is empty", () => {
    for (const cat of [en, es]) {
      for (const [key, text] of Object.entries(cat)) expect(text.trim(), key).not.toBe("");
    }
  });
});

describe("translate", () => {
  it("throws on an unknown key under test", () => {
    expect(() => translate("en", "nope.nothing")).toThrow(/nope\.nothing/);
    expect(() => translate("es", "nope.nothing")).toThrow(/nope\.nothing/);
  });

  it("replaces {name} placeholders and throws on a missing parameter", () => {
    expect(translate("en", "job.size", { size: "4 GB" })).toBe("Size: 4 GB");
    expect(() => translate("en", "job.size")).toThrow(/size/);
  });

  it("picks the plural form and formats the count", () => {
    expect(translate("en", "jobs.count", { count: 1 })).toBe("1 job");
    expect(translate("en", "jobs.count", { count: 1500 })).toBe("1,500 jobs");
    expect(translate("es", "jobs.count", { count: 1 })).toBe("1 trabajo");
    expect(translate("es", "jobs.count", { count: 0 })).toBe("0 trabajos");
    // Spanish has a "many" category (1 000 000); it falls back to "other".
    expect(translate("es", "jobs.count", { count: 1_000_000 })).toContain("trabajos");
  });
});

describe("formatting", () => {
  it("formats sizes with Intl", () => {
    expect(formatBytes(0, "en")).toBe("0 byte");
    expect(formatBytes(85899345920, "en")).toBe("85.9 GB");
    expect(formatBytes(1704, "en")).toBe("1.7 kB");
    expect(formatBytes(1704, "es")).toBe("1,7 kB");
  });

  it("formats durations with the two largest units", () => {
    expect(formatDuration(5_400_000, "en")).toBe("1h 30m");
    expect(formatDuration(45_000, "en")).toBe("45s");
    expect(formatDuration(0, "en")).toBe("0s");
  });

  it("formats numbers and dates in the language", () => {
    expect(formatNumber(1234.5, "en")).toBe("1,234.5");
    expect(formatNumber(1234.5, "es")).toBe("1234,5");
    expect(formatDate("2026-01-01T12:00:00Z", "en")).toContain("2026");
    expect(formatDate("not a date", "en")).toBe("");
  });
});

describe("resolveLanguage", () => {
  const allowed = ["en", "es"] as const;

  it("prefers the saved choice", () => {
    expect(resolveLanguage({ saved: "es", browser: ["en-US"], allowed })).toBe("es");
  });

  it("then the browser's languages, by primary subtag", () => {
    expect(resolveLanguage({ saved: null, browser: ["fr-FR", "es-419", "en"], allowed })).toBe(
      "es",
    );
  });

  it("then the first of the server's languages", () => {
    expect(resolveLanguage({ saved: null, browser: ["fr"], allowed, serverFirst: "es" })).toBe(
      "es",
    );
  });

  it("ignores a language the server does not offer, and unknown saved values", () => {
    expect(resolveLanguage({ saved: "es", browser: ["es"], allowed: ["en"] })).toBe("en");
    expect(resolveLanguage({ saved: "xx", browser: [], allowed })).toBe("en");
  });
});
