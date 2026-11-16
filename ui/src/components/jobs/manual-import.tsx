import { Check, Copy, ExternalLink } from "lucide-react";
import { useState } from "react";
import type { ManualImport } from "@/api/types";
import { Button } from "@/components/ui/button";
import { useT } from "@/i18n";

const appName = { radarr: "Radarr", sonarr: "Sonarr" } as const;

function FolderRow({ item }: { item: ManualImport }) {
  const { t } = useT();
  const app = appName[item.app];
  const [copy, setCopy] = useState<"idle" | "copied" | "failed">("idle");

  async function copyFolder() {
    try {
      await navigator.clipboard.writeText(item.folder);
      setCopy("copied");
    } catch {
      setCopy("failed");
    }
  }

  return (
    <li className="grid gap-2 py-4">
      <p className="text-sm text-muted-foreground">{t("job.manual.folder", { app })}</p>
      <p
        translate="no"
        className="figure rounded-md bg-muted px-3 py-2 text-sm whitespace-normal [overflow-wrap:anywhere] select-all"
      >
        {item.folder}
      </p>
      <div className="flex flex-wrap items-center gap-2">
        <Button
          type="button"
          variant="outline"
          aria-label={t("job.manual.copyFor", { app })}
          onClick={() => void copyFolder()}
        >
          {copy === "copied" ? <Check aria-hidden="true" /> : <Copy aria-hidden="true" />}
          {t("job.manual.copy")}
        </Button>
        {item.url && (
          <Button asChild variant="outline">
            <a href={item.url} target="_blank" rel="noopener noreferrer">
              <ExternalLink aria-hidden="true" />
              {t("job.manual.open", { app })}
            </a>
          </Button>
        )}
        <span role="status" className="text-sm text-muted-foreground">
          {copy === "copied" && t("job.manual.copied")}
          {copy === "failed" && t("job.manual.copyFailed")}
        </span>
      </div>
    </li>
  );
}

/**
 * The manual fallback of a `probed` job (M1): per app the folder to copy, the link when the
 * server knows an address a browser can open, and two sentences on what to do there.
 */
export function ManualImportSection({ items }: { items: ManualImport[] }) {
  const { t } = useT();
  if (items.length === 0) return null;
  return (
    <section className="grid gap-2 border-t pt-8">
      <h2>{t("job.manual.title")}</h2>
      <p className="max-w-[65ch] text-ink-2">{t("job.manual.intro")}</p>
      <ul className="divide-y">
        {items.map((item) => (
          <FolderRow key={item.app} item={item} />
        ))}
      </ul>
    </section>
  );
}
