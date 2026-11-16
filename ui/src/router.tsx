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
import { ConfirmPage } from "@/features/confirm/confirm-page";
import { AdminPage } from "@/pages/admin";
import { JobDetailPage } from "@/pages/job-detail";
import { JobsPage } from "@/pages/jobs";
import { MappingPage } from "@/pages/mapping";
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

const adminRoute = createRoute({
  getParentRoute: () => rootRoute,
  path: "/admin",
  component: AdminPage,
});

const matchRoute = createRoute({
  getParentRoute: () => rootRoute,
  path: "/jobs/$id/match",
  component: MatchPage,
});

const mappingRoute = createRoute({
  getParentRoute: () => rootRoute,
  path: "/jobs/$id/mapping",
  component: MappingPage,
});

const confirmRoute = createRoute({
  getParentRoute: () => rootRoute,
  path: "/jobs/$id/confirm",
  component: ConfirmPage,
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
  mappingRoute,
  adminRoute,
  confirmRoute,
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
