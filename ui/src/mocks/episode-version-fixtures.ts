// Mock of the second versions of episodes (#58, the season-dub policy `version`): the proposal's
// `seasonDub.version` and the job's `result.episodeVersions`, in the shape of the server's JSON
// (internal/flows/episode_version.go). Names are made up.

import type { EpisodeVersionRow, EpisodeVersionsView, SeasonDub } from "@/api/proposal";
import type { EpisodeVersionResult, EpisodeVersionsResult } from "@/api/types";

const seriesFolder = "Lighthouse Letters (2019)";

/** The row of an episode that has a file, with the name its version would get. */
export function versionRow(
  row: string,
  episodeId: number,
  season: number,
  episode: number,
  over: Partial<EpisodeVersionRow> = {},
  label = "Español",
): EpisodeVersionRow {
  const s = String(season).padStart(2, "0");
  const e = String(episode).padStart(2, "0");
  return {
    row,
    episodeId,
    seasonNumber: season,
    episodeNumber: episode,
    name: `${seriesFolder} - S${s}E${e} - ${label}.mkv`,
    problem: "",
    ...over,
  };
}

/** `seasonDub.version` as the server sends it while `version` is offered. */
export function versionView(over: Partial<EpisodeVersionsView> = {}): EpisodeVersionsView {
  return {
    label: "Español",
    language: "es",
    problem: null,
    rows: [versionRow("r1", 107, 1, 7)],
    notices: ["untracked_by_sonarr", "adopted_without_episode_file"],
    ...over,
  };
}

/** `seasonDub` of a proposal that asks and offers the three policies. */
export function versionDub(over: Partial<SeasonDub> = {}): SeasonDub {
  return {
    policy: "ask",
    ask: true,
    offers: ["replace", "subtitlesOnly", "version"],
    seasons: [{ season: 1, episodes: 2, existing: 1, new: 1 }],
    notOffered: [],
    version: versionView(),
    ...over,
  };
}

export function versionResult(
  row: string,
  episode: number,
  over: Partial<EpisodeVersionResult> = {},
): EpisodeVersionResult {
  return {
    row,
    seasonNumber: 1,
    episodeNumber: episode,
    name: `${seriesFolder} - S01E0${episode} - Español.mkv`,
    status: "grouped",
    refusal: "",
    detail: "",
    groupedAt: "2026-01-02T12:05:00Z",
    ...over,
  };
}

/** `result.episodeVersions` of a finished job. */
export function versionsResult(
  versions: EpisodeVersionResult[],
  over: Partial<EpisodeVersionsResult> = {},
): EpisodeVersionsResult {
  return {
    label: "Español",
    versions,
    notices: ["untracked_by_sonarr", "adopted_without_episode_file"],
    ...over,
  };
}
