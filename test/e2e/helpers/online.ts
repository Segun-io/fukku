import { test } from "@playwright/test";
import { metadataUnavailable, radarr, sonarr } from "./apps";
import { online, simulateOutage } from "./env";

// The tests tagged @online (the M2 and M3 exit criteria) add titles through the apps' public
// metadata services. They run only with FUKKU_E2E_ONLINE=1, and an outage of that service is
// a skip, judged by asking the app directly as the probes do; anything else stays a failure.

const upstream = "upstream metadata service unavailable";

/**
 * The first line of every online test: skips without FUKKU_E2E_ONLINE=1, and when the app
 * cannot reach its metadata service.
 */
export async function needMetadata(app: "radarr" | "sonarr", id: number): Promise<void> {
  test.skip(!online, "needs the internet: make e2e (FUKKU_E2E_ONLINE=1)");
  test.skip(simulateOutage, `${upstream} (simulated by FUKKU_E2E_SIMULATE_METADATA_OUTAGE=1)`);
  try {
    await (app === "radarr" ? radarr.lookup(id) : sonarr.lookup(id));
  } catch (err) {
    test.skip(metadataUnavailable(err), `${upstream}: ${err}`);
    throw err;
  }
}

/** A refusal of Fukku's that may be the upstream outage (502 arr_unavailable): skip if so. */
export async function outageOr(
  app: "radarr" | "sonarr",
  id: number,
  what: string,
  status: number,
  body: unknown,
): Promise<never> {
  if (status === 502) {
    try {
      await (app === "radarr" ? radarr.lookup(id) : sonarr.lookup(id));
    } catch (err) {
      test.skip(metadataUnavailable(err), `${upstream}: ${what}: ${err}`);
    }
  }
  throw new Error(`${what}: ${status} ${JSON.stringify(body)}`);
}
