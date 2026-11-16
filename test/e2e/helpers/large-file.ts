import { createHash } from "node:crypto";
import { open, readFile, statfs } from "node:fs/promises";
import { files } from "./files";

// The large file of exit-m1-large-upload.spec.ts: a real video at the start, then a hole up to
// the target size, so it costs (almost) nothing on the sending side and still passes the
// content check. Every MiB after the video carries a 4 KiB marker made from its own offset,
// so a byte sent twice, lost or written at the wrong place changes the file's hash; a file
// of zeros would not show that. The markers cost 4 KiB per MiB in the runner's tmpfs.

export const MiB = 1 << 20;
export const GiB = 1 << 30;
const markerSize = 4096;

/** "5GiB", "512MiB", "1073741824" → bytes. */
export function parseSize(text: string): number {
  const m = /^(\d+)(KiB|MiB|GiB|TiB)?$/.exec(text.trim());
  if (!m?.[1]) throw new Error(`not a size: ${JSON.stringify(text)} (use bytes or KiB, MiB, GiB)`);
  const unit = { KiB: 1 << 10, MiB, GiB, TiB: 1024 * GiB }[m[2] ?? ""] ?? 1;
  const n = Number(m[1]) * unit;
  if (!Number.isSafeInteger(n) || n <= 0) throw new Error(`size out of range: ${text}`);
  return n;
}

export function formatSize(bytes: number): string {
  return bytes >= GiB ? `${(bytes / GiB).toFixed(2)} GiB` : `${(bytes / MiB).toFixed(1)} MiB`;
}

function marker(offset: number): Buffer {
  const seed = createHash("sha256").update(`fukku-large-upload ${offset}`).digest();
  return Buffer.concat(Array.from({ length: markerSize / seed.length }, () => seed));
}

/** Write the large file into the runner's files volume; returns its path. */
export async function largeFile(name: string, size: number, video: string): Promise<string> {
  const head = await readFile(video);
  const file = await files.sparse(name, size, head);
  const handle = await open(file, "r+");
  try {
    const first = Math.ceil(head.length / MiB) * MiB;
    for (let at = first; at + markerSize <= size; at += MiB) {
      const m = marker(at);
      await handle.write(m, 0, m.length, at);
    }
  } finally {
    await handle.close();
  }
  return file;
}

/** The SHA-256 of bytes [start, end) of a file, read in 16 MiB blocks. */
export async function sha256(file: string, start = 0, end?: number): Promise<string> {
  const handle = await open(file, "r");
  try {
    const stop = end ?? (await handle.stat()).size;
    const hash = createHash("sha256");
    const buf = Buffer.alloc(16 * MiB);
    for (let at = start; at < stop; ) {
      const { bytesRead } = await handle.read(buf, 0, Math.min(buf.length, stop - at), at);
      if (bytesRead === 0) break;
      hash.update(buf.subarray(0, bytesRead));
      at += bytesRead;
    }
    return hash.digest("hex");
  } finally {
    await handle.close();
  }
}

/** Bytes free for an unprivileged writer on the filesystem that holds `dir`. */
export async function freeBytes(dir: string): Promise<number> {
  const s = await statfs(dir);
  return s.bavail * s.bsize;
}
