import { defineConfig } from "@playwright/test";
import base from "./playwright.config";

// The README's screenshots (make screenshots): screenshots.ts only, in English, at one
// viewport, into screenshots/ (the make target copies them to docs/images). `make e2e` does not
// run them: its configuration matches *.spec.ts only.
export default defineConfig({
  ...base,
  testMatch: "screenshots.ts",
  timeout: 15 * 60_000,
  use: {
    ...base.use,
    viewport: { width: 1200, height: 800 },
    deviceScaleFactor: 1,
    locale: "en-US",
    timezoneId: "UTC",
    colorScheme: "light",
    actionTimeout: 60_000,
  },
});
