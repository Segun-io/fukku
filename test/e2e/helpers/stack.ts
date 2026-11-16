import http from "node:http";
import { type AppService, baseURL, composeProject, dockerSocket, type Service } from "./env";

// Restarting a container needs the container runtime. The runner talks to the Engine API on
// the host's socket directly (mounted in the test profile only), so it needs no docker client.
function engine<T>(method: string, apiPath: string, body?: unknown): Promise<T | undefined> {
  return new Promise((resolve, reject) => {
    const payload = body === undefined ? undefined : JSON.stringify(body);
    const headers = payload
      ? { "Content-Type": "application/json", "Content-Length": Buffer.byteLength(payload) }
      : undefined;
    const req = http.request(
      { socketPath: dockerSocket, path: apiPath, method, headers },
      (res) => {
        const chunks: Buffer[] = [];
        res.on("data", (c: Buffer) => chunks.push(c));
        res.on("end", () => {
          const text = Buffer.concat(chunks).toString();
          if ((res.statusCode ?? 500) >= 300) {
            reject(new Error(`docker ${method} ${apiPath}: ${res.statusCode} ${text}`));
          } else {
            resolve(text ? (JSON.parse(text) as T) : undefined);
          }
        });
      },
    );
    req.on("error", reject);
    req.end(payload);
  });
}

async function containerId(service: Service | AppService): Promise<string> {
  const filters = encodeURIComponent(
    JSON.stringify({
      label: [
        `com.docker.compose.project=${composeProject}`,
        `com.docker.compose.service=${service}`,
      ],
    }),
  );
  const list = await engine<{ Id: string }[]>("GET", `/containers/json?filters=${filters}`);
  const found = list?.[0];
  if (!found) throw new Error(`no running container for ${composeProject}/${service}`);
  return found.Id;
}

async function health(service: Service): Promise<string> {
  const id = await containerId(service);
  const info = await engine<{ State: { Running: boolean; Health?: { Status: string } } }>(
    "GET",
    `/containers/${id}/json`,
  );
  if (!info?.State.Running) return "stopped";
  return info.State.Health?.Status ?? "running";
}

async function healthzOk(): Promise<boolean> {
  try {
    return (await fetch(`${baseURL}/healthz`, { signal: AbortSignal.timeout(3000) })).ok;
  } catch {
    return false;
  }
}

/** What `docker inspect` tells about a Fukku container, the parts the tests look at. */
export interface Inspect {
  HostConfig: {
    NetworkMode: string;
    ReadonlyRootfs: boolean;
    CapAdd: string[] | null;
    CapDrop: string[] | null;
    SecurityOpt: string[] | null;
  };
  NetworkSettings: { Networks: Record<string, { IPAddress: string }> };
}

/** A command's output, split by the Engine's stream framing (8-byte header per frame). */
function frames(raw: Buffer): { stdout: string; stderr: string } {
  let stdout = "";
  let stderr = "";
  for (let i = 0; i + 8 <= raw.length; ) {
    const size = raw.readUInt32BE(i + 4);
    const text = raw.subarray(i + 8, i + 8 + size).toString();
    if (raw[i] === 2) stderr += text;
    else stdout += text;
    i += 8 + size;
  }
  return { stdout, stderr };
}

