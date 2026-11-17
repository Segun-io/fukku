import { CircleAlert, CircleCheck, RotateCw, TriangleAlert } from "lucide-react";
import { useState } from "react";
import { useAdminSettings, useAdminStatus } from "@/api/admin";
import { useSession } from "@/api/session";
import { type AdminSetting, type AdminStatus, jobStates } from "@/api/types";
import { errorKey } from "@/components/error-message";
import { Notice } from "@/components/shell/notice";
import { ErrorState, LoadingState, NotFound } from "@/components/shell/states";
import { Button } from "@/components/ui/button";
import { type TKey, useT } from "@/i18n";

const checkLabels: Record<string, TKey> = {
  database: "admin.check.database",
  spool: "admin.check.spool",
  sentinels: "admin.check.sentinels",
  intakeRoot: "admin.check.intakeRoot",
  sameFilesystem: "admin.check.sameFilesystem",
  workerHeartbeat: "admin.check.workerHeartbeat",
  radarr: "admin.check.radarr",
  sonarr: "admin.check.sonarr",
};

/**
 * The admin page (plan §4): health and the effective settings, read-only. It is for
 * administrators only: for anyone else the page does not exist, as the endpoints do not.
 */
export function AdminPage() {
  const { t } = useT();
  const session = useSession();
  if (session.isPending) return <LoadingState />;
  if (session.data?.user.admin !== true) return <NotFound />;
  return (
    <div className="grid grid-cols-1 gap-10">
      <header className="grid gap-2">
        <h1>{t("admin.title")}</h1>
        <p className="max-w-[65ch] text-ink-2">{t("admin.intro")}</p>
      </header>
      <HealthSection />
      <SettingsSection />
    </div>
  );
}

function HealthSection() {
  const { t, formatDate } = useT();
  const status = useAdminStatus(true);

  return (
    <section aria-labelledby="admin-health" className="grid grid-cols-1 gap-6 border-t pt-8">
      <div className="flex flex-wrap items-baseline justify-between gap-x-4 gap-y-2">
        <h2 id="admin-health">{t("admin.health.title")}</h2>
        <Button
          type="button"
          variant="outline"
          disabled={status.isFetching}
          onClick={() => void status.refetch()}
        >
          <RotateCw aria-hidden="true" />
          {t("admin.health.refresh")}
        </Button>
      </div>

      {status.isPending && <LoadingState />}
      {status.isError && (
        <ErrorState
          as="h3"
          title={t("admin.health.error.title")}
          onRetry={() => void status.refetch()}
        >
          {t(errorKey(status.error))}
        </ErrorState>
      )}
      {status.isSuccess && (
        <>
          <StatusNotice status={status.data} />
          <p className="text-sm text-muted-foreground">
            <span translate="no">
              {t("admin.health.version", { version: status.data.version })}
            </span>
            {" · "}
            {t("admin.health.checked", { date: formatDate(status.data.checkedAt) })}
          </p>
          <Checks status={status.data} />
          <div className="grid grid-cols-1 gap-8 sm:grid-cols-2">
            <Apps status={status.data} />
            <Worker status={status.data} />
            <Disk status={status.data} />
            <JobCounts status={status.data} />
          </div>
        </>
      )}
    </section>
  );
}

function StatusNotice({ status }: { status: AdminStatus }) {
  const { t } = useT();
  const s = status.readiness.status;
  if (s === "ready")
    return <Notice tone="done" icon={CircleCheck} title={t("admin.status.ready")} />;
  if (s === "degraded") {
    return <Notice tone="caution" icon={TriangleAlert} title={t("admin.status.degraded")} />;
  }
  return <Notice tone="error" icon={CircleAlert} title={t("admin.status.not_ready")} />;
}

function Checks({ status }: { status: AdminStatus }) {
  const { t } = useT();
  return (
    <div>
      <h3 className="text-muted-foreground">{t("admin.checks.title")}</h3>
      <ul className="mt-2 divide-y border-y">
        {status.readiness.checks.map((c) => {
          const label = checkLabels[c.name];
          const Icon = c.ok ? CircleCheck : CircleAlert;
          return (
            <li
              key={c.name}
              className="grid grid-cols-[minmax(0,1fr)_auto] gap-x-4 gap-y-1 py-3 sm:items-baseline"
            >
              <span className="font-medium">{label ? t(label) : c.name}</span>
              <span
                className={`inline-flex items-center gap-1.5 text-sm font-medium ${c.ok ? "text-primary" : "text-destructive"}`}
              >
                <Icon aria-hidden="true" className="size-4 shrink-0" />
                {c.ok ? t("admin.check.ok") : t("admin.check.failing")}
              </span>
              {c.detail && (
                <p className="col-span-2 font-mono text-sm break-words text-muted-foreground">
                  {c.detail}
                </p>
              )}
            </li>
          );
        })}
      </ul>
    </div>
  );
}

function Apps({ status }: { status: AdminStatus }) {
  const { t } = useT();
  return (
    <div>
      <h3 className="text-muted-foreground">{t("admin.apps.title")}</h3>
      {status.apps.length === 0 ? (
        <p className="mt-2 text-ink-2">{t("admin.apps.none")}</p>
      ) : (
        <ul className="mt-2 grid grid-cols-1 gap-2">
          {status.apps.map((a) => {
            const label = checkLabels[a.name];
            return (
              <li key={a.name}>
                <span className="font-medium">{label ? t(label) : a.name}</span>
                <br />
                <span className="figure text-sm text-ink-2">
                  {a.version
                    ? t("admin.apps.version", { version: a.version })
                    : t("admin.apps.noVersion")}
                </span>
              </li>
            );
          })}
        </ul>
      )}
    </div>
  );
}

