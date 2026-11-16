import { control } from "./control";

/** Make the proxy behave as if the sign-in session had expired, and bring it back. */
export const session = {
  /** "401": every request gets 401. "302": every request is redirected to the sign-in page. */
  expire: (kind: "401" | "302") => control("/control/session", { mode: `expired-${kind}` }),
  restore: () => control("/control/session", { mode: "ok" }),
};
