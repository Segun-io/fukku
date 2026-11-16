import { act, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { HttpResponse, http } from "msw";
import { afterEach, describe, expect, it, vi } from "vitest";
import { session } from "@/mocks/fixtures";
import { server } from "@/mocks/server";
import { renderApp } from "@/test/render";

afterEach(() => vi.restoreAllMocks());

describe("the shell", () => {
  it("has a skip link to the main area, the wordmark and one navigation", async () => {
    renderApp("/jobs");
    const nav = await screen.findByRole("navigation", { name: "Main" });
    expect(within(nav).getByRole("link", { name: "Your uploads" })).toHaveAttribute(
      "aria-current",
      "page",
    );
    expect(within(nav).getByRole("link", { name: "Upload" })).not.toHaveAttribute("aria-current");
    expect(screen.getByRole("link", { name: "Skip to content" })).toHaveAttribute("href", "#main");
    expect(screen.getByRole("main")).toHaveAttribute("id", "main");
    expect(screen.getByRole("link", { name: "Fukku" })).toBeInTheDocument();
  });

  it("is walked by keyboard in reading order: skip link, wordmark, destinations, language", async () => {
    const user = userEvent.setup();
    renderApp("/jobs");
    const nav = await screen.findByRole("navigation", { name: "Main" });
    await user.tab();
    expect(screen.getByRole("link", { name: "Skip to content" })).toHaveFocus();
    await user.tab();
    expect(screen.getByRole("link", { name: "Fukku" })).toHaveFocus();
    for (const link of within(nav).getAllByRole("link")) {
      await user.tab();
      expect(link).toHaveFocus();
    }
    await user.tab();
    expect(screen.getByRole("combobox", { name: "Language" })).toHaveFocus();
  });

  it("shows no banner for a normal session", async () => {
    renderApp("/jobs");
    expect(await screen.findByText("Another Film (1999).mkv")).toBeInTheDocument();
    expect(screen.queryByRole("alert")).not.toBeInTheDocument();
    expect(screen.queryByText(/Authentication is off/)).not.toBeInTheDocument();
  });

  it("shows the insecure-development banner when the session says so", async () => {
    server.use(
      http.get("/api/session", () => HttpResponse.json({ ...session, insecureDev: true })),
    );
    renderApp("/upload");
    const banner = await screen.findByRole("alert");
    expect(banner).toHaveTextContent("Authentication is off.");
    expect(banner).toHaveTextContent("Never use it with real files.");
  });

  it("asks to sign in again when the session ended, in a new tab, and recovers", async () => {
    let signedIn = false;
    server.use(
      http.get("/api/session", () =>
        signedIn ? HttpResponse.json(session) : new HttpResponse(null, { status: 401 }),
      ),
    );
    renderApp("/upload");
    const notice = await screen.findByRole("alert");
    expect(notice).toHaveTextContent("Nothing is lost");
    const link = within(notice).getByRole("link", { name: "Sign in again" });
    expect(link).toHaveAttribute("target", "_blank");
    expect(link).toHaveAttribute("href", "/");

    // The user signs in in the other tab and comes back to this one.
    signedIn = true;
    act(() => {
      window.dispatchEvent(new Event("focus"));
    });
    await waitFor(() => expect(screen.queryByRole("alert")).not.toBeInTheDocument());
  });

  it("says so while the browser is offline", async () => {
    renderApp("/upload");
    expect(await screen.findByRole("heading", { name: "Upload" })).toBeInTheDocument();
    expect(screen.queryByText(/No connection/)).not.toBeInTheDocument();

    vi.spyOn(navigator, "onLine", "get").mockReturnValue(false);
    act(() => {
      window.dispatchEvent(new Event("offline"));
    });
    expect(await screen.findByRole("status")).toHaveTextContent("No connection.");

    vi.spyOn(navigator, "onLine", "get").mockReturnValue(true);
    act(() => {
      window.dispatchEvent(new Event("online"));
    });
    await waitFor(() => expect(screen.queryByText(/No connection/)).not.toBeInTheDocument());
  });

  it("an unknown address shows the not-found state inside the shell", async () => {
    renderApp("/no-such-page");
    expect(
      await screen.findByRole("heading", { level: 1, name: "This page does not exist" }),
    ).toBeInTheDocument();
    expect(screen.getByRole("link", { name: "Go to the start page" })).toHaveAttribute("href", "/");
    expect(screen.getByRole("navigation", { name: "Main" })).toBeInTheDocument();
  });
});
