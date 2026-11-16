// Data for the mock match endpoints (#37). Titles are the Blender open films (Creative Commons)
// and made-up shows; nothing here comes from a real library.

import type { Apps, MovieHit, SeriesHit } from "@/api/types";

export const apps: Apps = {
  radarr: {
    qualityProfiles: [
      { id: 4, name: "HD-1080p" },
      { id: 5, name: "Ultra-HD" },
    ],
    rootFolders: [{ path: "/data/media/movies" }],
    defaults: { qualityProfileId: 4, rootFolder: "/data/media/movies", monitored: true },
  },
  sonarr: {
    qualityProfiles: [
      { id: 1, name: "Any" },
      { id: 2, name: "HD-720p" },
    ],
    rootFolders: [{ path: "/data/media/tv" }, { path: "/data/media/anime" }],
    seriesTypes: ["standard", "anime", "daily"],
    defaults: { qualityProfileId: null, rootFolder: "/data/media/tv", seriesType: "standard" },
  },
};

export const movieHits: MovieHit[] = [
  {
    tmdbId: 10378,
    title: "Big Buck Bunny",
    year: 2008,
    overview: "A giant rabbit with a heart bigger than himself deals with three bullying rodents.",
    inLibrary: false,
    hasFile: false,
  },
  {
    tmdbId: 45745,
    title: "Sintel",
    year: 2010,
    overview: "A young woman searches for the baby dragon she befriended, and finds it grown up.",
    inLibrary: true,
    hasFile: true,
  },
  {
    tmdbId: 133701,
    title: "Tears of Steel",
    year: 2012,
    overview: "A group of warriors and scientists try to save the world from killer robots.",
    inLibrary: true,
    hasFile: false,
  },
];

export const seriesHits: SeriesHit[] = [
  {
    tvdbId: 9001,
    title: "Example Show",
    year: 2010,
    overview: "An invented show used to try the screens.",
    inLibrary: false,
    seasons: [1, 2],
  },
  {
    tvdbId: 9002,
    title: "Other Show",
    year: 2015,
    overview: "Another invented show, already in the library.",
    inLibrary: true,
    seasons: [1],
  },
];

/** The same hits, as the mock lookup answers them: by title words, `tmdb:<id>` or `tvdb:<id>`. */
export function searchMovies(term: string): MovieHit[] {
  const id = /^tmdb:(\d+)$/.exec(term.trim());
  if (id) return movieHits.filter((h) => h.tmdbId === Number(id[1]));
  const q = term.trim().toLowerCase();
  return movieHits.filter((h) => h.title.toLowerCase().includes(q));
}

export function searchSeries(term: string): SeriesHit[] {
  const id = /^tvdb:(\d+)$/.exec(term.trim());
  if (id) return seriesHits.filter((h) => h.tvdbId === Number(id[1]));
  const q = term.trim().toLowerCase();
  return seriesHits.filter((h) => h.title.toLowerCase().includes(q));
}
