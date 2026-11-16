import { describe, expect, it } from "vitest";
import type { Probe } from "@/api/types";
import { jobStates } from "@/api/types";
import en from "@/i18n/en";
import es from "@/i18n/es";
import { formatDuration } from "@/i18n/translate";
import {
  archiveRefusalCodes,
  fileRefusalCodes,
  formatAge,
  formatChannels,
  formatFrameRate,
  idleListInterval,
  jobErrorCodes,
  jobErrorText,
  jobPollInterval,
  jobsPollInterval,
  languageName,
  movingInterval,
  refusalText,
  skipReasonCodes,
  skipReasonText,
  slowInterval,
  stateTone,
  summarizeProbe,
  visibleActions,
} from "./job-text";

describe("state and error code to text", () => {
  it("every state has a label and a hint in both languages", () => {
    for (const state of jobStates) {
      for (const cat of [en, es] as Record<string, string>[]) {
        expect(cat[`state.${state}`], state).toBeTruthy();
        expect(cat[`stateHint.${state}`], state).toBeTruthy();
      }
    }
  });

  it("every job error code, refusal and skip reason has a sentence in both languages", () => {
    const keys = [
      ...jobErrorCodes.map((c) => jobErrorText(c).key),
      ...[...archiveRefusalCodes, ...fileRefusalCodes].map((c) => refusalText(c).key),
      ...skipReasonCodes.map((c) => skipReasonText(c).key),
    ];
    for (const key of keys) {
      expect(key).not.toMatch(/unknown/);
      expect((en as Record<string, string>)[key], key).toBeTruthy();
      expect((es as Record<string, string>)[key], key).toBeTruthy();
    }
  });

  it("an unknown code falls back to a generic sentence that carries the code", () => {
    expect(jobErrorText("from_the_future")).toEqual({
      key: "jobError.unknown",
      params: { code: "from_the_future" },
    });
    expect(refusalText("nope")).toEqual({ key: "refusal.unknown", params: { code: "nope" } });
    expect(skipReasonText("nope")).toEqual({ key: "skip.unknown", params: { code: "nope" } });
    expect(en["jobError.unknown"]).toContain("{code}");
    expect(es["jobError.unknown"]).toContain("{code}");
  });

  it("a stalled job waits and a failed one needs a person", () => {
    expect(stateTone("stalled")).toBe("waiting");
    expect(stateTone("cancelled")).toBe("waiting");
    expect(stateTone("failed")).toBe("error");
    expect(stateTone("probed")).toBe("done");
    expect(stateTone("probing")).toBe("moving");
  });
});

describe("visibleActions", () => {
  it("shows only retry, cancel and delete, in that order", () => {
    expect(visibleActions(["cancel", "retry", "delete"])).toEqual(["retry", "cancel", "delete"]);
    expect(visibleActions(["delete"])).toEqual(["delete"]);
    expect(visibleActions(["cancel"])).toEqual(["cancel"]);
  });

  it("leaves submit to the upload page and shows nothing for no actions", () => {
    expect(visibleActions(["cancel", "submit"])).toEqual(["cancel"]);
    expect(visibleActions([])).toEqual([]);
  });
});

describe("polling", () => {
  it("polls every 2 s while a job is moving", () => {
    for (const state of [
      "uploading",
      "uploaded",
      "probing",
      "matched",
      "importing",
      "imported",
      "post-processing",
      "stalled",
    ] as const) {
      expect(jobPollInterval(state), state).toBe(movingInterval);
    }
    expect(movingInterval).toBe(2000);
  });

  it("stops at probed, done, cancelled and failed", () => {
    for (const state of ["probed", "done", "cancelled", "failed"] as const) {
      expect(jobPollInterval(state), state).toBe(false);
    }
  });

  it("is slower in the other states and stops without data", () => {
    expect(jobPollInterval("awaiting-confirmation")).toBe(slowInterval);
    expect(jobPollInterval(undefined)).toBe(false);
  });

  it("a list follows its fastest job", () => {
    expect(jobsPollInterval(["done", "probing"])).toBe(movingInterval);
    expect(jobsPollInterval(["done", "importing"])).toBe(movingInterval);
    expect(jobsPollInterval(["done", "awaiting-confirmation"])).toBe(slowInterval);
    expect(jobsPollInterval(["done", "failed", "probed"])).toBe(idleListInterval);
    expect(jobsPollInterval([])).toBe(idleListInterval);
  });
});

