// Mock of flow 2 (#52, #53, #55, #56): another language of a film that is already in the library.
// The shapes are the ones of `internal/flows` (flow2.go, preview.go, version.go, merge.go as
// PR #91 describes it). Titles are the Blender open films; file names are made up.

import type {
  Flow2,
  Flow2Finding,
  Flow2Offer,
  ImportInfo,
  ImportResult,
  Job,
  MergeInfo,
  Preview,
  PreviewRequest,
} from "@/api/types";

const hex = (n: number) => n.toString(16).padStart(32, "0");
export const flow2JobId = hex(0x4c01);
export const flow2PalJobId = hex(0x4c02);
export const flow2DifferentJobId = hex(0x4c03);
export const flow2Target = { app: "radarr", titleId: 20, title: "Sintel", year: 2010 } as const;

const libraryMs = 888_000;
const englishRef = { id: 1, name: "English" };

/** The upload's duration for each finding, against the library file's 888 s. */
const uploadMs: Record<Flow2Finding, number> = {
  match: libraryMs + 200,
  pal: Math.round(libraryMs / 1.0427),
  different: 940_000,
};

const labelOf: Record<Flow2Finding, { ms: number; ratio: number }> = {
  match: { ms: 200, ratio: 1.0002 },
  pal: { ms: uploadMs.pal - libraryMs, ratio: 1.0427 },
  different: { ms: uploadMs.different - libraryMs, ratio: 1.0586 },
};

/** `flow2` of the job detail for a finding. `over` changes any part of it. */
export function flow2Fixture(finding: Flow2Finding, over: Partial<Flow2> = {}): Flow2 {
  const offers: Flow2Offer[] = finding === "match" ? ["merge", "version"] : ["version"];
  const base: Flow2 = {
    state: "ready",
    library: {
      fileId: 11,
      name: "Sintel (2010) WEBDL-1080p.mkv",
      size: 4_200_000_000,
      quality: { id: 3, name: "WEBDL-1080p" },
      languages: [englishRef],
      runtimeMs: libraryMs,
      durationMs: libraryMs,
      audio: [{ index: 1, codec: "ac3", language: "eng", channels: 6, default: true }],
      subtitles: [],
    },
    upload: {
      fileId: 301,
      name: "Sintel.2010.Spanish.1080p.mkv",
      size: 3_900_000_000,
      quality: null,
      languages: [],
      durationMs: uploadMs[finding],
      audio: [
        { index: 1, codec: "ac3", language: "spa", channels: 2, default: true },
        {
          index: 2,
          codec: "ac3",
          language: "es-419",
          title: "Latino",
          channels: 6,
          default: false,
        },
      ],
      subtitles: [
        {
          index: 3,
          codec: "subrip",
          language: "spa",
          default: false,
          forced: false,
          hearingImpaired: false,
        },
      ],
    },
    finding,
    deltaMs: labelOf[finding].ms,
    ratio: labelOf[finding].ratio,
    offers,
    notOffered: finding === "match" ? [] : [{ offer: "merge", reason: "runtime_differs" }],
    version: {
      label: "Español",
      language: "es",
      name: "Sintel (2010) - Español.mkv",
      problem: null,
      notices: ["outside_radarr", "regrouped_after_changes", "kept_on_delete"],
    },
    mergeRefusal: null,
  };
  return { ...base, ...over };
}

const base = (id: string, name: string, minute: string): Job => ({
  id,
  owner: "user1",
  state: "awaiting-confirmation",
  attempt: 1,
  error: null,
  name,
  fileCount: 1,
  totalSize: 3_900_000_000,
  createdAt: `2026-01-03T08:${minute}:00Z`,
  updatedAt: `2026-01-03T08:${minute}:30Z`,
  expiresAt: null,
  actions: ["cancel"],
});

/** `?mock=flow2`: an upload for each finding, waiting on the flow 2 page. */
export const flow2Jobs: Job[] = [
  base(flow2JobId, "Sintel.2010.Spanish.1080p.mkv", "10"),
  base(flow2PalJobId, "Sintel.2010.Spanish.PAL.mkv", "20"),
  base(flow2DifferentJobId, "Sintel.2010.Spanish.Extended.mkv", "30"),
];

export function initialFlow2s(): Record<string, Flow2> {
  return {
    [flow2JobId]: flow2Fixture("match"),
    [flow2PalJobId]: flow2Fixture("pal"),
    [flow2DifferentJobId]: flow2Fixture("different"),
  };
}

// ---- The sync check (#53) -----------------------------------------------------------------

export const noPreview: Preview = {
  state: "none",
  offsetMs: null,
  audioTrack: null,
  requestedAt: null,
  requestedBy: null,
  error: null,
  clips: [],
};

