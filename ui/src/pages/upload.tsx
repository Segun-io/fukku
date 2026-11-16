import { ApiError } from "@/api/client";
import { useSession } from "@/api/session";
import { errorKey } from "@/components/error-message";
import { ErrorState, LoadingState } from "@/components/shell/states";
import { UploadView } from "@/features/upload/upload-view";
import { useT } from "@/i18n";

/** The upload page: one column of the flow's width, the title, then the upload itself. */
export function UploadPage() {
  const { t } = useT();
  const session = useSession();
  // A signed-out session is the shell's "sign in again" notice; nothing more to say here.
  const signedOut = session.error instanceof ApiError && session.error.code === "signed_out";
  return (
    <section className="grid max-w-(--container-flow) gap-6">
      <h1>{t("upload.title")}</h1>
      {session.isPending && <LoadingState rows={1} />}
      {session.isError && !signedOut && (
        <ErrorState onRetry={() => void session.refetch()}>{t(errorKey(session.error))}</ErrorState>
      )}
      {session.isSuccess && <UploadView config={session.data.upload} />}
    </section>
  );
}
