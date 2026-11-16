import { screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { HttpResponse, http } from "msw";
import { afterEach, describe, expect, it, vi } from "vitest";
import { server } from "@/mocks/server";
import { renderApp } from "@/test/render";

afterEach(() => vi.unstubAllGlobals());

describe("routes", () => {
  it("/ redirects to /jobs and lists the jobs from the mock API", async () => {
    const { router } = renderApp("/");
    expect(await screen.findByText("Another Film (1999).mkv")).toBeInTheDocument();
    expect(screen.getByRole("heading", { name: "Jobs" })).toBeInTheDocument();
    expect(router.state.location.pathname).toBe("/jobs");
    expect(screen.getByText("4 jobs")).toBeInTheDocument();
    expect(screen.getByText("Failed")).toBeInTheDocument();
  });

  it("the header links reach /upload and /jobs", async () => {
    const user = userEvent.setup();
    const { router } = renderApp("/jobs");
    const nav = await screen.findByRole("navigation");
    await user.click(within(nav).getByRole("link", { name: "Upload" }));
    expect(await screen.findByRole("heading", { name: "Upload" })).toBeInTheDocument();
    expect(router.state.location.pathname).toBe("/upload");
    await user.click(within(nav).getByRole("link", { name: "Your uploads" }));
    expect(await screen.findByRole("heading", { name: "Jobs" })).toBeInTheDocument();
  });

  it("/jobs/$id shows one job and its files", async () => {
    renderApp(`/jobs/${"2".padStart(32, "0")}`);
    expect(
      await screen.findByRole("heading", { name: "Another Film (1999).mkv" }),
    ).toBeInTheDocument();
    expect(screen.getByText("Another Film (1999).es.srt (1.7 kB)")).toBeInTheDocument();
  });

  it("an unknown job shows the translated error for its code", async () => {
    renderApp(`/jobs/${"f".repeat(32)}`);
    expect(await screen.findByRole("alert")).toHaveTextContent("Not found.");
  });
});

describe("error states", () => {
  it("a signed-out answer shows the sign-in message", async () => {
    server.use(http.get("/api/jobs", () => new HttpResponse(null, { status: 401 })));
    renderApp("/jobs");
    expect(await screen.findByRole("alert")).toHaveTextContent("Your session ended");
  });

  it("insecureDev shows a banner", async () => {
    const { session } = await import("@/mocks/fixtures");
    server.use(
      http.get("/api/session", () => HttpResponse.json({ ...session, insecureDev: true })),
    );
    renderApp("/jobs");
    expect(await screen.findByText(/Authentication is off/)).toBeInTheDocument();
  });
});

describe("language", () => {
  it("the switch changes the visible strings and survives a reload", async () => {
    const user = userEvent.setup();
    const first = renderApp("/jobs");
    expect(await screen.findByText("Another Film (1999).mkv")).toBeInTheDocument();
    expect(screen.getByRole("heading", { name: "Jobs" })).toBeInTheDocument();

    await user.selectOptions(screen.getByRole("combobox", { name: "Language" }), "es");
    expect(await screen.findByRole("heading", { name: "Trabajos" })).toBeInTheDocument();
    expect(screen.getByText("Fallido")).toBeInTheDocument();
    expect(screen.getByText("4 trabajos")).toBeInTheDocument();
    expect(document.documentElement.lang).toBe("es");
    expect(localStorage.getItem("fukku.lang")).toBe("es");

    // A reload: a new page with the same localStorage.
    first.unmount();
    renderApp("/jobs");
    expect(await screen.findByRole("heading", { name: "Trabajos" })).toBeInTheDocument();
    expect(screen.getByRole("combobox", { name: "Idioma" })).toHaveValue("es");
  });

  it("without a saved choice it follows the browser's language", async () => {
    vi.stubGlobal("navigator", { ...navigator, languages: ["es-419", "en"], language: "es-419" });
    renderApp("/jobs");
    expect(await screen.findByRole("heading", { name: "Trabajos" })).toBeInTheDocument();
  });

  it("only offers the languages the server lists", async () => {
    const { session } = await import("@/mocks/fixtures");
    server.use(
      http.get("/api/session", () => HttpResponse.json({ ...session, uiLanguages: ["es"] })),
    );
    renderApp("/jobs");
    await waitFor(() =>
      expect(screen.getByRole("heading", { name: "Trabajos" })).toBeInTheDocument(),
    );
    expect(screen.queryByRole("option", { name: "English" })).not.toBeInTheDocument();
  });
});
