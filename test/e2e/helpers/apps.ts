import {
  bazarrKey,
  bazarrURL,
  indexerKey,
  indexerURL,
  jellyfinKey,
  jellyfinURL,
  mediacheckURL,
  radarrKey,
  radarrURL,
  simulateOutage,
  sonarrKey,
  sonarrURL,
} from "./env";

// Clients of the test environment's Radarr, Sonarr, Bazarr and Jellyfin (never a real one: the
// addresses are the stack's service names), for the M2 and M3 exit tests (issues #47, #61).
// They look at what Fukku did, and remove the titles a test added; the M3 tests also run
// Radarr's RSS sync and Wanted search against the fake indexer. Nothing else is changed in them.

export class HttpError extends Error {
  constructor(
    readonly method: string,
    readonly url: string,
    readonly status: number,
    readonly body: string,
  ) {
    super(`${method} ${url}: ${status} ${body.slice(0, 500)}`);
  }
}

class Rest {
  constructor(
    readonly name: string,
    private readonly base: string,
    private readonly headers: Record<string, string>,
  ) {}

  async call<T>(method: string, path: string, body?: unknown, timeoutMs = 30_000): Promise<T> {
    const res = await fetch(this.base + path, {
      method,
      headers: {
        ...this.headers,
        ...(body === undefined ? {} : { "Content-Type": "application/json" }),
      },
      body: body === undefined ? undefined : JSON.stringify(body),
      signal: AbortSignal.timeout(timeoutMs),
    });
    const text = await res.text();
    if (!res.ok) throw new HttpError(method, path, res.status, text);
    return (text ? JSON.parse(text) : undefined) as T;
  }

  get<T>(path: string): Promise<T> {
    return this.call<T>("GET", path);
  }
}

/** A command of Radarr or Sonarr as GET /command lists it. */
export interface ArrCommand {
  id: number;
  name: string;
  status: string;
  result?: string;
  message?: string;
  queued?: string;
  started?: string;
  ended?: string;
  body?: { files?: { path: string }[] };
}

export interface MovieFile {
  id: number;
  path: string;
  size: number;
  relativePath?: string;
  dateAdded?: string;
  quality?: { quality: { id: number; name: string } };
  languages?: { id: number; name: string }[];
}

export interface Movie {
  id: number;
  tmdbId: number;
  title: string;
  path: string;
  hasFile: boolean;
  monitored: boolean;
  runtime?: number;
  movieFile?: MovieFile;
}

/** One entry of a title's history in Radarr or Sonarr. */
export interface HistoryEvent {
  id: number;
  eventType: string;
  sourceTitle: string;
  date: string;
}

export interface Episode {
  id: number;
  seasonNumber: number;
  episodeNumber: number;
  title: string;
  hasFile: boolean;
  episodeFileId: number;
}

export interface EpisodeFile {
  id: number;
  path: string;
  size: number;
  seasonNumber: number;
}

export interface Series {
  id: number;
  tvdbId: number;
  title: string;
  path: string;
}

class Arr extends Rest {
  /** Answers once the app's API is up (after a restart). */
  ping(): Promise<unknown> {
    return this.call("GET", "/api/v3/system/status", undefined, 5_000);
  }

  /** ManualImport commands whose every file is under `folder` (as the app sees it). */
  async manualImports(folder: string): Promise<ArrCommand[]> {
    const prefix = folder.endsWith("/") ? folder : `${folder}/`;
    const all = await this.get<ArrCommand[]>("/api/v3/command");
    return all.filter((c) => {
      const files = c.body?.files ?? [];
      return (
        c.name === "ManualImport" &&
        files.length > 0 &&
        files.every((f) => f.path.startsWith(prefix))
      );
    });
  }

  command(id: number): Promise<ArrCommand> {
    return this.get<ArrCommand>(`/api/v3/command/${id}`);
  }

