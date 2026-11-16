import { describe, expect, it } from "vitest";
import en from "./en";
import es from "./es";
import {
  formatBytes,
  formatDate,
  formatDuration,
  formatNumber,
  formatPercent,
  formatRate,
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

describe("formatting for figures that change", () => {
  // A number and its unit are joined by a space that does not break.
  const nb = "\u00a0";

  it("prints a size in the unit of another, with fixed decimals", () => {
    expect(formatBytes(31.4e9, "en", 80e9)).toBe(`31.4${nb}GB`);
    expect(formatBytes(80e9, "en", 80e9)).toBe(`80.0${nb}GB`);
    expect(formatBytes(0, "en", 80e9)).toBe(`0.0${nb}GB`);
    expect(formatBytes(950e6, "en", 80e9)).toBe(`1.0${nb}GB`);
    expect(formatBytes(31.4e9, "es", 80e9)).toBe(`31,4${nb}GB`);
    expect(formatBytes(20_000, "en", 61_400)).toBe(`20${nb}kB`);
    expect(formatBytes(1.25e12, "en", 2e12)).toBe(`1.25${nb}TB`);
    // Without `like` nothing changes.
    expect(formatBytes(85899345920, "en")).toBe("85.9 GB");
  });

  it("formats speeds", () => {
    expect(formatRate(11.8e6, "en")).toBe(`11.8${nb}MB/s`);
    expect(formatRate(11.8e6, "es")).toBe(`11,8${nb}MB/s`);
    expect(formatRate(600_000, "en")).toBe(`0.6${nb}MB/s`);
    expect(formatRate(64_000, "en")).toBe(`64${nb}kB/s`);
    expect(formatRate(-1, "en")).toBe(`0${nb}kB/s`);
  });

  it("formats a share of a whole", () => {
    expect(formatPercent(0.392, "en")).toBe("39%");
    expect(formatPercent(0.392, "es")).toBe(`39${nb}%`);
    expect(formatPercent(1.2, "en")).toBe("100%");
    expect(formatPercent(-1, "en")).toBe("0%");
  });

  it("leaves the year out of a short date", () => {
    const short = formatDate("2026-01-01T12:00:00Z", "en", "short");
    expect(short).not.toContain("2026");
    expect(short).toContain("Jan");
    expect(formatDate("2026-01-01T12:00:00Z", "en", "medium")).toContain("2026");
    expect(formatDate("not a date", "en", "short")).toBe("");
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
