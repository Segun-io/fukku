import { screen, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { HttpResponse, http } from "msw";
import { describe, expect, it, vi } from "vitest";
import { adminStatus } from "@/mocks/admin-fixtures";
import { setMockAdmin } from "@/mocks/handlers";
import { server } from "@/mocks/server";
import { renderApp } from "@/test/render";

const adminLink = () => screen.queryByRole("link", { name: "Admin" });

describe("admin page", () => {
  it("is linked for an admin only", async () => {
    const { client } = renderApp("/jobs");
    await screen.findByRole("navigation", { name: "Main" });
    await vi.waitFor(() => expect(client.getQueryState(["session"])?.status).toBe("success"));
    expect(screen.getByRole("link", { name: "Your uploads" })).toBeInTheDocument();
    expect(adminLink()).not.toBeInTheDocument();
  });

  it("shows the link to an admin", async () => {
    setMockAdmin(true);
    renderApp("/jobs");
    expect(await screen.findByRole("link", { name: "Admin" })).toHaveAttribute("href", "/admin");
  });

  it("does not exist for a user: not-found page, and no request for the data", async () => {
    const asked = vi.fn();
    server.events.on("request:start", ({ request }) => {
      if (new URL(request.url).pathname.startsWith("/api/admin/")) asked();
    });
    try {
      renderApp("/admin");
      expect(
        await screen.findByRole("heading", { name: "This page does not exist" }),
      ).toBeInTheDocument();
      expect(screen.queryByRole("heading", { name: "Health" })).not.toBeInTheDocument();
      expect(asked).not.toHaveBeenCalled();
    } finally {
      server.events.removeAllListeners();
    }
  });

  it("shows health: status, checks, app versions, worker, uploads by state and disk space", async () => {
    setMockAdmin(true);
    renderApp("/admin");
    expect(await screen.findByText("Working, but an app does not answer")).toBeInTheDocument();

    const checks = within(
      screen.getByRole("heading", { name: "Checks" }).parentElement as HTMLElement,
    );
    expect(checks.getAllByRole("listitem")).toHaveLength(adminStatus.readiness.checks.length);
    expect(checks.getByText("sonarr is unreachable")).toBeInTheDocument();
    expect(checks.getAllByText("Failing")).toHaveLength(1);

    const apps = within(screen.getByRole("heading", { name: "Apps" }).parentElement as HTMLElement);
    expect(apps.getByText("Version 5.2.1.8000")).toBeInTheDocument();
    expect(apps.getByText("No version: the app did not answer")).toBeInTheDocument();

    expect(
      screen.getByText(/Last heartbeat 7s ago\. It is a problem after 2m\./),
    ).toBeInTheDocument();
    expect(screen.getByText("6 uploads in total, from everyone")).toBeInTheDocument();
    const failed = screen.getByText("Failed", { selector: "dt" });
    expect(failed.nextElementSibling).toHaveTextContent("1");
    expect(screen.getByText(/412\s?GB free in the intake folder/)).toBeInTheDocument();
    expect(screen.getByText(/above the 107\s?GB kept free/)).toBeInTheDocument();
  });

  it("says in words when the server is not ready or the worker has no heartbeat", async () => {
    setMockAdmin(true);
    server.use(
      http.get("/api/admin/status", () =>
        HttpResponse.json({
          ...adminStatus,
          readiness: { status: "not_ready", checks: [{ name: "database", ok: false }] },
          worker: { heartbeatAgeSeconds: null, maxAgeSeconds: 120 },
          disk: { freeBytes: 1_000_000_000, marginBytes: 107_374_182_400, aboveMargin: false },
          jobs: null,
        }),
      ),
    );
    renderApp("/admin");
    expect(
      await screen.findByText("Not ready: something on this server is failing"),
    ).toBeInTheDocument();
    expect(screen.getByText(/No heartbeat\./)).toBeInTheDocument();
    expect(screen.getByText(/new uploads are refused/)).toBeInTheDocument();
    expect(screen.getByText("The uploads could not be counted.")).toBeInTheDocument();
  });

  it("checks again when asked", async () => {
    setMockAdmin(true);
    let calls = 0;
    server.use(
      http.get("/api/admin/status", () => {
        calls++;
        return HttpResponse.json(adminStatus);
      }),
    );
    const user = userEvent.setup();
    renderApp("/admin");
    await screen.findByText("Working, but an app does not answer");
    expect(calls).toBe(1);
    await user.click(screen.getByRole("button", { name: "Check again" }));
    await vi.waitFor(() => expect(calls).toBe(2));
  });

  it("lists every setting as default or set, a secret never with its value", async () => {
    setMockAdmin(true);
    const user = userEvent.setup();
    renderApp("/admin");
    const heading = await screen.findByRole("heading", { name: "server" });
    const server_ = within(heading.parentElement as HTMLElement);
    const listen = server_.getByText("server.listenAddress").closest("li") as HTMLElement;
    expect(within(listen).getByText(":8080")).toBeInTheDocument();
    expect(within(listen).getByText("Default")).toBeInTheDocument();
    expect(
      within(listen).getByText("Environment: FUKKU_SERVER_LISTEN_ADDRESS"),
    ).toBeInTheDocument();
    const base = server_.getByText("server.publicBaseUrl").closest("li") as HTMLElement;
    expect(within(base).getByText("https://fukku.example.com")).toBeInTheDocument();
    expect(within(base).getByText("Set")).toBeInTheDocument();

    const radarr = within(
      (await screen.findByRole("heading", { name: "radarr" })).parentElement as HTMLElement,
    );
    const key = radarr.getByText("radarr.apiKey").closest("li") as HTMLElement;
    expect(within(key).getByText("Set, hidden")).toBeInTheDocument();
    expect(within(key).queryByText("[redacted]")).not.toBeInTheDocument();
    const sonarrKey = screen.getByText("sonarr.apiKey").closest("li") as HTMLElement;
    expect(within(sonarrKey).getByText("Not set")).toBeInTheDocument();

    await user.click(
      screen.getByRole("checkbox", { name: "Only settings that are not the default" }),
    );
    expect(screen.queryByText("server.listenAddress")).not.toBeInTheDocument();
    expect(screen.getByText("server.publicBaseUrl")).toBeInTheDocument();
  });

  it("offers nothing that changes a setting", async () => {
    setMockAdmin(true);
    renderApp("/admin");
    await screen.findByRole("heading", { name: "server" });
    expect(screen.queryByRole("textbox")).not.toBeInTheDocument();
    expect(screen.queryByRole("button", { name: /save|apply|edit/i })).not.toBeInTheDocument();
  });

  it("a failed request says so and can be retried", async () => {
    setMockAdmin(true);
    let fail = true;
    server.use(
      http.get("/api/admin/settings", () =>
        fail
          ? HttpResponse.json({ error: { code: "internal", message: "no" } }, { status: 500 })
          : HttpResponse.json({ settings: [] }),
      ),
    );
    const user = userEvent.setup();
    renderApp("/admin");
    const title = await screen.findByRole("heading", { name: "The settings could not load" });
    expect(title.closest("[role=alert]")).toHaveTextContent("The server had a problem.");
    fail = false;
    await user.click(
      within(title.closest("[role=alert]") as HTMLElement).getByRole("button", {
        name: "Try again",
      }),
    );
    expect(await screen.findByText("Every setting has its default value.")).toBeInTheDocument();
  });

  it("is in Spanish too", async () => {
    setMockAdmin(true);
    window.localStorage.setItem("fukku.lang", "es");
    renderApp("/admin");
    expect(await screen.findByRole("heading", { name: "Estado de salud" })).toBeInTheDocument();
    expect(
      await screen.findByText("Funciona, pero una aplicación no responde"),
    ).toBeInTheDocument();
    expect(screen.getAllByText("Predeterminado").length).toBeGreaterThan(0);
  });
});