/** The clips of a preview that is ready: start, middle and end of 20 s each. */
export function readyPreview(jobId: string, req: Required<PreviewRequest>, at: string): Preview {
  return {
    state: "ready",
    offsetMs: req.offsetMs,
    audioTrack: req.audioTrack,
    requestedAt: at,
    requestedBy: "user1",
    error: null,
    clips: [0, 434_000, 868_000].map((startMs, i) => ({
      n: i + 1,
      startMs,
      lengthMs: 20_000,
      size: 812_345,
      url: `/api/jobs/${jobId}/preview/${i + 1}`,
    })),
  };
}

/** The rule of the server's request: offset within ±60 s, track one of the upload's audio tracks. */
export function previewRefusal(flow2: Flow2, req: PreviewRequest): { code: string } | null {
  if (!flow2.offers.includes("merge")) return { code: "merge_not_offered" };
  const audio = flow2.upload?.audio ?? [];
  if (audio.length === 0) return { code: "no_audio" };
  if (req.offsetMs !== undefined && Math.abs(req.offsetMs) > 60_000) return { code: "bad_request" };
  if (req.audioTrack !== undefined && !audio.some((a) => a.index === req.audioTrack)) {
    return { code: "bad_request" };
  }
  return null;
}

// ---- The confirmation and what follows (#55, #56) ------------------------------------------

/** What a mock flow 2 job does after its confirmation; the job detail moves it one step per call. */
export type Flow2Progress =
  | {
      kind: "merge";
      offsetMs: number;
      audioTracks: number[];
      subtitleTracks: number[];
      languageTag: string;
      trackName: string;
      keepOriginalDefault: boolean;
      tick: number;
    }
  | { kind: "version"; label: string; name: string; tick: number };

const mergedName = "Sintel (2010) WEBDL-1080p.mkv";

/** The version label rules of the server, as far as the mock knows them. */
export function labelProblem(label: string): string | null {
  if (label.trim() === "") return "label_empty";
  if (/\b(1080p|720p|web-?dl|bluray)\b/i.test(label)) return "label_forbidden";
  return null;
}

export function flow2ImportAt(p: Flow2Progress): {
  state: "importing" | "post-processing" | "done";
  import: ImportInfo;
  result: ImportResult | null;
} {
  const steps = (done: boolean) => [
    {
      step: "mediaServerRefresh",
      status: done ? ("ok" as const) : ("pending" as const),
      detail: "",
    },
  ];
  const link = "https://media.example.com/web/index.html#!/details?id=0a1b2c";
  if (p.kind === "merge") {
    const merge = (state: MergeInfo["state"]): MergeInfo => ({
      state,
      refusal: "",
      audioTracks: p.audioTracks,
      subtitleTracks: p.subtitleTracks,
      offsetMs: p.offsetMs,
      languageTag: p.languageTag,
      trackName: p.trackName,
      keepOriginalDefault: p.keepOriginalDefault,
      arrLanguage: { id: 37, name: "Spanish (Latino)" },
      name: state === "merged" ? mergedName : "",
      size: state === "merged" ? 4_300_000_000 : 0,
    });
    const row = (result: string) => [{ id: "r1", name: mergedName, result }];
    if (p.tick === 0) {
      return {
        state: "importing",
        import: { status: "pending", rows: [], merge: merge("pending") },
        result: null,
      };
    }
    if (p.tick === 1) {
      return {
        state: "importing",
        import: { status: "pending", rows: [], merge: merge("running") },
        result: null,
      };
    }
    if (p.tick === 2) {
      return {
        state: "importing",
        import: { status: "running", rows: row(""), merge: merge("merged") },
        result: null,
      };
    }
    const result = (done: boolean): ImportResult => ({
      titleLink: done ? link : null,
      monitored: false,
      unmonitorReason: "merged",
      merge: {
        name: mergedName,
        warnings: [{ code: "search_can_replace", message: "The film is unmonitored." }],
      },
      postProcessing: steps(done),
    });
    return {
      state: p.tick === 3 ? "post-processing" : "done",
      import: { status: "done", rows: row("imported"), merge: merge("merged") },
      result: result(p.tick > 3),
    };
  }
  if (p.tick === 0) {
    return { state: "importing", import: { status: "running", rows: [] }, result: null };
  }
  const version = (status: string) => ({
    name: p.name,
    label: p.label,
    status,
    detail: "",
    groupedAt: status === "grouped" ? "2026-01-03T09:00:00Z" : null,
    primaryChangedAt: null,
    notices: ["outside_radarr", "regrouped_after_changes", "kept_on_delete"],
  });
  return {
    state: p.tick === 1 ? "post-processing" : "done",
    import: { status: "done", rows: [] },
    result: {
      titleLink: p.tick > 1 ? link : null,
      monitored: null,
      unmonitorReason: "",
      version: version(p.tick > 1 ? "grouped" : "linked"),
      postProcessing: steps(p.tick > 1),
    },
  };
}