const probe: Probe = {
  container: "matroska,webm",
  durationMs: 5_400_000,
  size: 4_294_967_296,
  video: [
    { index: 0, codec: "h264", width: 1920, height: 1080, frameRate: "24000/1001", default: true },
  ],
  audio: [
    { index: 1, codec: "aac", language: "eng", channels: 2, default: true },
    { index: 2, codec: "ac3", language: "spa", title: "Castellano", channels: 6, default: false },
    { index: 3, codec: "opus", default: false },
  ],
  subtitles: [
    {
      index: 4,
      codec: "subrip",
      language: "spa",
      default: false,
      forced: true,
      hearingImpaired: true,
    },
  ],
};

describe("summarizeProbe", () => {
  const fmt = (lang: "en" | "es") => ({
    lang,
    formatDuration: (ms: number) => formatDuration(ms, lang),
  });

  it("reads duration, resolution, codec and frame rate", () => {
    const s = summarizeProbe(probe, fmt("en"));
    expect(s.duration).toBe("1h 30m");
    expect(s.resolution).toBe("1920×1080");
    expect(s.videoCodec).toBe("H264");
    expect(s.frameRate).toBe("23.976");
  });

  it("names each audio and subtitle track with language, title and flags", () => {
    const s = summarizeProbe(probe, fmt("en"));
    expect(s.audio).toEqual([
      { key: "a1", language: "English", title: undefined, detail: "AAC 2.0", flags: ["default"] },
      { key: "a2", language: "Spanish", title: "Castellano", detail: "AC3 5.1", flags: [] },
      { key: "a3", language: undefined, title: undefined, detail: "OPUS", flags: [] },
    ]);
    expect(s.subtitles).toEqual([
      {
        key: "s4",
        language: "Spanish",
        title: undefined,
        detail: "SUBRIP",
        flags: ["forced", "hearingImpaired"],
      },
    ]);
  });

  it("writes languages in the user's language", () => {
    expect(summarizeProbe(probe, fmt("es")).audio[0]?.language).toBe("inglés");
  });

  it("copes with a file that has no video track", () => {
    const s = summarizeProbe({ ...probe, video: [] }, fmt("en"));
    expect(s.resolution).toBeUndefined();
    expect(s.videoCodec).toBeUndefined();
  });

  it("formats the small pieces", () => {
    expect(formatChannels(2)).toBe("2.0");
    expect(formatChannels(6)).toBe("5.1");
    expect(formatChannels(3)).toBe("3");
    expect(formatFrameRate("25/1")).toBe("25");
    expect(formatFrameRate("0/0")).toBeUndefined();
    expect(formatFrameRate(undefined)).toBeUndefined();
    expect(languageName("und", "en")).toBeUndefined();
    expect(languageName("zz-not-a-tag-at-all-!", "en")).toBeTruthy();
  });
});

describe("formatAge", () => {
  const now = new Date("2026-01-02T12:00:00Z");
  it("uses the largest whole unit", () => {
    expect(formatAge("2026-01-02T11:00:00Z", now, "en")).toBe("1 hour ago");
    expect(formatAge("2025-12-30T12:00:00Z", now, "en")).toBe("3 days ago");
    expect(formatAge("2026-01-02T11:55:00Z", now, "es")).toBe("hace 5 minutos");
    expect(formatAge("2026-01-02T12:00:00Z", now, "en")).toBe("this minute");
  });
});
