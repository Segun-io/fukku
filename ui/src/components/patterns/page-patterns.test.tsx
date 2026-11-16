import { fireEvent, render, screen, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import type { ReactNode } from "react";
import { describe, expect, it, vi } from "vitest";
import { createQueryClient, Providers } from "@/providers";
import { CopyField } from "./copy-field";
import { FilePicker } from "./file-picker";
import { FileProgress, FileProgressList } from "./file-progress";
import { SelectField } from "./select-field";

// The patterns the upload and job pages added (#32): the short rows of the file label, the
// file picker, the copy field and the select field.

const GB = 1e9;

function show(ui: ReactNode) {
  return render(<Providers client={createQueryClient()}>{ui}</Providers>);
}

const plain = (text: string | null) => (text ?? "").replace(/\s/g, " ");

describe("FileProgress, the short rows", () => {
  it("a finished file is its name, the word and its size: no bar, no figures", () => {
    show(
      <FileProgressList>
        <FileProgress name="done.mkv" size={4.7 * GB} sent={4.7 * GB} status="done" />
      </FileProgressList>,
    );
    const row = screen.getByRole("listitem");
    expect(within(row).getByText("Uploaded")).toBeInTheDocument();
    expect(plain(row.textContent)).toContain("4.7 GB");
    expect(plain(row.textContent)).not.toContain(" of ");
    expect(within(row).queryByRole("progressbar")).not.toBeInTheDocument();
  });

  it("a file that waits with nothing sent keeps its bar and says its size once", () => {
    show(
      <FileProgressList>
        <FileProgress name="next.mkv" size={4.7 * GB} sent={0} status="queued" />
      </FileProgressList>,
    );
    const row = screen.getByRole("listitem");
    expect(within(row).getByRole("progressbar", { name: "next.mkv" })).toHaveAttribute(
      "aria-valuenow",
      "0",
    );
    expect(plain(row.textContent)).toContain("Waiting its turn4.7 GB");
    expect(plain(row.textContent)).not.toContain(" of ");
  });

  it("a file that waits with bytes on the server shows where it stands", () => {
    show(
      <FileProgressList>
        <FileProgress name="half.mkv" size={80 * GB} sent={31.4 * GB} status="queued" />
      </FileProgressList>,
    );
    expect(plain(screen.getByRole("listitem").textContent)).toContain("31.4 GB of 80.0 GB");
  });

  it("an unfinished upload on a job page says what the server has and offers nothing", () => {
    show(
      <FileProgressList>
        <FileProgress
          name="left.mkv"
          size={80 * GB}
          sent={31.4 * GB}
          status="unfinished"
          onRemove={() => {}}
        />
      </FileProgressList>,
    );
    const row = screen.getByRole("listitem");
    expect(within(row).getByText("Not finished")).toBeInTheDocument();
    expect(plain(row.textContent)).toContain("31.4 GB is on the server. It continues from there.");
    expect(within(row).queryByRole("button")).not.toBeInTheDocument();
  });
});

describe("FilePicker", () => {
  const texts = {
    title: "Choose your files",
    hint: "Pick several files, or one zip, or drop them here.",
    chooseLabel: "Choose files",
    accepted: "Accepted: .mkv .zip",
    accept: ".mkv,.zip",
  };

  it("hands over the chosen files and can be used again for the same file", async () => {
    const user = userEvent.setup();
    const onFiles = vi.fn();
    show(<FilePicker {...texts} onFiles={onFiles} />);
    expect(screen.getByRole("heading", { name: "Choose your files" })).toBeVisible();
    const input = screen.getByLabelText("Choose files") as HTMLInputElement;
    expect(input).toHaveAttribute("accept", ".mkv,.zip");
    const file = new File(["x"], "a.mkv");
    await user.upload(input, file);
    expect(onFiles).toHaveBeenCalledWith([file]);
    expect(input.value).toBe("");
  });

  it("takes files dropped on it, unless it is disabled", () => {
    const onFiles = vi.fn();
    const { rerender } = show(<FilePicker {...texts} onFiles={onFiles} />);
    const zone = screen.getByRole("region", { name: "Choose your files" });
    const file = new File(["x"], "a.mkv");
    fireEvent.drop(zone, { dataTransfer: { files: [file] } });
    expect(onFiles).toHaveBeenCalledTimes(1);

    rerender(
      <Providers client={createQueryClient()}>
        <FilePicker {...texts} onFiles={onFiles} disabled />
      </Providers>,
    );
    fireEvent.drop(screen.getByRole("region", { name: "Choose your files" }), {
      dataTransfer: { files: [file] },
    });
    expect(onFiles).toHaveBeenCalledTimes(1);
  });

  it("the compact size keeps the button's name and says the hint once", () => {
    show(<FilePicker {...texts} size="compact" primary={false} onFiles={() => {}} />);
    expect(screen.getByLabelText("Choose files")).toBeInTheDocument();
    expect(screen.getByRole("heading", { name: "Choose your files" })).toBeInTheDocument();
    expect(screen.getAllByText(/Pick several files/)).toHaveLength(1);
    expect(screen.getByText(/Accepted: \.mkv \.zip/)).toBeInTheDocument();
  });
});

describe("CopyField", () => {
  const texts = {
    label: "Folder in Radarr",
    value: "/data/intake/jobs/abc/files",
    copyLabel: "Copy",
    copyName: "Copy the Radarr folder",
    copiedText: "Copied",
    failedText: "Could not copy. Select the folder text and copy it.",
  };

  it("copies the exact value and says so", async () => {
    const user = userEvent.setup();
    const writeText = vi.spyOn(navigator.clipboard, "writeText").mockResolvedValue();
    show(<CopyField {...texts} />);
    expect(screen.getByText("/data/intake/jobs/abc/files")).toBeInTheDocument();
    await user.click(screen.getByRole("button", { name: "Copy the Radarr folder" }));
    expect(writeText).toHaveBeenCalledWith("/data/intake/jobs/abc/files");
    expect(await screen.findByRole("status")).toHaveTextContent("Copied");
    writeText.mockRestore();
  });

  it("says what to do when the browser refuses", async () => {
    const user = userEvent.setup();
    const writeText = vi
      .spyOn(navigator.clipboard, "writeText")
      .mockRejectedValue(new Error("denied"));
    show(<CopyField {...texts} />);
    await user.click(screen.getByRole("button", { name: "Copy the Radarr folder" }));
    expect(await screen.findByText(/Select the folder text/)).toBeInTheDocument();
    writeText.mockRestore();
  });
});

describe("SelectField", () => {
  it("is a native select named by its caption", async () => {
    const user = userEvent.setup();
    const onChange = vi.fn();
    show(
      <SelectField label="State" defaultValue="" onChange={onChange}>
        <option value="">All states</option>
        <option value="failed">Failed</option>
      </SelectField>,
    );
    await user.selectOptions(screen.getByLabelText("State"), "failed");
    expect(onChange).toHaveBeenCalledOnce();
    expect(screen.getByLabelText("State")).toHaveValue("failed");
  });
});
