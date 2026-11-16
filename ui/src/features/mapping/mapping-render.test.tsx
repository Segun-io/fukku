import { screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, describe, expect, it, vi } from "vitest";
import { mappingJobId } from "@/mocks/mapping-fixtures";
import { renderApp } from "@/test/render";

// Counts how often the editor builds its table, which is once per render.
const table = vi.hoisted(() => ({ renders: 0 }));
vi.mock("@tanstack/react-table", async (original) => {
  const mod = await original<typeof import("@tanstack/react-table")>();
  const useReactTable: typeof mod.useReactTable = (options) => {
    table.renders++;
    return mod.useReactTable(options);
  };
  return { ...mod, useReactTable };
});

afterEach(() => {
  table.renders = 0;
});

const settle = (ms: number) => new Promise((resolve) => setTimeout(resolve, ms));

describe("the mapping editor's table", () => {
  // A filter list made on every render made the table reset its page index in a microtask,
  // which set state, which rendered again, without end: in a browser a click on any checkbox
  // froze the page. Here the loop shows as renders that never stop.
  it("stops rendering once a click has been handled", async () => {
    const user = userEvent.setup();
    renderApp(`/jobs/${mappingJobId}/mapping`);
    await screen.findByRole("heading", { name: "Check the files" });

    for (const name of ["Only files that need attention", "Select all the files shown"]) {
      await user.click(screen.getByRole("checkbox", { name }));
      await settle(50);
      const after = table.renders;
      await settle(250);
      expect(table.renders - after).toBeLessThanOrEqual(2);
    }
  });
});
