import {
  createRootRoute,
  createRoute,
  createRouter,
  Link,
  Outlet,
  type RouterHistory,
  redirect,
} from "@tanstack/react-router";
import { useSession } from "@/api/session";
import { useT } from "@/i18n";
import { LanguageSwitch } from "@/i18n/language-switch";
import { JobDetailPage } from "@/pages/job-detail";
import { JobsPage } from "@/pages/jobs";
import { UploadPage } from "@/pages/upload";

// The shell is #8's; this one only has the links, the language switch and the banner.
function Root() {
  const { t } = useT();
  const session = useSession();
  return (
    <div className="min-h-dvh bg-background text-foreground">
      <header className="border-b">
        <div className="mx-auto flex max-w-5xl items-center gap-4 px-4 py-3">
          <img src="/favicon.svg" alt="" className="size-6" />
          <span className="font-semibold tracking-tight">Fukku</span>
          <nav className="flex gap-3 text-sm">
            <Link to="/upload">{t("nav.upload")}</Link>
            <Link to="/jobs">{t("nav.jobs")}</Link>
          </nav>
          <div className="ml-auto">
            <LanguageSwitch />
          </div>
        </div>
        {session.data?.insecureDev && (
          <p role="alert" className="bg-destructive px-4 py-1 text-center text-sm text-white">
            {t("shell.insecureDev")}
          </p>
        )}
      </header>
      <main className="mx-auto max-w-5xl px-4 py-8">
        <Outlet />
      </main>
    </div>
  );
}

const rootRoute = createRootRoute({ component: Root });

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

const routeTree = rootRoute.addChildren([indexRoute, uploadRoute, jobsRoute, jobRoute]);

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
