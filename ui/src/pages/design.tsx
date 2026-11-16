import { type ReactNode, useEffect, useState } from "react";
import { ConfirmStep } from "@/components/patterns/confirm-step";
import { CopyField } from "@/components/patterns/copy-field";
import { FileList, type FileListItem } from "@/components/patterns/file-list";
import { FilePicker } from "@/components/patterns/file-picker";
import {
  FileProgress,
  FileProgressList,
  type UploadStatus,
} from "@/components/patterns/file-progress";
import { SelectField } from "@/components/patterns/select-field";
import { type JobEvent, StatusTimeline } from "@/components/patterns/status-timeline";
import { InsecureDevBanner, OfflineBanner, SignInAgain } from "@/components/shell/banners";
import { ShellHeader } from "@/components/shell/header";
import { destinations } from "@/components/shell/nav";
import { EmptyState, ErrorState, LoadingState } from "@/components/shell/states";
import { Button } from "@/components/ui/button";
import { useT } from "@/i18n";

// The pattern examples, with fixed sample data. This page is registered in development
// builds only (see router.tsx), so none of it reaches the embedded UI.

const GB = 1e9;
const MB = 1e6;
const film = "Some Film (2001).mkv";
const noop = () => {};
const accepted = [".mkv", ".mp4", ".srt", ".zip"];

function Section({ title, children }: { title: string; children: ReactNode }) {
  return (
    <section className="grid gap-5 border-t pt-8">
      <h2>{title}</h2>
      {children}
    </section>
  );
}

function Example({ title, children }: { title: string; children: ReactNode }) {
  return (
    <div className="grid gap-3">
      <h3 className="text-muted-foreground">{title}</h3>
      {children}
    </div>
  );
}

function at(hour: number, minute: number) {
  return `2026-01-01T${String(hour).padStart(2, "0")}:${String(minute).padStart(2, "0")}:00Z`;
}

const upTo = {
  probing: [
    { at: at(12, 0), from: null, to: "uploading" },
    { at: at(13, 10), from: "uploading", to: "uploaded" },
    { at: at(13, 11), from: "uploaded", to: "probing" },
  ],
  awaiting: [
    { at: at(12, 0), from: null, to: "uploading" },
    { at: at(13, 10), from: "uploading", to: "uploaded" },
    { at: at(13, 11), from: "uploaded", to: "probing" },
    { at: at(13, 14), from: "probing", to: "probed" },
    { at: at(13, 20), from: "probed", to: "matched" },
    { at: at(13, 21), from: "matched", to: "awaiting-confirmation" },
  ],
} satisfies Record<string, JobEvent[]>;

const histories = {
  running: upTo.probing,
  failed: [...upTo.probing, { at: at(13, 21), from: "probing", to: "failed" }],
  stalled: [
    ...upTo.awaiting,
    { at: at(13, 40), from: "awaiting-confirmation", to: "importing" },
    { at: at(13, 55), from: "importing", to: "stalled" },
  ],
  cancelled: [...upTo.awaiting, { at: at(15, 2), from: "awaiting-confirmation", to: "cancelled" }],
  done: [
    ...upTo.awaiting,
    { at: at(13, 40), from: "awaiting-confirmation", to: "importing" },
    { at: at(13, 41), from: "importing", to: "imported" },
    { at: at(13, 41), from: "imported", to: "post-processing" },
    { at: at(13, 43), from: "post-processing", to: "done" },
  ],
} satisfies Record<string, JobEvent[]>;

/** A row that keeps uploading, to check that the figures change without moving anything. */
const liveSize = 80 * GB;

function LiveRow() {
  const size = liveSize;
  const [sent, setSent] = useState(31.4 * GB);
  const [speed, setSpeed] = useState(11.8 * MB);
  const [status, setStatus] = useState<UploadStatus>("uploading");

  useEffect(() => {
    if (status !== "uploading") return;
    const timer = window.setInterval(() => {
      const next = Math.max(2 * MB, Math.min(24 * MB, speed + (Math.random() - 0.5) * 3 * MB));
      setSpeed(next);
      setSent((value) => (value + next * 0.5 >= size ? 0 : value + next * 0.5));
    }, 500);
    return () => window.clearInterval(timer);
  }, [status, speed]);

  return (
    <FileProgress
      name="Some.Long.Release.Name.2001.1080p.BluRay.x264.DTS-HD.MA.5.1-GROUP.mkv"
      size={size}
      sent={sent}
      status={status}
      bytesPerSecond={speed}
      secondsLeft={(size - sent) / speed}
      onPause={() => setStatus("paused")}
      onResume={() => setStatus("uploading")}
      onRemove={noop}
    />
  );
}

