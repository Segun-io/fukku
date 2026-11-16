import { useQuery } from "@tanstack/react-query";
import { apiFetch } from "./client";
import type { AdminSettings, AdminStatus } from "./types";

export const adminStatusKey = ["admin", "status"] as const;
export const adminSettingsKey = ["admin", "settings"] as const;

/** Health of the server (admins only). Read when the page opens and when asked again. */
export function useAdminStatus(enabled: boolean) {
  return useQuery<AdminStatus, Error>({
    queryKey: adminStatusKey,
    queryFn: ({ signal }) => apiFetch<AdminStatus>("/api/admin/status", { signal }),
    enabled,
    staleTime: 0,
  });
}

/** The effective settings, secrets redacted by the server (admins only). */
export function useAdminSettings(enabled: boolean) {
  return useQuery<AdminSettings, Error>({
    queryKey: adminSettingsKey,
    queryFn: ({ signal }) => apiFetch<AdminSettings>("/api/admin/settings", { signal }),
    enabled,
    staleTime: 60_000,
  });
}