export const stack = {
  /** `docker inspect` of a Fukku container. */
  async inspect(service: Service): Promise<Inspect> {
    const info = await engine<Inspect>("GET", `/containers/${await containerId(service)}/json`);
    if (!info) throw new Error(`inspect ${service}: empty answer`);
    return info;
  },

  /** Every address of a Fukku container, on each network it is attached to. */
  async addresses(service: Service): Promise<string[]> {
    const info = await stack.inspect(service);
    return Object.values(info.NetworkSettings.Networks)
      .map((n) => n.IPAddress)
      .filter(Boolean);
  },

  /**
   * Run a command inside a running Fukku container as the container's own user (`docker exec`)
   * and return its exit code and output.
   */
  async exec(
    service: Service,
    cmd: string[],
  ): Promise<{ code: number; stdout: string; stderr: string }> {
    const id = await containerId(service);
    const created = await engine<{ Id: string }>("POST", `/containers/${id}/exec`, {
      Cmd: cmd,
      AttachStdout: true,
      AttachStderr: true,
    });
    if (!created) throw new Error("exec: no id");
    const body = JSON.stringify({ Detach: false, Tty: false });
    const out = await new Promise<Buffer>((resolve, reject) => {
      const req = http.request(
        {
          socketPath: dockerSocket,
          path: `/exec/${created.Id}/start`,
          method: "POST",
          headers: {
            "Content-Type": "application/json",
            "Content-Length": Buffer.byteLength(body),
          },
        },
        (res) => {
          const chunks: Buffer[] = [];
          res.on("data", (c: Buffer) => chunks.push(c));
          res.on("end", () => resolve(Buffer.concat(chunks)));
        },
      );
      req.on("error", reject);
      req.end(body);
    });
    const info = await engine<{ ExitCode: number }>("GET", `/exec/${created.Id}/json`);
    return { code: info?.ExitCode ?? -1, ...frames(out) };
  },

  /** Restart a Fukku container (a stop and a start, like `docker restart`). */
  async restart(service: Service): Promise<void> {
    const id = await containerId(service);
    await engine("POST", `/containers/${id}/restart?t=10`);
  },

  /**
   * Kill a Fukku container with SIGKILL, as a crash would (no shutdown code runs). Returns
   * when the process is gone, with the time of that (Date.now()) and a function that starts
   * the container again. (The restart policy does not start a killed container by itself.)
   */
  async kill(service: Service): Promise<{ at: number; start: () => Promise<void> }> {
    const id = await containerId(service);
    await engine("POST", `/containers/${id}/kill?signal=KILL`);
    for (let i = 0; ; i++) {
      const info = await engine<{ State: { Running: boolean } }>("GET", `/containers/${id}/json`);
      if (!info?.State.Running) break;
      if (i >= 100) throw new Error(`${service} still runs 10 s after SIGKILL`);
      await new Promise((r) => setTimeout(r, 100));
    }
    const at = Date.now();
    return { at, start: () => engine("POST", `/containers/${id}/start`).then(() => {}) };
  },

  /**
   * Freeze one of the test environment's apps (`docker pause`): it accepts connections but
   * answers nothing until `unpause`, like an app that hangs. Returns the thaw function.
   */
  async freezeApp(app: AppService): Promise<() => Promise<void>> {
    const id = await containerId(app);
    await engine("POST", `/containers/${id}/pause`);
    return () => engine("POST", `/containers/${id}/unpause`).then(() => {});
  },

  /** What a Fukku container logged since a time (ms since the epoch). */
  async logs(service: Service, sinceMs: number): Promise<string> {
    const id = await containerId(service);
    const since = Math.floor(sinceMs / 1000);
    const raw = await new Promise<Buffer>((resolve, reject) => {
      const req = http.request(
        {
          socketPath: dockerSocket,
          path: `/containers/${id}/logs?stdout=1&stderr=1&since=${since}`,
          method: "GET",
        },
        (res) => {
          const chunks: Buffer[] = [];
          res.on("data", (c: Buffer) => chunks.push(c));
          res.on("end", () => resolve(Buffer.concat(chunks)));
        },
      );
      req.on("error", reject);
      req.end();
    });
    const { stdout, stderr } = frames(raw);
    return stdout + stderr;
  },

  /** Restart one of the test environment's apps (Radarr or Sonarr) and wait for its API. */
  async restartApp(app: AppService, ping: () => Promise<unknown>, timeoutMs = 180_000) {
    const id = await containerId(app);
    await engine("POST", `/containers/${id}/restart?t=30`);
    const deadline = Date.now() + timeoutMs;
    let last = "";
    while (Date.now() < deadline) {
      try {
        await ping();
        return;
      } catch (err) {
        last = String(err);
      }
      await new Promise((r) => setTimeout(r, 1000));
    }
    throw new Error(`${app} did not answer within ${timeoutMs} ms of its restart (${last})`);
  },

  /**
   * Wait until both Fukku containers report healthy and /healthz answers through the proxy,
   * on three checks in a row. It polls observable state; it never sleeps a fixed time.
   */
  async waitReady(timeoutMs = 90_000): Promise<void> {
    const deadline = Date.now() + timeoutMs;
    let streak = 0;
    let last = "";
    while (Date.now() < deadline) {
      try {
        const [web, worker, ok] = await Promise.all([
          health("fukku-web"),
          health("fukku-worker"),
          healthzOk(),
        ]);
        last = `web ${web}, worker ${worker}, healthz ${ok ? "ok" : "failing"}`;
        streak = web === "healthy" && worker === "healthy" && ok ? streak + 1 : 0;
      } catch (err) {
        last = String(err);
        streak = 0;
      }
      if (streak >= 3) return;
      await new Promise((r) => setTimeout(r, 500));
    }
    throw new Error(`the stack was not ready after ${timeoutMs} ms (${last})`);
  },
};