  /** Run a command (POST /command) and wait until it has ended; returns how it ended. */
  async run(body: Record<string, unknown>, timeoutMs = 180_000): Promise<ArrCommand> {
    const started = await this.call<ArrCommand>("POST", "/api/v3/command", body);
    const deadline = Date.now() + timeoutMs;
    for (;;) {
      const c = await this.command(started.id);
      if (!["queued", "started"].includes(c.status)) return c;
      if (Date.now() > deadline) {
        throw new Error(
          `${this.name} ${String(body.name)} still ${c.status} after ${timeoutMs} ms`,
        );
      }
      await new Promise((r) => setTimeout(r, 500));
    }
  }

  /** The app's size limits per quality, in MB per minute (GET /qualitydefinition). */
  qualityDefinitions(): Promise<
    { quality: { id: number; name: string }; minSize: number; maxSize?: number }[]
  > {
    return this.get("/api/v3/qualitydefinition");
  }
}

export class RadarrClient extends Arr {
  movieByTmdb(tmdbId: number): Promise<Movie[]> {
    return this.get<Movie[]>(`/api/v3/movie?tmdbId=${tmdbId}`);
  }

  movie(id: number): Promise<Movie> {
    return this.get<Movie>(`/api/v3/movie/${id}`);
  }

  movieFiles(movieId: number): Promise<MovieFile[]> {
    return this.get<MovieFile[]>(`/api/v3/moviefile?movieId=${movieId}`);
  }

  /** The film's history, newest first. */
  history(movieId: number): Promise<HistoryEvent[]> {
    return this.get<HistoryEvent[]>(`/api/v3/history/movie?movieId=${movieId}`);
  }

  /** What Radarr reads in a name (GET /parse): the quality and its revision. */
  async parse(
    title: string,
  ): Promise<{ quality: string; source: string; resolution: number; version: number }> {
    const res = await this.get<{
      parsedMovieInfo?: {
        quality?: {
          quality?: { name: string; source: string; resolution: number };
          revision?: { version: number };
        };
      };
    }>(`/api/v3/parse?title=${encodeURIComponent(title)}`);
    const q = res.parsedMovieInfo?.quality;
    return {
      quality: q?.quality?.name ?? "",
      source: q?.quality?.source ?? "",
      resolution: q?.quality?.resolution ?? 0,
      version: q?.revision?.version ?? 0,
    };
  }

  /** Remove the film and its files, as a test that added it must (no import exclusion). */
  async removeByTmdb(tmdbId: number): Promise<string[]> {
    const paths: string[] = [];
    for (const m of await this.movieByTmdb(tmdbId)) {
      paths.push(m.path);
      await this.call("DELETE", `/api/v3/movie/${m.id}?deleteFiles=true&addImportExclusion=false`);
    }
    return paths;
  }

  /** The metadata lookup, asked directly: tells an upstream outage from a fault of Fukku's. */
  lookup(tmdbId: number): Promise<unknown> {
    return this.call("GET", `/api/v3/movie/lookup/tmdb?tmdbId=${tmdbId}`, undefined, 60_000);
  }
}

export class SonarrClient extends Arr {
  seriesByTvdb(tvdbId: number): Promise<Series[]> {
    return this.get<Series[]>(`/api/v3/series?tvdbId=${tvdbId}`);
  }

  episodes(seriesId: number): Promise<Episode[]> {
    return this.get<Episode[]>(`/api/v3/episode?seriesId=${seriesId}`);
  }

  episodeFiles(seriesId: number): Promise<EpisodeFile[]> {
    return this.get<EpisodeFile[]>(`/api/v3/episodefile?seriesId=${seriesId}`);
  }

  /** Remove the show and its files (no import exclusion). */
  async removeByTvdb(tvdbId: number): Promise<string[]> {
    const paths: string[] = [];
    for (const s of await this.seriesByTvdb(tvdbId)) {
      paths.push(s.path);
      await this.call(
        "DELETE",
        `/api/v3/series/${s.id}?deleteFiles=true&addImportListExclusion=false`,
      );
    }
    return paths;
  }

