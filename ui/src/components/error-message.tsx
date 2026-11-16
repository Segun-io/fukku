import { ApiError } from "@/api/client";
import { isKnownErrorCode } from "@/api/types";
import { type TKey, useT } from "@/i18n";

/** The translation key for an API error, by its code. `message` is English and not shown. */
export function errorKey(error: Error): TKey {
  const code = error instanceof ApiError ? error.code : "";
  return isKnownErrorCode(code) ? (`errors.${code}` as TKey) : "errors.unknown";
}

/** An API error in the user's language, by its code. */
export function ErrorMessage({ error }: { error: Error }) {
  const { t } = useT();
  return (
    <p role="alert" className="mt-2 text-sm text-destructive">
      {t(errorKey(error))}
    </p>
  );
}
