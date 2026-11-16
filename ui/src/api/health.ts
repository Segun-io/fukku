import { apiFetch } from "./client";
import type { Health } from "./types";

export function getHealth(signal?: AbortSignal): Promise<Health> {
  return apiFetch<Health>("/healthz", { signal });
}
