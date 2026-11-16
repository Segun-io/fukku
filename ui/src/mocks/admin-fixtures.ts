// What the admin endpoints (#69) answer in the mock API. Every value is made up.

import type { AdminSetting, AdminSettings, AdminStatus } from "@/api/types";
import { jobStates } from "@/api/types";

const counts: Record<string, number> = {
  uploading: 1,
  done: 2,
  failed: 1,
  "awaiting-confirmation": 2,
};

export const adminStatus: AdminStatus = {
  version: "0.1.0",
  readiness: {
    status: "degraded",
    checks: [
      { name: "database", ok: true },
      { name: "spool", ok: true },
      { name: "sentinels", ok: true },
      { name: "intakeRoot", ok: true },
      { name: "sameFilesystem", ok: true },
      { name: "workerHeartbeat", ok: true },
      { name: "radarr", ok: true },
      { name: "sonarr", ok: false, detail: "sonarr is unreachable" },
    ],
  },
  apps: [
    { name: "radarr", version: "5.2.1.8000" },
    { name: "sonarr", version: "" },
  ],
  worker: { heartbeatAgeSeconds: 7, maxAgeSeconds: 120 },
  jobs: {
    total: 6,
    byState: Object.fromEntries(jobStates.map((s) => [s, counts[s] ?? 0])),
  },
  disk: { freeBytes: 412_316_860_416, marginBytes: 107_374_182_400, aboveMargin: true },
  checkedAt: "2026-01-01T12:00:00Z",
};

const setting = (
  key: string,
  env: string,
  value: string,
  source: AdminSetting["source"],
  secret = false,
): AdminSetting => ({ key, env, secret, value, source });

export const adminSettings: AdminSettings = {
  settings: [
    setting("server.listenAddress", "FUKKU_SERVER_LISTEN_ADDRESS", ":8080", "default"),
    setting(
      "server.publicBaseUrl",
      "FUKKU_SERVER_PUBLIC_BASE_URL",
      "https://fukku.example.com",
      "set",
    ),
    setting("auth.adminGroup", "FUKKU_AUTH_ADMIN_GROUP", "admins", "set"),
    setting("radarr.url", "FUKKU_RADARR_URL", "http://radarr.example.com:7878", "set"),
    setting("radarr.apiKey", "FUKKU_RADARR_API_KEY", "[redacted]", "set", true),
    setting("sonarr.apiKey", "FUKKU_SONARR_API_KEY", "", "default", true),
    setting("upload.freeSpaceMargin", "FUKKU_UPLOAD_FREE_SPACE_MARGIN", "100GiB", "default"),
  ],
};
