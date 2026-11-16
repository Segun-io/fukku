import { useT } from "@/i18n";

// Placeholder: the upload page is #12.
export function UploadPage() {
  const { t } = useT();
  return (
    <section>
      <h1 className="text-xl font-semibold">{t("upload.title")}</h1>
      <p className="mt-2 text-sm text-muted-foreground">{t("upload.placeholder")}</p>
    </section>
  );
}
