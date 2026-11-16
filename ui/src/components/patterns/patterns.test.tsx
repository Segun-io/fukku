import { render, screen, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import type { ReactNode } from "react";
import { describe, expect, it, vi } from "vitest";
import { createQueryClient, Providers } from "@/providers";
import { ConfirmStep } from "./confirm-step";
import { FileList } from "./file-list";
import { FileProgress, FileProgressList } from "./file-progress";
import { type JobEvent, jobPath, StatusTimeline, standingAt } from "./status-timeline";

const GB = 1e9;
const MB = 1e6;

function show(ui: ReactNode) {
  return render(<Providers client={createQueryClient()}>{ui}</Providers>);
}

/** Intl joins a number and its unit with different spaces; the tests do not care which. */
const plain = (text: string | null) => (text ?? "").replace(/\s/g, " ");

describe("FileProgress", () => {
  it("shows the figures of an upload in fixed-width form and names the progress bar", () => {
    show(
      <FileProgressList>
        <FileProgress
          name="Some Film (2001).mkv"
          size={80 * GB}
          sent={31.4 * GB}
          status="uploading"
          bytesPerSecond={11.8 * MB}
          secondsLeft={4119}
          onPause={() => {}}
        />
      </FileProgressList>,
    );
    const row = screen.getByRole("listitem");
    expect(plain(row.textContent)).toContain("31.4 GB of 80.0 GB");
    expect(plain(row.textContent)).toContain("11.8 MB/s");
    // 4119 s is rounded to five minutes above an hour, so the time left does not flicker.
    expect(plain(row.textContent)).toContain("about 1h 10m left");
    const bar = within(row).getByRole("progressbar", { name: "Some Film (2001).mkv" });
    expect(bar).toHaveAttribute("aria-valuenow", "39");
    expect(bar).toHaveAttribute("aria-valuetext", "39% uploaded");
  });

  it("offers only the action its state allows, named after the file", async () => {
    const user = userEvent.setup();
    const onResume = vi.fn();
    const onPause = vi.fn();
    show(
      <FileProgressList>
        <FileProgress
          name="a.mkv"
          size={80 * GB}
          sent={31.4 * GB}
          status="paused"
          onPause={onPause}
          onResume={onResume}
        />
      </FileProgressList>,
    );
    expect(screen.queryByRole("button", { name: "Pause a.mkv" })).not.toBeInTheDocument();
    await user.click(screen.getByRole("button", { name: "Resume a.mkv" }));
    expect(onResume).toHaveBeenCalledOnce();
    expect(plain(screen.getByRole("listitem").textContent)).toContain(
      "Stopped at 31.4 GB. It continues from there.",
    );
  });

  it("says why an upload failed and has nothing to act on when it is done", () => {
    show(
      <FileProgressList>
        <FileProgress
          name="big.mkv"
          size={84 * GB}
          sent={0}
          status="failed"
          note="It is too large."
          onRetry={() => {}}
        />
        <FileProgress name="done.mkv" size={GB} sent={GB} status="done" onRemove={() => {}} />
      </FileProgressList>,
    );
    const [failed, done] = screen.getAllByRole("listitem");
    expect(failed).toHaveTextContent("Upload stopped");
    expect(failed).toHaveTextContent("It is too large.");
    expect(within(failed as HTMLElement).getByRole("button", { name: "Try big.mkv again" }));
    expect(within(done as HTMLElement).queryByRole("button")).not.toBeInTheDocument();
  });
});

describe("StatusTimeline", () => {
  const history: JobEvent[] = [
    { at: "2026-01-01T12:00:00Z", from: null, to: "uploading" },
    { at: "2026-01-01T13:10:00Z", from: "uploading", to: "uploaded" },
    { at: "2026-01-01T13:11:00Z", from: "uploaded", to: "probing" },
  ];

  it("finds the step a failed, stalled or cancelled job was in", () => {
    expect(standingAt("probing", history)).toBe("probing");
    const failed: JobEvent[] = [
      ...history,
      { at: "2026-01-01T13:21:00Z", from: "probing", to: "failed" },
    ];
    expect(standingAt("failed", failed)).toBe("probing");
    expect(standingAt("stalled", [])).toBe("uploading");
  });

  it("marks done, current and upcoming steps in words", () => {
    show(<StatusTimeline state="probing" history={history} steps={jobPath.slice(0, 4)} />);
    const steps = screen.getAllByRole("listitem");
    expect(steps.map((li) => li.textContent?.split(":")[0])).toEqual([
      "Done",
      "Done",
      "Now",
      "Not yet",
    ]);
    expect(steps[2]).toHaveAttribute("aria-current", "step");
    expect(steps[2]).toHaveTextContent("Checking the files");
  });

  it("shows a failure at the step where it happened, with its detail and actions", () => {
    show(
      <StatusTimeline
        state="failed"
        history={[...history, { at: "2026-01-01T13:21:00Z", from: "probing", to: "failed" }]}
        detail="The worker did not start."
        actions={<button type="button">Try again</button>}
      />,
    );
    const alert = screen.getByRole("alert");
    expect(alert).toHaveTextContent("Failed");
    expect(alert).toHaveTextContent("It needs you");
    expect(alert).toHaveTextContent("The worker did not start.");
    expect(within(alert).getByRole("button", { name: "Try again" })).toBeInTheDocument();
    expect(alert.closest("li")).toHaveTextContent("Checking the files");
  });

  it("ends a cancelled job at its step", () => {
    show(
      <StatusTimeline
        state="cancelled"
        history={[...history, { at: "2026-01-01T15:00:00Z", from: "probing", to: "cancelled" }]}
      />,
    );
    expect(screen.getAllByRole("listitem")).toHaveLength(3);
    expect(screen.getByRole("status")).toHaveTextContent("Cancelled here.");
  });
});

describe("ConfirmStep", () => {
  const facts = [{ term: "Film", value: "Some Film (2001)" }];

  it("confirms at once when there is nothing to warn about", async () => {
    const user = userEvent.setup();
    const onConfirm = vi.fn();
    show(<ConfirmStep title="Add?" facts={facts} confirmLabel="Add" onConfirm={onConfirm} />);
    expect(screen.queryByRole("checkbox")).not.toBeInTheDocument();
    await user.click(screen.getByRole("button", { name: "Add" }));
    expect(onConfirm).toHaveBeenCalledOnce();
  });

  it("with warnings, asks for the tick: says what is missing and focuses the box", async () => {
    const user = userEvent.setup();
    const onConfirm = vi.fn();
    show(
      <ConfirmStep
        title="Add?"
        facts={facts}
        warnings={["The old file is deleted."]}
        confirmLabel="Add"
        onConfirm={onConfirm}
      />,
    );
    const add = screen.getByRole("button", { name: "Add" });
    expect(add).toBeEnabled();
    await user.click(add);
    expect(onConfirm).not.toHaveBeenCalled();
    const box = screen.getByRole("checkbox", { name: "Confirm that you have read this" });
    expect(box).toHaveFocus();
    expect(box).toBeInvalid();
    expect(screen.getByText("Tick the box above to continue.")).toBeInTheDocument();

    await user.click(box);
    expect(screen.queryByText("Tick the box above to continue.")).not.toBeInTheDocument();
    await user.click(add);
    expect(onConfirm).toHaveBeenCalledOnce();
  });

  it("does not confirm twice while the request runs", async () => {
    const user = userEvent.setup();
    const onConfirm = vi.fn();
    show(<ConfirmStep title="Add?" facts={facts} confirmLabel="Add" onConfirm={onConfirm} busy />);
    await user.click(screen.getByRole("button", { name: "Add" }));
    expect(onConfirm).not.toHaveBeenCalled();
    expect(screen.getByText("Working…")).toBeInTheDocument();
  });
});

describe("FileList", () => {
  it("keeps a refused file in the list, with its reason in words", () => {
    show(
      <FileList
        files={[
          { id: 1, name: "a.mkv", kind: "video", size: 2.7 * GB, status: "ok" },
          {
            id: 2,
            name: "b.mkv",
            kind: "video",
            size: 412 * MB,
            status: "refused",
            reason: "No video was found inside this file.",
            from: "season.zip",
          },
        ]}
      />,
    );
    const [ok, refused] = within(
      screen.getByRole("list", { name: "Files in this upload" }),
    ).getAllByRole("listitem");
    expect(ok).toHaveTextContent("Accepted");
    expect(plain(ok?.textContent ?? "")).toContain("2.7 GB");
    expect(refused).toHaveTextContent("Refused");
    expect(refused).toHaveTextContent("No video was found inside this file.");
    expect(refused).toHaveTextContent("From season.zip");
    expect(screen.getByText(/A refused file is not kept/)).toBeInTheDocument();
  });

  it("says nothing about refusals when there are none", () => {
    show(
      <FileList
        files={[{ id: 1, name: "a.srt", kind: "subtitle", size: 1704, status: "pending" }]}
      />,
    );
    expect(screen.getByRole("listitem")).toHaveTextContent("Not checked yet");
    expect(screen.queryByText(/A refused file is not kept/)).not.toBeInTheDocument();
  });
});
