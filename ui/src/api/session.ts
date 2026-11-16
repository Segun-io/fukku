import { useQuery } from "@tanstack/react-query";
import { apiFetch, isPermanent } from "./client";
import type { Session } from "./types";

export const sessionKey = ["session"] as const;

export function getSession(signal?: AbortSignal): Promise<Session> {
  return apiFetch<Session>("/api/session", { signal });
}

/** The signed-in user and the server's settings the UI needs. Read first, changes rarely. */
export function useSession() {
  return useQuery({
    queryKey: sessionKey,
    queryFn: ({ signal }) => getSession(signal),
    staleTime: 5 * 60_000,
    retry: (count, err) => !isPermanent(err) && count < 1,
  });
}
