import { ExternalLink } from "lucide-react";
import type { ManualImport } from "@/api/types";
import { CopyField } from "@/components/patterns/copy-field";
import { Button } from "@/components/ui/button";
import { useT } from "@/i18n";
import { cn } from "@/lib/utils";

const appName = { radarr: "Radarr", sonarr: "Sonarr" } as const;

/**
 * The manual fallback of a `probed` job (M1): per app the folder to copy, the link when the
 * server knows an address a browser can open, and two sentences on what to do there. It is
 * what the user does next, so the job page shows it before the history and the files.
 */
export function ManualImportSection({
  items,
  className,
}: {
  items: ManualImport[];
  className?: string;
}) {
  const { t } = useT();
  if (items.length === 0) return null;
  return (
    <section className={cn("grid gap-4", className)}>
      <div className="grid gap-2">
        <h2>{t("job.manual.title")}</h2>
        <p className="max-w-[65ch] text-ink-2">{t("job.manual.intro")}</p>
      </div>
      <ul className="grid gap-5">
        {items.map((item) => {
          const app = appName[item.app];
          return (
            <li key={item.app}>
              <CopyField
                label={t("job.manual.folder", { app })}
                value={item.folder}
                copyLabel={t("job.manual.copy")}
                copyName={t("job.manual.copyFor", { app })}
                copiedText={t("job.manual.copied")}
                failedText={t("job.manual.copyFailed")}
              >
                {item.url && (
                  <Button asChild variant="outline">
                    <a href={item.url} target="_blank" rel="noopener noreferrer">
                      <ExternalLink aria-hidden="true" />
                      {t("job.manual.open", { app })}
                    </a>
                  </Button>
                )}
              </CopyField>
            </li>
          );
        })}
      </ul>
    </section>
  );
}