function Worker({ status }: { status: AdminStatus }) {
  const { t, formatDuration } = useT();
  const { heartbeatAgeSeconds: age, maxAgeSeconds: max } = status.worker;
  return (
    <div>
      <h3 className="text-muted-foreground">{t("admin.worker.title")}</h3>
      <p className="figure mt-2 text-ink-2">
        {age === null
          ? t("admin.worker.none")
          : t("admin.worker.age", {
              age: formatDuration(age * 1000),
              max: formatDuration(max * 1000),
            })}
      </p>
    </div>
  );
}

function Disk({ status }: { status: AdminStatus }) {
  const { t, formatBytes } = useT();
  const { freeBytes, marginBytes, aboveMargin } = status.disk;
  const margin = formatBytes(marginBytes);
  return (
    <div>
      <h3 className="text-muted-foreground">{t("admin.disk.title")}</h3>
      {freeBytes === null ? (
        <p className="mt-2 text-ink-2">{t("admin.disk.unknown", { margin })}</p>
      ) : (
        <>
          <p className="figure mt-2 font-medium">
            {t("admin.disk.free", { free: formatBytes(freeBytes) })}
          </p>
          <p className="text-sm text-ink-2">
            {t(aboveMargin ? "admin.disk.above" : "admin.disk.below", { margin })}
          </p>
        </>
      )}
    </div>
  );
}

function JobCounts({ status }: { status: AdminStatus }) {
  const { t, formatNumber } = useT();
  const jobs = status.jobs;
  return (
    <div>
      <h3 className="text-muted-foreground">{t("admin.jobs.title")}</h3>
      {jobs === null ? (
        <p className="mt-2 text-ink-2">{t("admin.jobs.none")}</p>
      ) : (
        <>
          <p className="mt-2 text-sm text-ink-2">{t("admin.jobs.total", { count: jobs.total })}</p>
          <dl className="mt-2 divide-y border-y">
            {jobStates.map((s) => (
              <div key={s} className="flex items-baseline justify-between gap-4 py-1.5">
                <dt>{t(`state.${s}`)}</dt>
                <dd className="figure">{formatNumber(jobs.byState[s] ?? 0)}</dd>
              </div>
            ))}
          </dl>
        </>
      )}
    </div>
  );
}

function groupOf(key: string): string {
  const dot = key.indexOf(".");
  return dot < 0 ? "mode" : key.slice(0, dot);
}

function SettingsSection() {
  const { t } = useT();
  const settings = useAdminSettings(true);
  const [onlySet, setOnlySet] = useState(false);

  const shown = (settings.data?.settings ?? []).filter((s) => !onlySet || s.source === "set");
  const groups = new Map<string, AdminSetting[]>();
  for (const s of shown) {
    const g = groupOf(s.key);
    groups.set(g, [...(groups.get(g) ?? []), s]);
  }

  return (
    <section aria-labelledby="admin-settings" className="grid grid-cols-1 gap-6 border-t pt-8">
      <div className="grid gap-2">
        <h2 id="admin-settings">{t("admin.settings.title")}</h2>
        <p className="max-w-[65ch] text-ink-2">{t("admin.settings.intro")}</p>
      </div>

      {settings.isPending && <LoadingState />}
      {settings.isError && (
        <ErrorState
          as="h3"
          title={t("admin.settings.error.title")}
          onRetry={() => void settings.refetch()}
        >
          {t(errorKey(settings.error))}
        </ErrorState>
      )}
      {settings.isSuccess && (
        <>
          <label className="flex min-h-11 items-center gap-3">
            <input
              type="checkbox"
              checked={onlySet}
              onChange={(e) => setOnlySet(e.target.checked)}
              className="size-5 accent-primary"
            />
            {t("admin.settings.onlySet")}
          </label>
          {shown.length === 0 && <p className="text-ink-2">{t("admin.settings.none")}</p>}
          {[...groups].map(([group, items]) => (
            <div key={group}>
              <h3 translate="no" className="text-muted-foreground">
                {group}
              </h3>
              <ul className="mt-2 divide-y border-y">
                {items.map((s) => (
                  <SettingRow key={s.key} setting={s} />
                ))}
              </ul>
            </div>
          ))}
        </>
      )}
    </section>
  );
}

function SettingRow({ setting: s }: { setting: AdminSetting }) {
  const { t } = useT();
  let value: string;
  if (s.secret) value = s.value === "" ? t("admin.setting.notSet") : t("admin.setting.hidden");
  else value = s.value === "" ? t("admin.setting.empty") : s.value;
  const literal = !s.secret && s.value !== "";
  return (
    <li className="grid gap-x-6 gap-y-1 py-3 sm:grid-cols-[minmax(0,1fr)_auto]">
      <span translate="no" className="figure font-medium [overflow-wrap:anywhere]">
        {s.key}
      </span>
      <span
        className={`text-sm font-medium sm:col-start-2 sm:row-start-1 sm:justify-self-end ${s.source === "set" ? "text-primary" : "text-muted-foreground"}`}
      >
        {s.source === "set" ? t("admin.setting.set") : t("admin.setting.default")}
      </span>
      <span
        translate={literal ? "no" : undefined}
        className={`${literal ? "figure" : "text-ink-2"} text-sm break-words sm:col-start-1`}
      >
        {value}
      </span>
      <span translate="no" className="figure text-sm text-muted-foreground sm:col-start-1">
        {t("admin.setting.env", { name: s.env })}
      </span>
    </li>
  );
}
