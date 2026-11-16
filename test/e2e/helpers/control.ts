import { controlURL } from "./env";

/** POST to the test proxy's control API (never forwarded to Fukku). */
export async function control(path: string, body?: unknown): Promise<void> {
  const res = await fetch(`${controlURL}${path}`, {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: body === undefined ? undefined : JSON.stringify(body),
  });
  if (!res.ok) throw new Error(`control ${path}: ${res.status} ${await res.text()}`);
}
