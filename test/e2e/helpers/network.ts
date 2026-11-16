import { control } from "./control";

/** Break upload connections on demand, at the proxy. */
export const network = {
  /**
   * For the next `times` (default 1) upload PATCH requests, forward `afterBytes` of the body
   * and then drop the connection.
   */
  cutUploads: ({ afterBytes, times = 1 }: { afterBytes: number; times?: number }) =>
    control("/control/cut", { afterBytes, times }),
  /** Back to the defaults: no cuts, session ok. */
  reset: () => control("/control/reset"),
};
