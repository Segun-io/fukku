import { screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { HttpResponse, http } from "msw";
import { afterEach, describe, expect, it } from "vitest";
import { apps, movieHits } from "@/mocks/match-fixtures";
import { server } from "@/mocks/server";
import { renderApp } from "@/test/render";

afterEach(() => server.events.removeAllListeners());

const id = (n: number) => n.toString(16).padStart(32, "0");
/** Job 2 is one video (a film); job 5 is an archive (a show). Both are `probed`. */
const film = id(2);
const show = id(5);
const statusOf: Record<string, number> = {
  arr_unavailable: 502,
  app_not_configured: 404,
  title_not_found: 404,
  bad_request: 400,
};
const open = (jobId: string) => renderApp(`/jobs/${jobId}/match`);

async function search(user: ReturnType<typeof userEvent.setup>, term: string) {
  await user.type(await screen.findByRole("textbox", { name: "Title" }), term);
  await user.click(screen.getByRole("button", { name: "Search" }));
}

describe("the match page", () => {
  it("starts on the film for one video and on the show for an archive", async () => {
    open(film);
    expect(await screen.findByRole("heading", { name: "Choose the film or show" })).toBeVisible();
    expect(screen.getByRole("radio", { name: "A film" })).toBeChecked();
    expect(screen.getByRole("radio", { name: "A show" })).not.toBeChecked();
  });

  it("preselects the show for an archive, and the user can change it", async () => {
    const user = userEvent.setup();
    open(show);
    expect(await screen.findByRole("radio", { name: "A show" })).toBeChecked();
    await user.click(screen.getByRole("radio", { name: "A film" }));
    expect(screen.getByRole("radio", { name: "A film" })).toBeChecked();
  });

  it("offers only the configured apps", async () => {
    server.use(http.get("/api/apps", () => HttpResponse.json({ radarr: apps.radarr })));
    open(show);
    await screen.findByRole("textbox", { name: "Title" });
    expect(screen.queryByRole("radio", { name: "A show" })).not.toBeInTheDocument();
    // Only films can be searched, whatever the files suggest.
    const requests: string[] = [];
    server.events.on("request:start", ({ request }) =>
      requests.push(new URL(request.url).pathname),
    );
    await search(userEvent.setup(), "big");
    await screen.findByRole("radio", { name: /Big Buck Bunny/ });
    expect(requests).toContain("/api/lookup/movies");
    server.events.removeAllListeners();
  });

  it("does not offer an app that did not answer", async () => {
    server.use(
      http.get("/api/apps", () =>
        HttpResponse.json({
          radarr: apps.radarr,
          sonarr: { qualityProfiles: [], rootFolders: [], unavailable: true, defaults: {} },
        }),
      ),
    );
    open(show);
    await screen.findByRole("textbox", { name: "Title" });
    expect(screen.queryByRole("radio", { name: "A show" })).not.toBeInTheDocument();
    // Only films are left, so there is no choice to make.
    expect(screen.queryByRole("radio", { name: "A film" })).not.toBeInTheDocument();
  });

  it("says the apps are not answering when none is available", async () => {
    server.use(
      http.get("/api/apps", () =>
        HttpResponse.json({
          radarr: { qualityProfiles: [], rootFolders: [], unavailable: true, defaults: {} },
        }),
      ),
    );
    open(film);
    expect(
      await screen.findByRole("heading", { name: "Radarr and Sonarr are not answering" }),
    ).toBeVisible();
    expect(screen.getByText(/cannot be reached right now/)).toBeVisible();
  });

  it("cannot add a new title when the app lists no quality profiles", async () => {
    server.use(
      http.get("/api/apps", () =>
        HttpResponse.json({
          radarr: {
            qualityProfiles: [],
            rootFolders: [],
            defaults: { qualityProfileId: null, rootFolder: null, monitored: true },
          },
        }),
      ),
    );
    const user = userEvent.setup();
    open(film);
    await search(user, "big");
    await user.click(await screen.findByRole("radio", { name: /Big Buck Bunny/ }));
    expect(
      screen.getByText("Radarr has no quality profiles. Add one there, then come back."),
    ).toBeVisible();
    expect(screen.getByRole("button", { name: "Use this title" })).toBeDisabled();
  });

  it("says so when no app is configured", async () => {
    server.use(http.get("/api/apps", () => HttpResponse.json({})));
    open(film);
    expect(
      await screen.findByRole("heading", { name: "Radarr and Sonarr are not set up" }),
    ).toBeVisible();
  });

  it("shows the search hint, and finds a film with title, year, overview and library status", async () => {
    server.use(http.get("/api/lookup/movies", () => HttpResponse.json({ results: movieHits })));
    const user = userEvent.setup();
    open(film);
    expect(await screen.findByText(/Try the English title, or an id: tmdb:<id>/)).toBeVisible();
    await search(user, "e");
    const results = await screen.findByRole("group", { name: "Results" });
    expect(await within(results).findAllByRole("radio")).toHaveLength(3);
    const sintel = within(results).getByRole("radio", { name: /Sintel \(2010\)/ });
    expect(sintel.closest("label")).toHaveTextContent("Already in Radarr · Already has a file");
    expect(sintel.closest("label")).toHaveTextContent("A young woman searches for the baby dragon");
    expect(
      within(results)
        .getByRole("radio", { name: /Big Buck Bunny \(2008\)/ })
        .closest("label"),
    ).not.toHaveTextContent("Already in");
  });

  it("finds a title by id", async () => {
    const user = userEvent.setup();
    open(film);
    await search(user, "tmdb:10378");
    expect(await screen.findByRole("radio", { name: /Big Buck Bunny/ })).toBeVisible();
    expect(screen.getAllByRole("radio", { name: /\(\d{4}\)/ })).toHaveLength(1);
  });

  it("explains an empty result and repeats the hint", async () => {
    const user = userEvent.setup();
    open(film);
    await search(user, "zzz");
    expect(await screen.findByRole("heading", { name: "Nothing found for “zzz”" })).toBeVisible();
    expect(
      screen.getByText(/Try the English title, a shorter name, or an id such as/),
    ).toBeVisible();
  });

  it("shows a lookup failure", async () => {
    server.use(
      http.get("/api/lookup/movies", () =>
        HttpResponse.json({ error: { code: "arr_unavailable", message: "x" } }, { status: 502 }),
      ),
    );
    const user = userEvent.setup();
    open(film);
    await search(user, "big");
    expect(await screen.findByText(/Radarr or Sonarr cannot be reached/)).toBeVisible();
  });

  it("pre-fills the film options from the defaults and hides a single root folder", async () => {
    const user = userEvent.setup();
    open(film);
    await search(user, "big");
    await user.click(await screen.findByRole("radio", { name: /Big Buck Bunny/ }));
    expect(screen.getByRole("combobox", { name: "Quality profile" })).toHaveValue("4");
    expect(screen.queryByRole("combobox", { name: "Root folder" })).not.toBeInTheDocument();
    expect(
      screen.getByRole("checkbox", { name: "Replace it when a better copy appears" }),
    ).toBeChecked();
    expect(screen.queryByRole("combobox", { name: "Type of show" })).not.toBeInTheDocument();
  });

  it("sends the film choice and goes back to the job", async () => {
    let body: unknown;
    server.events.on("request:start", async ({ request }) => {
      if (request.method === "POST" && request.url.endsWith("/match")) {
        body = await request.clone().json();
      }
    });
    const user = userEvent.setup();
    const { router } = open(film);
    await search(user, "big");
    await user.click(await screen.findByRole("radio", { name: /Big Buck Bunny/ }));
    await user.selectOptions(screen.getByRole("combobox", { name: "Quality profile" }), "Ultra-HD");
    await user.click(
      screen.getByRole("checkbox", { name: "Replace it when a better copy appears" }),
    );
    await user.click(screen.getByRole("button", { name: "Use this title" }));
    await waitFor(() => expect(router.state.location.pathname).toBe(`/jobs/${film}`));
    expect(body).toEqual({
      app: "radarr",
      tmdbId: 10378,
      qualityProfileId: 5,
      rootFolder: "/data/media/movies",
      monitored: false,
    });
    expect(await screen.findByText("Big Buck Bunny (2008) in Radarr")).toBeVisible();
    server.events.removeAllListeners();
  });

  it("asks for a profile when there is no default, and lists several root folders and the show types", async () => {
    let body: unknown;
    server.events.on("request:start", async ({ request }) => {
      if (request.method === "POST" && request.url.endsWith("/match")) {
        body = await request.clone().json();
      }
    });
    const user = userEvent.setup();
    open(show);
    await search(user, "example");
    await user.click(await screen.findByRole("radio", { name: /Example Show/ }));
    const submit = screen.getByRole("button", { name: "Use this title" });
    expect(submit).toBeDisabled();
    expect(screen.getByText("Choose a quality profile to continue.")).toBeVisible();
    expect(screen.getByRole("combobox", { name: "Root folder" })).toHaveValue("/data/media/tv");
    expect(screen.getByRole("combobox", { name: "Type of show" })).toHaveValue("standard");
    expect(screen.queryByRole("checkbox")).not.toBeInTheDocument();
    await user.selectOptions(screen.getByRole("combobox", { name: "Quality profile" }), "HD-720p");
    await user.selectOptions(screen.getByRole("combobox", { name: "Type of show" }), "Anime");
    await user.selectOptions(
      screen.getByRole("combobox", { name: "Root folder" }),
      "/data/media/anime",
    );
    expect(submit).toBeEnabled();
    await user.click(submit);
    await waitFor(() => expect(body).toBeDefined());
    expect(body).toEqual({
      app: "sonarr",
      tvdbId: 9001,
      qualityProfileId: 2,
      rootFolder: "/data/media/anime",
      seriesType: "anime",
    });
    server.events.removeAllListeners();
  });

  it("shows no options for a title that is already in the app", async () => {
    const user = userEvent.setup();
    open(show);
    await search(user, "other");
    await user.click(await screen.findByRole("radio", { name: /Other Show/ }));
    expect(
      screen.getByText("Other Show is already in Sonarr. Its settings stay as they are."),
    ).toBeVisible();
    const options = screen.getByRole("form", { name: "Options" });
    expect(within(options).queryByRole("combobox")).not.toBeInTheDocument();
    expect(within(options).getByRole("button", { name: "Use this title" })).toBeEnabled();
  });

  it("explains a film that already has a file and offers the manual fallback", async () => {
    const user = userEvent.setup();
    open(film);
    await search(user, "sintel");
    await user.click(await screen.findByRole("radio", { name: /Sintel/ }));
    expect(screen.getByRole("heading", { name: "This film already has a file" })).toBeVisible();
    expect(screen.getByText(/arrives in a later version/)).toBeVisible();
    expect(screen.queryByRole("button", { name: "Use this title" })).not.toBeInTheDocument();
    const manual = within(screen.getByRole("note")).getByRole("link", {
      name: "Import it by hand instead",
    });
    expect(manual).toHaveAttribute("href", `/jobs/${film}`);
  });

  it.each([
    [
      "already_in_library",
      "This film already has a file. Adding another language arrives in a later version.",
    ],
    ["title_in_use", "Another upload is already working on this title. Wait for it to finish."],
    ["title_busy", "This title is downloading right now. Try again when it has finished."],
    ["wrong_state", "That is not possible while the upload is in this state."],
    ["arr_unavailable", "Radarr or Sonarr cannot be reached right now. Try again in a moment."],
    ["app_not_configured", "That app is not set up in Fukku."],
    ["title_not_found", "The app does not know that title any more. Search again."],
    ["bad_request", "The request was not valid."],
  ])("translates the refusal %s", async (code, text) => {
    server.use(
      http.post("/api/jobs/:id/match", () =>
        HttpResponse.json({ error: { code, message: "x" } }, { status: statusOf[code] ?? 409 }),
      ),
    );
    const user = userEvent.setup();
    const { router } = open(film);
    await search(user, "big");
    await user.click(await screen.findByRole("radio", { name: /Big Buck Bunny/ }));
    await user.click(screen.getByRole("button", { name: "Use this title" }));
    expect(await screen.findByText(text)).toBeVisible();
    expect(router.state.location.pathname).toBe(`/jobs/${film}/match`);
  });

  it("works with the keyboard alone", async () => {
    const user = userEvent.setup();
    open(film);
    await user.click(await screen.findByRole("textbox", { name: "Title" }));
    await user.keyboard("big{Enter}");
    const first = await screen.findByRole("radio", { name: /Big Buck Bunny/ });
    await user.tab(); // the search button
    await user.tab();
    expect(first).toHaveFocus();
    await user.keyboard(" ");
    expect(first).toBeChecked();
    expect(await screen.findByRole("combobox", { name: "Quality profile" })).toBeVisible();
    await user.tab();
    await user.tab();
    await user.tab();
    await user.keyboard("{Enter}");
    expect(await screen.findByText("Big Buck Bunny (2008) in Radarr")).toBeVisible();
  });

  it("refuses a job that is not ready", async () => {
    open(id(4));
    expect(
      await screen.findByRole("heading", { name: "This upload cannot be matched now" }),
    ).toBeVisible();
  });

  it("is available in Spanish", async () => {
    localStorage.setItem("fukku.lang", "es");
    const user = userEvent.setup();
    open(film);
    expect(
      await screen.findByRole("heading", { name: "Elegir la película o la serie" }),
    ).toBeVisible();
    await user.type(screen.getByRole("textbox", { name: "Título" }), "zzz");
    await user.click(screen.getByRole("button", { name: "Buscar" }));
    expect(
      await screen.findByRole("heading", { name: "No se encontró nada para «zzz»" }),
    ).toBeVisible();
    localStorage.removeItem("fukku.lang");
  });
});

describe("the job page and the match step", () => {
  it("leads a probed job to the match page and keeps the manual fallback", async () => {
    const user = userEvent.setup();
    const { router } = renderApp(`/jobs/${film}`);
    await user.click(await screen.findByRole("link", { name: "Choose the film or show" }));
    await waitFor(() => expect(router.state.location.pathname).toBe(`/jobs/${film}/match`));
  });

  it("keeps the manual fallback on the probed job page", async () => {
    renderApp(`/jobs/${film}`);
    expect(await screen.findByRole("heading", { name: "Import it by hand" })).toBeVisible();
  });

  it("'Choose a different title' takes the choice back and returns here", async () => {
    const user = userEvent.setup();
    const { router } = open(film);
    await search(user, "big");
    await user.click(await screen.findByRole("radio", { name: /Big Buck Bunny/ }));
    await user.click(screen.getByRole("button", { name: "Use this title" }));
    await screen.findByText("Big Buck Bunny (2008) in Radarr");
    await user.click(screen.getByRole("button", { name: "Choose a different title" }));
    await waitFor(() => expect(router.state.location.pathname).toBe(`/jobs/${film}/match`));
    expect(await screen.findByRole("textbox", { name: "Title" })).toBeVisible();
  });
});
