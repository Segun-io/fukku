import { LogIn, ShieldAlert, WifiOff } from "lucide-react";
import type * as React from "react";
import { Button } from "@/components/ui/button";
import { useT } from "@/i18n";
import { cn } from "@/lib/utils";
import { Notice } from "./notice";

/** A band across the top of the app, above the header. */
function Band({ className, children, ...props }: React.ComponentProps<"div">) {
  return (
    <div className={cn("border-b", className)} {...props}>
      <div className="shell-container flex items-start gap-3 py-2.5 text-sm">{children}</div>
    </div>
  );
}

/**
 * Shown when `session.insecureDev` is true: the server runs with sign-in switched off.
 * It is the only use of the alarm colour, it cannot be closed, and it looks the same in
 * light and dark mode.
 */
export function InsecureDevBanner() {
  const { t } = useT();
  return (
    <Band role="alert" className="border-alarm-foreground bg-alarm text-alarm-foreground">
      <ShieldAlert aria-hidden="true" className="mt-0.5 size-5 shrink-0" />
      <p className="min-w-0">
        <strong className="font-bold">{t("shell.insecure.title")}.</strong>{" "}
        {t("shell.insecure.body")}
      </p>
    </Band>
  );
}

/** Shown while the browser has no connection. Uploads continue by themselves afterwards. */
export function OfflineBanner() {
  const { t } = useT();
  return (
    <Band role="status" className="bg-muted text-foreground">
      <WifiOff aria-hidden="true" className="mt-0.5 size-5 shrink-0 text-muted-foreground" />
      <p className="min-w-0">
        <strong className="font-bold">{t("shell.offline.title")}.</strong> {t("shell.offline.body")}
      </p>
    </Band>
  );
}

type SignInAgainProps = {
  /**
   * Where signing in happens: any page of this app, because the proxy in front of Fukku
   * sends a signed-out visitor to its sign-in page.
   */
  href?: string;
  className?: string;
};

/**
 * The "sign in again" state (plan §10): a chunk got a 401 or a redirect. Sign-in opens in a
 * new tab on purpose, so this tab keeps its chosen files and its upload position.
 */
export function SignInAgain({ href = "/", className }: SignInAgainProps) {
  const { t } = useT();
  return (
    <Notice
      role="alert"
      tone="caution"
      icon={LogIn}
      title={t("shell.signedOut.title")}
      className={className}
      action={
        <>
          <Button asChild>
            <a href={href} target="_blank" rel="noopener">
              {t("shell.signedOut.action")}
            </a>
          </Button>
          <span className="text-sm text-ink-2">{t("shell.signedOut.hint")}</span>
        </>
      }
    >
      {t("shell.signedOut.body")}
    </Notice>
  );
}
