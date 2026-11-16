// Typed calls to the Fukku web process. Everything is same-origin; the reverse proxy in
// front of Fukku handles sign-in.

export type Health = { status: string; version: string };

export async function getHealth(signal?: AbortSignal): Promise<Health> {
  const res = await fetch("/healthz", { signal, headers: { Accept: "application/json" } });
  if (!res.ok) throw new Error(`healthz returned ${res.status}`);
  return (await res.json()) as Health;
}
