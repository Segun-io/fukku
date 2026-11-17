import { Link, useNavigate } from "@tanstack/react-router";
import { TriangleAlert } from "lucide-react";
import { useId, useState } from "react";
import { ApiError } from "@/api/client";
import { useMatchAction } from "@/api/match";
import type { AppName, MatchRequest, SeriesType } from "@/api/types";
import { ErrorMessage } from "@/components/error-message";
import { SelectField } from "@/components/patterns/select-field";
import { Notice } from "@/components/shell/notice";
import { Button } from "@/components/ui/button";
import { useT } from "@/i18n";
import type { AppInfos, Hit } from "./hits";

const appName: Record<AppName, string> = { radarr: "Radarr", sonarr: "Sonarr" };
export const nameOf = (app: AppName) => appName[app];

/**
 * The film that already has a file (flow 2): the upload may be another language of it. The
 * button chooses the film for the job, which the server marks as flow 2, and the next page
 * compares the upload with the library file.
 */
export function ExistsNotice({ jobId, tmdbId }: { jobId: string; tmdbId: number }) {
  const { t } = useT();
  const navigate = useNavigate();
  const match = useMatchAction(jobId);
  return (
    <div className="grid gap-3">
      <Notice
        role="note"
        tone="caution"
        icon={TriangleAlert}
        titleAs="h2"
        title={t("match.exists.title")}
        action={
          <>
            <Button
              type="button"
              size="lg"
              disabled={match.isPending}
              onClick={() =>
                match.mutate(
                  { app: "radarr", tmdbId, monitored: true },
                  {
                    onSuccess: () =>
                      void navigate({ to: "/jobs/$id/flow2", params: { id: jobId } }),
                  },
                )
              }
            >
              {t("match.exists.action")}
            </Button>
            <Link
              to="/jobs/$id"
              params={{ id: jobId }}
              className="inline-flex min-h-11 items-center font-medium underline underline-offset-4"
            >
              {t("match.manual")}
            </Link>
          </>
        }
      >
        {t("match.exists.body")}
      </Notice>
      {match.isError && <ErrorMessage error={match.error} />}
    </div>
  );
}

type Props = { jobId: string; hit: Hit; infos: AppInfos };

/**
 * The options of the chosen title, pre-filled from the app's defaults, and the button that
 * sends the choice. A title that is already in the app is not added, so it has no options.
 * Mounted with a `key` of the title, so every title starts from the defaults again.
 */
export function MatchOptions({ jobId, hit, infos }: Props) {
  const { t } = useT();
  const navigate = useNavigate();
  const match = useMatchAction(jobId);
  const ids = useId();

  const info = hit.app === "radarr" ? infos.radarr : infos.sonarr;
  const profiles = info?.qualityProfiles ?? [];
  const folders = info?.rootFolders ?? [];
  const defaults = info?.defaults;

  const [profile, setProfile] = useState(
    defaults?.qualityProfileId != null ? String(defaults.qualityProfileId) : "",
  );
  const [folder, setFolder] = useState(
    folders.some((f) => f.path === defaults?.rootFolder)
      ? (defaults?.rootFolder ?? "")
      : (folders[0]?.path ?? ""),
  );
  const [monitored, setMonitored] = useState(
    defaults && "monitored" in defaults ? defaults.monitored : true,
  );
  const [seriesType, setSeriesType] = useState<SeriesType>(
    defaults && "seriesType" in defaults ? defaults.seriesType : "standard",
  );

  // The server adds a new title only with a profile the app lists (it fills a gap from the
  // settings, which the defaults already show), so there is nothing to send without one.
  const noProfiles = !hit.inLibrary && profiles.length === 0;
  const needsProfile = !hit.inLibrary && profile === "";

  function request(): MatchRequest {
    const qualityProfileId = profile === "" ? undefined : Number(profile);
    const rootFolder = folder === "" ? undefined : folder;
    return hit.app === "radarr"
      ? { app: "radarr", tmdbId: hit.id, qualityProfileId, rootFolder, monitored }
      : { app: "sonarr", tvdbId: hit.id, qualityProfileId, rootFolder, seriesType };
  }

  function submit(e: React.FormEvent) {
    e.preventDefault();
    if (needsProfile || match.isPending) return;
    match.mutate(request(), {
      // The files are checked against the title in the mapping editor (#45).
      onSuccess: () => void navigate({ to: "/jobs/$id/mapping", params: { id: jobId } }),
    });
  }

  const app = nameOf(hit.app);
  return (
    <form onSubmit={submit} className="grid gap-5 border-t pt-8" aria-labelledby={`${ids}-title`}>
      <h2 id={`${ids}-title`}>{t("match.options.title")}</h2>

      {hit.inLibrary ? (
        <p className="max-w-[65ch] text-ink-2">
          {t("match.options.inLibrary", { title: hit.title, app })}
        </p>
      ) : (
        <div className="grid gap-x-4 gap-y-5 sm:grid-cols-2">
          {profiles.length > 0 && (
            <SelectField
              label={t("match.profile")}
              value={profile}
              onChange={(e) => setProfile(e.target.value)}
            >
              {profile === "" && <option value="">{t("match.profile.choose")}</option>}
              {profiles.map((p) => (
                <option key={p.id} value={String(p.id)}>
                  {p.name}
                </option>
              ))}
            </SelectField>
          )}

          {folders.length > 1 && (
            <SelectField
              label={t("match.folder")}
              value={folder}
              onChange={(e) => setFolder(e.target.value)}
              className="[&_select]:font-mono"
            >
              {folders.map((f) => (
                <option key={f.path} value={f.path}>
                  {f.path}
                </option>
              ))}
            </SelectField>
          )}

          {hit.app === "radarr" && (
            <label className="flex min-h-11 items-center gap-3 sm:col-span-2">
              <input
                type="checkbox"
                className="size-5 shrink-0 accent-primary"
                checked={monitored}
                onChange={(e) => setMonitored(e.target.checked)}
              />
              {t("match.monitored")}
            </label>
          )}

          {hit.app === "sonarr" && (
            <SelectField
              label={t("match.seriesType")}
              value={seriesType}
              onChange={(e) => setSeriesType(e.target.value as SeriesType)}
            >
              {(infos.sonarr?.seriesTypes ?? ["standard", "anime", "daily"]).map((s) => (
                <option key={s} value={s}>
                  {t(`match.seriesType.${s}`)}
                </option>
              ))}
            </SelectField>
          )}
        </div>
      )}

      {noProfiles && (
        <p className="text-sm text-muted-foreground">{t("match.profile.none", { app })}</p>
      )}
      {needsProfile && !noProfiles && (
        <p className="text-sm text-muted-foreground">{t("match.profile.needed")}</p>
      )}
      <div className="grid sm:block">
        <Button type="submit" size="lg" disabled={needsProfile || match.isPending}>
          {t("match.submit")}
        </Button>
      </div>
      {match.isError && (
        <div>
          <ErrorMessage error={match.error} />
          {match.error instanceof ApiError && match.error.code === "already_in_library" && (
            <p>
              <Link
                to="/jobs/$id"
                params={{ id: jobId }}
                className="inline-flex min-h-11 items-center underline underline-offset-4"
              >
                {t("match.manual")}
              </Link>
            </p>
          )}
        </div>
      )}
    </form>
  );
}
