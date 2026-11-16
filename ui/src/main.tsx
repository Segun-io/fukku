import { RouterProvider } from "@tanstack/react-router";
import { StrictMode } from "react";
import { createRoot } from "react-dom/client";
import { createQueryClient, Providers } from "@/providers";
import { router } from "@/router";
import "./index.css";

const queryClient = createQueryClient();

async function start() {
  // `npm run dev:mock` answers the API in the browser. The branch is removed from the
  // production build, so MSW is never shipped.
  if (import.meta.env.MODE === "mock") {
    const { worker } = await import("@/mocks/browser");
    await worker.start({ onUnhandledFrame: "bypass" });
  }

  const root = document.getElementById("root");
  if (!root) throw new Error("missing #root");

  createRoot(root).render(
    <StrictMode>
      <Providers client={queryClient}>
        <RouterProvider router={router} />
      </Providers>
    </StrictMode>,
  );
}

void start();