  lookup(tvdbId: number): Promise<unknown> {
    return this.call("GET", `/api/v3/series/lookup?term=tvdb:${tvdbId}`, undefined, 60_000);
  }
}

/** One entry of Bazarr's film or episode history. */
export interface SubtitleHistory {
  action: number;
  description: string;
  provider: string;
  subtitles_path: string;
  language: { code2: string; forced: boolean; hi: boolean } | null;
  timestamp: string;
}

export class BazarrClient extends Rest {
  async movieHistory(radarrId: number): Promise<SubtitleHistory[]> {
    const res = await this.get<{ data: SubtitleHistory[] }>(
      `/api/movies/history?radarrid=${radarrId}`,
    );
    return res.data ?? [];
  }

  async episodeHistory(episodeId: number): Promise<SubtitleHistory[]> {
    const res = await this.get<{ data: SubtitleHistory[] }>(
      `/api/episodes/history?episodeid=${episodeId}`,
    );
    return res.data ?? [];
  }
}

export interface JellyfinItem {
  Id: string;
  Name: string;
  Type: string;
  Path?: string;
}

export class JellyfinClient extends Rest {
  /** The item at exactly this path (as Jellyfin sees it), or null. */
  async itemAt(path: string, types = "Movie,Episode,Series"): Promise<JellyfinItem | null> {
    const q = new URLSearchParams({
      Recursive: "true",
      IncludeItemTypes: types,
      Fields: "Path",
    });
    const res = await this.get<{ Items: JellyfinItem[] }>(`/Items?${q}`);
    return res.Items.find((i) => i.Path === path) ?? null;
  }

  /**
   * The paths of an item's media sources: its own file and the versions grouped with it
   * (MergeVersions, decision #20).
   */
  async sources(itemId: string): Promise<string[]> {
    const q = new URLSearchParams({ Ids: itemId, Fields: "MediaSources,Path" });
    const res = await this.get<{ Items: { MediaSources?: { Path?: string }[] }[] }>(`/Items?${q}`);
    return (res.Items[0]?.MediaSources ?? []).map((s) => s.Path ?? "");
  }

  /** Every item whose path is under `folder`. */
  async itemsUnder(folder: string): Promise<JellyfinItem[]> {
    const q = new URLSearchParams({
      Recursive: "true",
      IncludeItemTypes: "Movie,Episode,Series,Season",
      Fields: "Path",
    });
    const res = await this.get<{ Items: JellyfinItem[] }>(`/Items?${q}`);
    return res.Items.filter((i) => i.Path === folder || i.Path?.startsWith(`${folder}/`));
  }

  /**
   * Remove Jellyfin's items under a folder whose files are gone (a test removed its title).
   * A scan does not do it while the library's folder is empty, and item ids follow the path,
   * so the next import at the same path would otherwise find the old item at once.
   */
  async forget(folder: string): Promise<void> {
    const items = await this.itemsUnder(folder);
    // A show first: removing it removes its seasons and episodes.
    items.sort((a, b) => (a.Path?.length ?? 0) - (b.Path?.length ?? 0));
    const gone = new Set<string>();
    for (const it of items) {
      if (gone.has(it.Id)) continue;
      try {
        await this.call("DELETE", `/Items/${it.Id}`);
      } catch (err) {
        if (!(err instanceof HttpError && err.status === 404)) throw err;
      }
      gone.add(it.Id);
    }
  }
}

/** A release the fake indexer offers (test/env/indexer). */
export interface Release {
  title: string;
  category: number; // a Newznab category: 2040 Movies/HD, 5040 TV/HD
  size: number; // bytes
  guid?: string;
}

/** A search or NZB request the fake indexer answered. */
export interface IndexerRequest {
  at: string;
  agent: string;
  t: string;
  query: Record<string, string>;
  returned: string[];
}

