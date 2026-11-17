import { lstat, readdir, readlink, rm, writeFile } from "node:fs/promises";
import path from "node:path";
import { dataDir } from "./env";

// The library filesystem (/data) as the runner sees it, for the tests that must show nothing
// changed outside a job's directory.

/** One line per entry: its path, its kind, its size or link target. Directories carry no size. */
export type Listing = Map<string, string>;

async function walk(dir: string, rel: string, out: Listing, skip: (rel: string) => boolean) {
  for (const name of await readdir(dir)) {
    const entry = rel === "" ? name : `${rel}/${name}`;
    if (skip(entry)) continue;
    const full = path.join(dir, name);
    const st = await lstat(full);
    if (st.isSymbolicLink()) out.set(entry, `symlink -> ${await readlink(full)}`);
    else if (st.isDirectory()) {
      out.set(entry, "dir");
      await walk(full, entry, out, skip);
    } else if (st.isFile()) out.set(entry, `file ${st.size}`);
    else out.set(entry, "special");
  }
}

export const data = {
  /**
   * Everything under /data except `skip` (paths relative to /data, applied to every entry).
   * It records names, kinds and sizes, not times: Radarr and Jellyfin may touch their own files.
   */
  async list(skip: (rel: string) => boolean = () => false): Promise<Listing> {
    const out: Listing = new Map();
    await walk(dataDir, "", out, skip);
    return out;
  },

  /**
   * What a job's directory holds (or, with top "worker", the worker's folder of the job), as
   * the same kind of listing, relative to it.
   */
  async jobTree(jobId: string, top: "jobs" | "worker" = "jobs"): Promise<Listing> {
    const out: Listing = new Map();
    const dir = path.join(dataDir, "intake", top, jobId);
    // The worker's folder exists once a task of the job ran.
    if (top === "worker" && !(await lstat(dir).catch(() => null))) return out;
    await walk(dir, "", out, () => false);
    return out;
  },

  /** The files of one directory tree that are not regular files or directories. */
  oddEntries(listing: Listing): string[] {
    return [...listing]
      .filter(([, v]) => v === "special" || v.startsWith("symlink"))
      .map(([k]) => k);
  },

  sentinel: path.join(dataDir, ".mounted"),

  removeSentinel: () => rm(path.join(dataDir, ".mounted")),
  restoreSentinel: () => writeFile(path.join(dataDir, ".mounted"), ""),
};

/** The differences between two listings, as readable lines (empty when equal). */
export function diff(before: Listing, after: Listing): string[] {
  const lines: string[] = [];
  for (const [k, v] of after) {
    if (!before.has(k)) lines.push(`added   ${k} (${v})`);
    else if (before.get(k) !== v) lines.push(`changed ${k}: ${before.get(k)} -> ${v}`);
  }
  for (const k of before.keys()) if (!after.has(k)) lines.push(`removed ${k}`);
  return lines;
}
