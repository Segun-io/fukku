import { screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { HttpResponse, http } from "msw";
import { describe, expect, it } from "vitest";
import { flow2PalJobId } from "@/mocks/flow2-fixtures";
import { applyScenarios } from "@/mocks/handlers";
import { server } from "@/mocks/server";
import { renderApp } from "@/test/render";

describe("the label field of the film's second version", () => {
  it("is described by the label error, marked invalid, and takes focus when the server refuses the label", async () => {
    const user = userEvent.setup();
    applyScenarios(["flow2"]);
    renderApp(`/jobs/${flow2PalJobId}/flow2`);
    const field = await screen.findByRole("textbox", { name: "Version label" });
    server.use(
      http.post("/api/jobs/:id/confirm", () =>
        HttpResponse.json({ error: { code: "version_label", message: "x" } }, { status: 409 }),
      ),
    );
    await user.click(screen.getByRole("button", { name: "Keep as a second version" }));
    const error = await screen.findByText("That label cannot be used. Change it and try again.");
    expect(field).toHaveAttribute("aria-invalid", "true");
    expect(field.getAttribute("aria-describedby")).toContain(error.id);
    expect(error.id).not.toBe("");
    expect(field).toHaveFocus();
  });
});
