import { render, screen, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { type ReactNode, useState } from "react";
import { describe, expect, it, vi } from "vitest";
import { createQueryClient, Providers } from "@/providers";
import { ChoiceList } from "./choice-list";
import { ConfirmStep } from "./confirm-step";
import { NudgeField } from "./nudge-field";
import { SideBySide } from "./side-by-side";

// The patterns the flow 2 screens added (#60): the choice with consequences, two things side
// by side, the field with step buttons, and the heading level of the confirmation.

function show(ui: ReactNode) {
  return render(<Providers client={createQueryClient()}>{ui}</Providers>);
}

describe("ChoiceList", () => {
  it("is a group of native radios named by the question, and reports the choice", async () => {
    const user = userEvent.setup();
    const onChange = vi.fn();
    show(
      <ChoiceList
        legend="How do you want to add it?"
        value="merge"
        onChange={onChange}
        choices={[
          { value: "merge", label: "Merge", body: "One file stays." },
          { value: "version", label: "Keep both", input: { "data-policy": "version" } },
        ]}
      >
        <p>Under the choices</p>
      </ChoiceList>,
    );
    const group = screen.getByRole("group", { name: "How do you want to add it?" });
    expect(within(group).getByRole("radio", { name: /Merge/ })).toBeChecked();
    expect(within(group).getByText("Under the choices")).toBeVisible();
    const other = within(group).getByRole("radio", { name: "Keep both" });
    expect(other).toHaveAttribute("data-policy", "version");
    await user.click(other);
    expect(onChange).toHaveBeenCalledWith("version");
  });

  it("has nothing chosen while the value is empty", () => {
    show(
      <ChoiceList
        legend="What should happen?"
        value=""
        onChange={() => {}}
        choices={[
          { value: "a", label: "One" },
          { value: "b", label: "Two" },
        ]}
      />,
    );
    for (const radio of screen.getAllByRole("radio")) expect(radio).not.toBeChecked();
  });
});

describe("SideBySide", () => {
  it("shows each side as a region with every term and its own value", () => {
    show(
      <SideBySide
        titles={["In the library", "Your upload"]}
        rows={[
          { term: "File", values: ["Library.mkv", "Upload.mkv"], verbatim: true },
          { term: "Length", values: ["14m 48s", "14m 12s"], figure: true },
        ]}
      />,
    );
    const library = screen.getByRole("region", { name: "In the library" });
    const upload = screen.getByRole("region", { name: "Your upload" });
    expect(within(library).getByText("Library.mkv")).toHaveAttribute("translate", "no");
    expect(within(library).getByText("14m 48s")).toHaveClass("figure");
    expect(within(upload).getByText("Upload.mkv")).toBeVisible();
    // Each side carries its own terms, so it reads whole without the other.
    expect(
      within(upload)
        .getAllByRole("term")
        .map((el) => el.textContent),
    ).toEqual(["File", "Length"]);
    expect(screen.getAllByRole("heading", { level: 3 })).toHaveLength(2);
  });
});

const parse = (text: string) => (/^-?\d+$/.test(text.trim()) ? Number(text) : null);

function Field({ start = "0", error }: { start?: string; error?: string }) {
  const [value, setValue] = useState(start);
  return (
    <NudgeField
      id="offset"
      label="Offset in milliseconds"
      value={value}
      onChange={setValue}
      parse={parse}
      min={-1000}
      max={1000}
      unit="ms"
      groups={[
        { caption: "The sound comes late", steps: [{ by: -50, label: "50 ms earlier" }] },
        { caption: "The sound comes early", steps: [{ by: 50, label: "50 ms later" }] },
      ]}
      readout={`Now ${value}`}
      hint="From -1000 to 1000."
      error={error}
    />
  );
}
describe("NudgeField", () => {
  it("writes the stepped number into the field and stops at the bounds", async () => {
    const user = userEvent.setup();
    show(<Field start="980" />);
    const field = screen.getByRole("textbox", { name: "Offset in milliseconds" });
    await user.click(screen.getByRole("button", { name: "50 ms later" }));
    expect(field).toHaveValue("1000");
    await user.click(screen.getByRole("button", { name: "50 ms later" }));
    expect(field).toHaveValue("1000");
    await user.click(screen.getByRole("button", { name: "50 ms earlier" }));
    expect(field).toHaveValue("950");
    expect(screen.getByText("Now 950")).toHaveAttribute("aria-live", "polite");
  });

  it("starts from 0 when the text is not a number, and can still be typed into", async () => {
    const user = userEvent.setup();
    show(<Field start="abc" />);
    const field = screen.getByRole("textbox", { name: "Offset in milliseconds" });
    await user.click(screen.getByRole("button", { name: "50 ms earlier" }));
    expect(field).toHaveValue("-50");
    await user.clear(field);
    await user.type(field, "120");
    expect(field).toHaveValue("120");
  });

  it("groups the buttons under what the user notices, and describes the field", () => {
    show(<Field error="Enter a whole number." />);
    const field = screen.getByRole("textbox", { name: "Offset in milliseconds" });
    expect(field).toBeInvalid();
    expect(field).toHaveAccessibleDescription("Now 0 From -1000 to 1000.");
    expect(screen.getByRole("alert")).toHaveTextContent("Enter a whole number.");
    const late = screen.getByRole("group", { name: "The sound comes late" });
    expect(within(late).getByRole("button", { name: "50 ms earlier" })).toHaveAttribute(
      "type",
      "button",
    );
  });
});

describe("ConfirmStep, under another heading", () => {
  it("takes the level it is given and puts its warnings one level below", () => {
    show(
      <ConfirmStep
        as="h4"
        title="Merge the audio?"
        facts={[{ term: "Offset", value: "250 ms" }]}
        warnings={["The old file is removed."]}
        confirmLabel="Merge"
        onConfirm={() => {}}
      />,
    );
    expect(screen.getByRole("heading", { level: 4, name: "Merge the audio?" })).toBeVisible();
    expect(screen.getByRole("heading", { level: 5, name: "Before you confirm" })).toBeVisible();
    expect(screen.getByRole("form", { name: "Merge the audio?" })).toBeVisible();
  });

  it("is a second-level heading by default", () => {
    show(
      <ConfirmStep
        title="Delete this upload?"
        facts={[]}
        warnings={["It cannot be brought back."]}
        confirmLabel="Delete"
        onConfirm={() => {}}
      />,
    );
    expect(screen.getByRole("heading", { level: 2, name: "Delete this upload?" })).toBeVisible();
    expect(screen.getByRole("heading", { level: 3, name: "Before you confirm" })).toBeVisible();
  });
});
