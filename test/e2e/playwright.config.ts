import { defineConfig } from "@playwright/test";
import { baseURL } from "./helpers/env";

// The runner is a container of the test stack (test/env/compose.yaml, service e2e); the
// browsers come from its pinned image. global-setup.ts makes the stack answer on the
// contract's 127.0.0.1 addresses from inside that container.
export default defineConfig({
  testDir: ".",
  testMatch: "**/*.spec.ts",
  outputDir: "test-results",
  // One stack, one proxy with global state (session mode, cut uploads): one test at a time.
  workers: 1,
  fullyParallel: false,
  forbidOnly: !!process.env.CI,
  retries: 0,
  timeout: 60_000,
  expect: { timeout: 10_000 },
  reporter: [["list"], ["html", { open: "never", outputFolder: "playwright-report" }]],
  globalSetup: "./global-setup.ts",
  use: {
    baseURL,
    // A trace of every failed test is the artifact CI uploads.
    trace: "retain-on-failure",
    screenshot: "only-on-failure",
    // The runner's container may not create user namespaces.
    launchOptions: { args: ["--no-sandbox"] },
  },
  projects: [{ name: "chromium", use: { browserName: "chromium" } }],
});
