// The addresses and names of the test stack contract (issue #30, test/env/compose.yaml).
export const proxyPort = 18080;
export const controlPort = 18081;
export const baseURL = `http://127.0.0.1:${proxyPort}`;
export const controlURL = `http://127.0.0.1:${controlPort}`;

export type TestUser = "user1" | "user2" | "admin1";
export type Service = "fukku-web" | "fukku-worker";

/** The Compose project of the stack, to find its containers by label. */
export const composeProject = process.env.FUKKU_E2E_COMPOSE_PROJECT ?? "fukku-test";
/** The container runtime's socket, mounted into the runner by the test profile only. */
export const dockerSocket = process.env.FUKKU_E2E_DOCKER_SOCKET ?? "/var/run/docker.sock";
/** Where files.sparse creates files: a volume of the runner. */
export const filesDir = process.env.FUKKU_E2E_FILES_DIR ?? "/files";
