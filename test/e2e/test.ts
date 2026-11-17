import { test as base, expect } from "@playwright/test";

// `test` of every spec: Playwright's, plus a guard that fails a test when the page broke the
// Content-Security-Policy. The policy (internal/web/server.go) allows only what the UI needs,
// so a blocked script, style, image, font, media or connection means the policy and the UI
// disagree. Browsers report a violation as a `securitypolicyviolation` event on the page and as
// a "Refused to ..." message in the console; either one fails the test.
export const test = base.extend<{ cspGuard: undefined }>({
  cspGuard: [
    async ({ context }, use) => {
      const violations: string[] = [];
      await context.exposeBinding("__fukkuCspViolation", (_source, what: string) => {
        violations.push(what);
      });
      await context.addInitScript(() => {
        document.addEventListener("securitypolicyviolation", (e) => {
          const report = (window as unknown as Record<string, (s: string) => void>)
            .__fukkuCspViolation;
          report?.(`${e.violatedDirective} blocked ${e.blockedURI || "inline"} (${e.sourceFile})`);
        });
      });
      context.on("console", (message) => {
        if (/Refused to|violates the following Content Security Policy/.test(message.text())) {
          violations.push(message.text());
        }
      });
      await use(undefined);
      expect(violations, "Content-Security-Policy violations").toEqual([]);
    },
    { auto: true },
  ],
});

export { expect };
