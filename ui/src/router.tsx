import { createRootRoute, createRoute, createRouter, Outlet } from "@tanstack/react-router";
import { HomePage } from "@/pages/home";

const rootRoute = createRootRoute({
  component: () => (
    <div className="min-h-dvh bg-background text-foreground">
      <header className="border-b">
        <div className="mx-auto flex max-w-5xl items-center gap-3 px-4 py-3">
          <img src="/favicon.svg" alt="" className="size-6" />
          <span className="font-semibold tracking-tight">Fukku</span>
        </div>
      </header>
      <main className="mx-auto max-w-5xl px-4 py-8">
        <Outlet />
      </main>
    </div>
  ),
});

const indexRoute = createRoute({ getParentRoute: () => rootRoute, path: "/", component: HomePage });

export const router = createRouter({ routeTree: rootRoute.addChildren([indexRoute]) });

declare module "@tanstack/react-router" {
  interface Register {
    router: typeof router;
  }
}