/**
 * The fake indexer's control API (test/env/indexer): what it offers, and what the apps asked
 * it. With the seeded blackhole download client, a grab is the release's NZB written to
 * /data/blackhole/<app>/nzb (nothing is downloaded).
 */
export class IndexerClient extends Rest {
  offer(releases: Release[]): Promise<Release[]> {
    return this.call<Release[]>("PUT", "/control/releases", releases);
  }

  clear(): Promise<unknown> {
    return this.call("DELETE", "/control/releases");
  }

  requests(): Promise<IndexerRequest[]> {
    return this.get<IndexerRequest[]>("/control/requests");
  }

  forgetRequests(): Promise<unknown> {
    return this.call("DELETE", "/control/requests");
  }
}

/** What mediacheck (test/env/mediacheck) measured in a clip: the audio's offset to the picture. */
export interface SyncReport {
  offsetMs: number;
  flashes: number;
  beeps: number;
}

/** A track of mkvmerge's identification (mkvmerge -J), the parts the tests look at. */
export interface MkvTrack {
  id: number;
  type: "video" | "audio" | "subtitles";
  codec: string;
  properties: {
    language?: string;
    language_ietf?: string;
    track_name?: string;
    default_track?: boolean;
  };
}

class MediacheckClient {
  constructor(private readonly base: string) {}

  /**
   * How far a clip's audio is from its picture, by the flash and the beep of the generated
   * media (fixtures.SyncOffset, as the #53 tests measure it); positive: the audio is late.
   * Ambiguous by whole seconds: 800 ms late reads as 200 ms early.
   */
  async sync(clip: Buffer): Promise<SyncReport> {
    const res = await fetch(`${this.base}/sync`, {
      method: "POST",
      headers: { "Content-Type": "application/octet-stream" },
      body: new Uint8Array(clip),
      signal: AbortSignal.timeout(180_000),
    });
    const text = await res.text();
    if (!res.ok) throw new Error(`mediacheck sync: ${res.status} ${text}`);
    return JSON.parse(text) as SyncReport;
  }

  /** mkvmerge's identification of a file on the library filesystem (/data). */
  async tracks(file: string): Promise<MkvTrack[]> {
    const res = await fetch(`${this.base}/identify?path=${encodeURIComponent(file)}`, {
      signal: AbortSignal.timeout(120_000),
    });
    const text = await res.text();
    if (!res.ok) throw new Error(`mediacheck identify ${file}: ${res.status} ${text}`);
    return (JSON.parse(text) as { tracks: MkvTrack[] }).tracks;
  }
}

export const radarr = new RadarrClient("radarr", radarrURL, { "X-Api-Key": radarrKey });
export const sonarr = new SonarrClient("sonarr", sonarrURL, { "X-Api-Key": sonarrKey });
export const bazarr = new BazarrClient("bazarr", bazarrURL, { "X-API-KEY": bazarrKey });
export const jellyfin = new JellyfinClient("jellyfin", jellyfinURL, {
  Authorization: `MediaBrowser Token="${jellyfinKey}"`,
});
export const indexer = new IndexerClient("indexer", indexerURL, { "X-Api-Key": indexerKey });
export const mediacheck = new MediacheckClient(mediacheckURL);

// The words the apps use when their metadata service cannot be reached; the same list as the
// probes' (test/probes/gate.go).
const metadataWords = [
  "unable to communicate",
  "skyhook",
  "radarrapi",
  "metadata",
  "timed out",
  "timeout",
  "unavailable",
  "upstream",
];

/**
 * Whether an error of a lookup or an add, asked of the app directly, is an outage of the
 * app's public metadata service (a timeout, or a 5xx that says so), as the probes judge it.
 */
export function metadataUnavailable(err: unknown): boolean {
  if (simulateOutage) return true;
  if (err instanceof HttpError) {
    const body = err.body.toLowerCase();
    return err.status >= 500 && metadataWords.some((w) => body.includes(w));
  }
  const name = (err as { name?: string } | null)?.name;
  return name === "TimeoutError" || name === "AbortError";
}