export function DesignPage() {
  const { t, formatBytes } = useT();
  const [confirmed, setConfirmed] = useState(false);

  const files: FileListItem[] = [
    { id: 1, name: "Some Show - Season 2.zip", kind: "archive", size: 21.4 * GB, status: "ok" },
    {
      id: 2,
      name: "Some Show - S02E01 - The Long Way Round.mkv",
      kind: "video",
      size: 2.7 * GB,
      status: "ok",
      from: "Some Show - Season 2.zip",
    },
    {
      id: 3,
      name: "Some Show - S02E02.mkv",
      kind: "video",
      size: 412 * MB,
      status: "refused",
      reason: t("patterns.demo.files.refused1"),
      from: "Some Show - Season 2.zip",
    },
    { id: 4, name: "Some Show - S02E01.es.srt", kind: "subtitle", size: 48_200, status: "ok" },
    { id: 5, name: "Some Show - S02E03.mkv", kind: "video", size: 2.6 * GB, status: "pending" },
    {
      id: 6,
      name: "Some Show - Extras.rar",
      kind: "archive",
      size: 1.9 * GB,
      status: "refused",
      reason: t("patterns.demo.files.refused2"),
    },
  ];

  return (
    <div className="mx-auto grid max-w-(--container-flow) gap-10">
      <div className="grid gap-3">
        <h1>{t("patterns.demo.title")}</h1>
        <p className="text-ink-2">{t("patterns.demo.intro")}</p>
      </div>

      <Section title={t("patterns.demo.progress.title")}>
        <FileProgressList>
          <FileProgress
            name={film}
            size={80 * GB}
            sent={31.4 * GB}
            status="uploading"
            bytesPerSecond={11.8 * MB}
            secondsLeft={(48.6 * GB) / (11.8 * MB)}
            onPause={noop}
            onRemove={noop}
          />
          <FileProgress
            name={film}
            size={80 * GB}
            sent={2.1 * GB}
            status="uploading"
            bytesPerSecond={0.6 * MB}
            secondsLeft={(77.9 * GB) / (0.6 * MB)}
            note={t("patterns.progress.hint.slow")}
            onPause={noop}
            onRemove={noop}
          />
          <FileProgress
            name={film}
            size={80 * GB}
            sent={31.4 * GB}
            status="paused"
            onResume={noop}
            onRemove={noop}
          />
          <FileProgress
            name={film}
            size={80 * GB}
            sent={31.6 * GB}
            status="uploading"
            bytesPerSecond={9.4 * MB}
            secondsLeft={(48.4 * GB) / (9.4 * MB)}
            note={t("patterns.progress.hint.resumed", { sent: formatBytes(31.4 * GB) })}
            onPause={noop}
            onRemove={noop}
          />
          <FileProgress
            name={film}
            size={80 * GB}
            sent={31.4 * GB}
            status="reconnecting"
            onRemove={noop}
          />
          <FileProgress
            name="Some Other Film (1987) - Director’s Cut.mkv"
            size={84.2 * GB}
            sent={0}
            status="failed"
            note={t("patterns.demo.progress.failed")}
            onRetry={noop}
            onRemove={noop}
          />
          <FileProgress
            name="Some Film (2001).es.srt"
            size={61_400}
            sent={0}
            status="queued"
            onRemove={noop}
          />
          <FileProgress name={film} size={80 * GB} sent={80 * GB} status="done" />
          <FileProgress name={film} size={80 * GB} sent={31.4 * GB} status="unfinished" />
        </FileProgressList>

        <Example title={t("patterns.demo.progress.live")}>
          <FileProgressList>
            <LiveRow />
          </FileProgressList>
        </Example>
      </Section>

      <Section title={t("patterns.demo.timeline.title")}>
        <Example title={t("patterns.demo.timeline.running")}>
          <StatusTimeline state="probing" history={histories.running} />
        </Example>
        <Example title={t("patterns.demo.timeline.failed")}>
          <StatusTimeline
            state="failed"
            history={histories.failed}
            detail={t("patterns.demo.timeline.failedDetail")}
            actions={
              <>
                <Button type="button" size="sm" onClick={noop}>
                  {t("patterns.progress.retry")}
                </Button>
                <Button type="button" size="sm" variant="outline" onClick={noop}>
                  {t("patterns.demo.timeline.cancel")}
                </Button>
              </>
            }
          />
        </Example>
        <Example title={t("patterns.demo.timeline.stalled")}>
          <StatusTimeline
            state="stalled"
            history={histories.stalled}
            detail={t("patterns.demo.timeline.stalledDetail")}
          />
        </Example>
        <Example title={t("patterns.demo.timeline.cancelled")}>
          <StatusTimeline state="cancelled" history={histories.cancelled} />
        </Example>
        <Example title={t("patterns.demo.timeline.done")}>
          <StatusTimeline state="done" history={histories.done} />
        </Example>
      </Section>

      <Section title={t("patterns.demo.confirm.title")}>
        <ConfirmStep
          title={t("patterns.demo.confirm.heading")}
          facts={[
            { term: t("patterns.demo.confirm.film"), value: "Some Film (2001)" },
            { term: t("patterns.demo.confirm.file"), value: film },
            { term: t("patterns.demo.confirm.quality"), value: "WEBDL-1080p", figure: true },
            {
              term: t("patterns.demo.confirm.audio"),
              value: t("patterns.demo.confirm.audioValue"),
            },
          ]}
          warnings={[t("patterns.demo.confirm.warn1"), t("patterns.demo.confirm.warn2")]}
          confirmLabel={t("patterns.demo.confirm.action")}
          onConfirm={() => setConfirmed(true)}
          cancelLabel={t("patterns.demo.confirm.back")}
          onCancel={() => setConfirmed(false)}
        />
        <p aria-live="polite" className="min-h-[1lh] text-sm text-muted-foreground">
          {confirmed ? t("patterns.demo.confirm.result") : ""}
        </p>
      </Section>

      <Section title={t("patterns.demo.files.title")}>
        <FileList files={files} />
      </Section>

      <Section title={t("patterns.demo.picker.title")}>
        <Example title={t("patterns.demo.picker.full")}>
          <FilePicker
            as="h3"
            title={t("upload.drop.title")}
            hint={t("upload.drop.hint")}
            chooseLabel={t("upload.choose")}
            accepted={t("upload.drop.accepted", { types: accepted.join(" ") })}
            accept={accepted.join(",")}
            onFiles={noop}
          />
        </Example>
        <Example title={t("patterns.demo.picker.compact")}>
          <FilePicker
            as="h3"
            size="compact"
            primary={false}
            title={t("upload.drop.title")}
            hint={t("upload.drop.hint")}
            chooseLabel={t("upload.choose")}
            accepted={t("upload.drop.accepted", { types: accepted.join(" ") })}
            accept={accepted.join(",")}
            onFiles={noop}
          />
        </Example>
      </Section>

      <Section title={t("patterns.demo.copy.title")}>
        <CopyField
          label={t("job.manual.folder", { app: "Radarr" })}
          value="/data/intake/jobs/0000000000000000000000000000002a/files"
          copyLabel={t("job.manual.copy")}
          copyName={t("job.manual.copyFor", { app: "Radarr" })}
          copiedText={t("job.manual.copied")}
          failedText={t("job.manual.copyFailed")}
        />
      </Section>

      <Section title={t("patterns.demo.select.title")}>
        <SelectField label={t("jobs.filter.state")} name="demo-state" defaultValue="">
          <option value="">{t("jobs.filter.allStates")}</option>
          <option value="uploading">{t("state.uploading")}</option>
          <option value="failed">{t("state.failed")}</option>
        </SelectField>
      </Section>

      <Section title={t("patterns.demo.shell.title")}>
        <Example title={t("patterns.demo.shell.nav")}>
          <div className="overflow-hidden rounded-lg border">
            <ShellHeader nav={destinations.slice(0, 2)} />
          </div>
        </Example>
        <Example title={t("patterns.demo.shell.banner")}>
          <div className="overflow-hidden rounded-lg">
            <InsecureDevBanner />
          </div>
        </Example>
        <Example title={t("patterns.demo.shell.offline")}>
          <div className="overflow-hidden rounded-lg border">
            <OfflineBanner />
          </div>
        </Example>
        <Example title={t("patterns.demo.shell.signedOut")}>
          <SignInAgain />
        </Example>
        <Example title={t("patterns.demo.shell.empty")}>
          <EmptyState
            as="h3"
            title={t("patterns.demo.empty.title")}
            action={
              <Button type="button" onClick={noop}>
                {t("patterns.demo.empty.action")}
              </Button>
            }
          >
            {t("patterns.demo.empty.body")}
          </EmptyState>
        </Example>
        <Example title={t("patterns.demo.shell.error")}>
          <ErrorState as="h3" detail={t("patterns.demo.error.detail")} onRetry={noop} />
        </Example>
        <Example title={t("patterns.demo.shell.loading")}>
          <LoadingState />
        </Example>
      </Section>
    </div>
  );
}
