import http from "node:http";
import { baseURL, composeProject, dockerSocket, type Service } from "./env";

// Restarting a container needs the container runtime. The runner talks to the Engine API on
// the host's socket directly (mounted in the test profile only), so it needs no docker client.
function engine<T>(method: string, apiPath: string): Promise<T | undefined> {
  return new Promise((resolve, reject) => {
    const req = http.request({ socketPath: dockerSocket, path: apiPath, method }, (res) => {
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
    });
    req.on("error", reject);
    req.end();
  });
}

async function containerId(service: Service): Promise<string> {
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

export const stack = {
  /** Restart a Fukku container (a stop and a start, like `docker restart`). */
  async restart(service: Service): Promise<void> {
    const id = await containerId(service);
    await engine("POST", `/containers/${id}/restart?t=10`);
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
