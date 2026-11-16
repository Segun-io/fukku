// The addresses and names of the test stack contract (issue #30, test/env/compose.yaml).
export const proxyPort = 18080;
export const controlPort = 18081;
export const baseURL = `http://127.0.0.1:${proxyPort}`;
export const controlURL = `http://127.0.0.1:${controlPort}`;

export type TestUser = "user1" | "user2" | "admin1";
export type Service = "fukku-web" | "fukku-worker";
/** The test environment's apps, which only the M2 exit tests restart (Radarr, Sonarr). */
export type AppService = "radarr" | "sonarr";

/** The Compose project of the stack, to find its containers by label. */
export const composeProject = process.env.FUKKU_E2E_COMPOSE_PROJECT ?? "fukku-test";
/** The container runtime's socket, mounted into the runner by the test profile only. */
export const dockerSocket = process.env.FUKKU_E2E_DOCKER_SOCKET ?? "/var/run/docker.sock";
/** Where files.sparse creates files: a volume of the runner. */
export const filesDir = process.env.FUKKU_E2E_FILES_DIR ?? "/files";

/** The web process, straight (it answers 403 to anyone but the proxy), and the test Radarr. */
export const webURL = "http://fukku-web:8080";
export const radarrURL = "http://radarr:7878";
export const radarrKey = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"; // fake, valid only in the test environment
/** The other apps of the test environment, with their fixed fake keys (test/env/compose.yaml). */
export const sonarrURL = "http://sonarr:8989";
export const sonarrKey = "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb";
export const bazarrURL = "http://bazarr:6767";
export const bazarrKey = "cccccccccccccccccccccccccccccccc";
export const jellyfinURL = "http://jellyfin:8096";
export const jellyfinKey = "dddddddddddddddddddddddddddddddd";
/** The media server's title link template of the dev stack (test/env/fukku.yaml). */
export const titleLinkTemplate = "http://127.0.0.1:18096/web/#/details?id={itemId}";

/**
 * Tests tagged @online add titles through the *arr apps' public metadata services: they run
 * only with FUKKU_E2E_ONLINE=1 (make e2e sets it unless E2E_ONLINE=0; CI's pull requests run
 * offline). FUKKU_E2E_SIMULATE_METADATA_OUTAGE=1 makes them skip as on an upstream outage,
 * without asking the internet.
 */
export const online = process.env.FUKKU_E2E_ONLINE === "1";
export const simulateOutage = process.env.FUKKU_E2E_SIMULATE_METADATA_OUTAGE === "1";
/** The library filesystem as the runner sees it, and the generated upload fixtures. */
export const dataDir = process.env.FUKKU_E2E_DATA_DIR ?? "/data";
export const fixturesDir = process.env.FUKKU_E2E_FIXTURES_DIR ?? "/fixtures";
