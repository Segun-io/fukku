import { Outlet } from "@tanstack/react-router";
import { useEffect } from "react";
import { ApiError } from "@/api/client";
import { useSession } from "@/api/session";
import { useT } from "@/i18n";
import { InsecureDevBanner, OfflineBanner, SignInAgain } from "./banners";
import { ShellHeader } from "./header";
import { useNav } from "./nav";
import { useOnline } from "./use-online";

/**
 * The frame around every page: the insecure-development banner, the header, the offline
 * and signed-out notices, and the main area. Pages render inside <main> and bring no
 * frame of their own.
 */
export function AppShell() {
  const { t } = useT();
  const nav = useNav();
  const online = useOnline();
  const session = useSession();
  const signedOut = session.error instanceof ApiError && session.error.code === "signed_out";
  const refetchSession = session.refetch;

  // Sign-in happens in another tab. When this one is looked at again, ask once more, so the
  // notice goes away by itself.
  useEffect(() => {
    if (!signedOut) return;
    const ask = () => {
      if (document.visibilityState === "visible") void refetchSession();
    };
    window.addEventListener("focus", ask);
    document.addEventListener("visibilitychange", ask);
    return () => {
      window.removeEventListener("focus", ask);
      document.removeEventListener("visibilitychange", ask);
    };
  }, [signedOut, refetchSession]);

  return (
    <div className="flex min-h-dvh flex-col bg-background text-foreground">
      <a
        href="#main"
        className="sr-only focus-visible:not-sr-only focus-visible:fixed focus-visible:top-2 focus-visible:left-2 focus-visible:z-50 focus-visible:rounded-md focus-visible:bg-card focus-visible:px-4 focus-visible:py-3 focus-visible:font-medium"
      >
        {t("shell.skip")}
      </a>
      {session.data?.insecureDev && <InsecureDevBanner />}
      <ShellHeader nav={nav} />
      {!online && <OfflineBanner />}
      <main id="main" className="shell-container flex-1 pt-6 pb-16 sm:pt-10">
        {signedOut && <SignInAgain className="mb-8" />}
        <Outlet />
      </main>
    </div>
  );
}
