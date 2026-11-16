import { ApiError } from "@/api/client";
import { isKnownErrorCode } from "@/api/types";
import { useT } from "@/i18n";

/** An API error in the user's language, by its code. `message` is English and not shown. */
export function ErrorMessage({ error }: { error: Error }) {
  const { t } = useT();
  const code = error instanceof ApiError ? error.code : "";
  return (
    <p role="alert" className="mt-2 text-sm text-destructive">
      {isKnownErrorCode(code) ? t(`errors.${code}`) : t("errors.unknown")}
    </p>
  );
}
