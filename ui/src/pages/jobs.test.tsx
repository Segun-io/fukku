import { screen, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { HttpResponse, http } from "msw";
import { describe, expect, it } from "vitest";
import { setMockAdmin } from "@/mocks/handlers";
import { server } from "@/mocks/server";
import { renderApp } from "@/test/render";

const list = () => screen.findByRole("list", { name: "Uploads" });

describe("job list", () => {
  it("shows name, state, size, age and the day retention deletes it", async () => {
    renderApp("/jobs");
    const rows = within(await list()).getAllByRole("listitem");
    expect(rows).toHaveLength(5);
    const row = rows.find((r) => within(r).queryByText("Another Film (1999).mkv"));
    expect(row).toBeDefined();
    if (!row) return;
    expect(within(row).getByText("Checked")).toBeInTheDocument();
    expect(within(row).getByText(/2 files, 4\.29\s?GB/)).toBeInTheDocument();
    expect(within(row).getByText(/Started .* ago/)).toBeInTheDocument();
    expect(within(row).getByText(/Deleted on Jan 15, 2026/)).toBeInTheDocument();
    expect(within(row).getByRole("link", { name: "Another Film (1999).mkv" })).toHaveAttribute(
      "href",
      expect.stringMatching(/^\/jobs\/0+2$/),
    );
  });

  it("a user does not see the owner or the scope filter, and never other people's jobs", async () => {
    renderApp("/jobs");
    await list();
    expect(screen.queryByText("Other Show S02.zip")).not.toBeInTheDocument();
    expect(screen.queryByText(/^By /)).not.toBeInTheDocument();
    expect(screen.queryByLabelText("Show")).not.toBeInTheDocument();
  });

  it("filters by state", async () => {
    const user = userEvent.setup();
    renderApp("/jobs");
    await list();
    await user.selectOptions(screen.getByLabelText("State"), "failed");
    expect(await screen.findByText("1 upload")).toBeInTheDocument();
    expect(screen.getByText("Example Show S01.zip")).toBeInTheDocument();
    expect(screen.queryByText("Another Film (1999).mkv")).not.toBeInTheDocument();
  });

  it("a filter that matches nothing says so without sending the user to upload", async () => {
    const user = userEvent.setup();
    renderApp("/jobs");
    await list();
    await user.selectOptions(screen.getByLabelText("State"), "importing");
    expect(await screen.findByRole("heading", { name: "No uploads match" })).toBeInTheDocument();
    expect(screen.queryByRole("link", { name: "Upload a file" })).not.toBeInTheDocument();
  });

  it("an admin sees everyone's jobs with their owners and can limit the list to their own", async () => {
    setMockAdmin(true);
    const user = userEvent.setup();
    renderApp("/jobs");
    expect(await screen.findByText("Other Show S02.zip")).toBeInTheDocument();
    expect(screen.getByText("By user2")).toBeInTheDocument();
    expect(screen.getAllByText("By user1").length).toBeGreaterThan(0);
    expect(screen.getByText("6 uploads")).toBeInTheDocument();

    await user.selectOptions(screen.getByLabelText("Show"), "mine");
    expect(await screen.findByText("5 uploads")).toBeInTheDocument();
    expect(screen.queryByText("Other Show S02.zip")).not.toBeInTheDocument();
  });

  it("the empty state leads to /upload", async () => {
    server.use(http.get("/api/jobs", () => HttpResponse.json({ jobs: [] })));
    const user = userEvent.setup();
    const { router } = renderApp("/jobs");
    expect(
      await screen.findByRole("heading", { name: "Nothing uploaded yet" }),
    ).toBeInTheDocument();
    await user.click(screen.getByRole("link", { name: "Upload a file" }));
    expect(router.state.location.pathname).toBe("/upload");
  });

  it("a failed request says so and can be retried", async () => {
    let fail = true;
    server.use(
      http.get("/api/jobs", () =>
        fail
          ? HttpResponse.json({ error: { code: "forbidden", message: "no" } }, { status: 403 })
          : HttpResponse.json({ jobs: [] }),
      ),
    );
    const user = userEvent.setup();
    renderApp("/jobs");
    expect(await screen.findByRole("alert")).toHaveTextContent("You are not allowed to do that.");
    fail = false;
    await user.click(screen.getByRole("button", { name: "Try again" }));
    expect(
      await screen.findByRole("heading", { name: "Nothing uploaded yet" }),
    ).toBeInTheDocument();
  });
});
