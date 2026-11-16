import type { AppName, Apps, JobFile, MovieHit, SeriesHit } from "@/api/types";

/** One search result of either app, with the id Fukku sends back (`tmdbId` or `tvdbId`). */
export type Hit = {
  app: AppName;
  id: number;
  title: string;
  year: number;
  overview: string;
  inLibrary: boolean;
  /** A film that already has a file (M2 refuses it until M3). */
  hasFile: boolean;
  seasons: number[];
};

export type AppInfos = Apps;

export function toHit(app: AppName, hit: MovieHit | SeriesHit): Hit {
  return "tmdbId" in hit
    ? { ...hit, app, id: hit.tmdbId, seasons: [] }
    : { ...hit, app, id: hit.tvdbId, hasFile: false };
}

/**
 * What the files suggest: a single video is a film; an archive or several videos are a show.
 * Falls back to the one app that is configured.
 */
export function guessKind(files: readonly JobFile[], apps: Apps): AppName | null {
  const usable = files.filter((f) => f.status !== "refused");
  const archives = usable.filter((f) => f.kind === "archive").length;
  const videos = usable.filter((f) => f.kind === "video").length;
  const guess: AppName = archives > 0 || videos > 1 ? "sonarr" : "radarr";
  if (apps[guess]) return guess;
  return apps.radarr ? "radarr" : apps.sonarr ? "sonarr" : null;
}
