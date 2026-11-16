import { Link, type LinkProps } from "@tanstack/react-router";
import { useT } from "@/i18n";
import { LanguageSwitch } from "@/i18n/language-switch";
import { Logo } from "./logo";
import type { NavItem } from "./nav";

/**
 * The top bar: the wordmark, the destinations and the language.
 *
 * From 40rem up it is one line. Below that the destinations take a second line of their
 * own, which scrolls sideways before it ever wraps: Spanish labels are about a third
 * longer, and a phone is 360px wide. In the source the destinations come before the
 * language, which is the order a keyboard meets them on the one-line header.
 */
export function ShellHeader({ nav }: { nav: NavItem[] }) {
  const { t } = useT();
  return (
    <header className="border-b bg-background">
      <div className="shell-container grid grid-cols-[auto_minmax(0,1fr)] items-center gap-x-6 sm:grid-cols-[auto_minmax(0,1fr)_auto]">
        <Link
          to="/"
          className="-ml-1 flex h-14 items-center gap-2 rounded-md px-1 font-display text-lg font-bold tracking-tight text-foreground no-underline transition-opacity duration-150 hover:opacity-80"
        >
          <Logo className="text-primary" />
          <span translate="no">Fukku</span>
        </Link>

        {nav.length > 0 && (
          <nav
            aria-label={t("shell.nav.label")}
            className="col-span-2 row-start-2 -mx-1 flex gap-1 overflow-x-auto border-t px-1 sm:col-span-1 sm:col-start-2 sm:row-start-1 sm:mx-0 sm:border-t-0 sm:px-0"
          >
            {nav.map((item) => (
              <Link
                key={item.to}
                to={item.to as LinkProps["to"]}
                className="relative flex h-11 shrink-0 items-center rounded-md px-3 font-medium whitespace-nowrap text-muted-foreground no-underline transition-colors duration-150 after:absolute after:inset-x-3 after:bottom-0 after:h-0.5 after:rounded-full hover:text-foreground aria-[current=page]:text-foreground aria-[current=page]:after:bg-primary sm:h-14"
              >
                {t(item.label)}
              </Link>
            ))}
          </nav>
        )}

        <div className="col-start-2 row-start-1 justify-self-end sm:col-start-3">
          <LanguageSwitch />
        </div>
      </div>
    </header>
  );
}
