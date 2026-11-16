import { createMemoryHistory, RouterProvider } from "@tanstack/react-router";
import { render } from "@testing-library/react";
import { createQueryClient, Providers } from "@/providers";
import { createAppRouter } from "@/router";

/** Renders the whole app at `url`, against the MSW handlers. */
export function renderApp(url = "/") {
  const router = createAppRouter(createMemoryHistory({ initialEntries: [url] }));
  const client = createQueryClient();
  client.setDefaultOptions({ queries: { retry: false } });
  const view = render(
    <Providers client={client}>
      <RouterProvider router={router} />
    </Providers>,
  );
  return { router, client, ...view };
}
