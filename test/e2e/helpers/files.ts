import { mkdir, open } from "node:fs/promises";
import path from "node:path";
import { filesDir } from "./env";

export const files = {
  /**
   * A file of `bytes` bytes that is one big hole: it costs no disk. It is created in the
   * runner's files volume; the returned path is for setInputFiles. `head` (optional) is
   * written at the start, for a test that needs real content there.
   */
  async sparse(name: string, bytes: number, head?: Buffer): Promise<string> {
    if (name !== path.basename(name) || name === "" || name === "." || name === "..") {
      throw new Error(`files.sparse: ${JSON.stringify(name)} is not a plain file name`);
    }
    if (!Number.isSafeInteger(bytes) || bytes < 0 || (head && head.length > bytes)) {
      throw new Error(`files.sparse: bad size ${bytes}`);
    }
    await mkdir(filesDir, { recursive: true });
    const file = path.join(filesDir, name);
    const handle = await open(file, "w");
    try {
      if (head) await handle.write(head, 0, head.length, 0);
      await handle.truncate(bytes);
    } finally {
      await handle.close();
    }
    return file;
  },
};
