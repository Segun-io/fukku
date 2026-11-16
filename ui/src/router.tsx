import {
  createRootRoute,
  createRoute,
  createRouter,
  lazyRouteComponent,
  type RouterHistory,
  redirect,
} from "@tanstack/react-router";
import { AppShell } from "@/components/shell/app-shell";
import { NotFound } from "@/components/shell/states";
import { JobDetailPage } from "@/pages/job-detail";
import { JobsPage } from "@/pages/jobs";
import { MatchPage } from "@/pages/match";
import { UploadPage } from "@/pages/upload";

const rootRoute = createRootRoute({ component: AppShell, notFoundComponent: NotFound });

const indexRoute = createRoute({
  getParentRoute: () => rootRoute,
  path: "/",
  beforeLoad: () => {
    throw redirect({ to: "/jobs", replace: true });
  },
});
const uploadRoute = createRoute({
  getParentRoute: () => rootRoute,
  path: "/upload",
  component: UploadPage,
});
const jobsRoute = createRoute({
  getParentRoute: () => rootRoute,
  path: "/jobs",
  component: JobsPage,
});
const jobRoute = createRoute({
  getParentRoute: () => rootRoute,
  path: "/jobs/$id",
  component: JobDetailPage,
});

const matchRoute = createRoute({
  getParentRoute: () => rootRoute,
  path: "/jobs/$id/match",
  component: MatchPage,
});

// The pattern examples (docs/design.md) exist in development builds only: `npm run dev`.
// In a production build this branch, and the page behind it, are removed.
const devRoutes = import.meta.env.DEV
  ? [
      createRoute({
        getParentRoute: () => rootRoute,
        path: "/design",
        component: lazyRouteComponent(() => import("@/pages/design"), "DesignPage"),
      }),
    ]
  : [];

const routeTree = rootRoute.addChildren([
  indexRoute,
  uploadRoute,
  jobsRoute,
  jobRoute,
  matchRoute,
  ...devRoutes,
]);

/** Tests pass a memory history. */
export function createAppRouter(history?: RouterHistory) {
  return createRouter({ routeTree, history });
}

export const router = createAppRouter();

declare module "@tanstack/react-router" {
  interface Register {
    router: typeof router;
  }
}
