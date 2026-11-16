import { useRouter } from "@tanstack/react-router";
import type { TKey } from "@/i18n";

export type NavItem = { to: string; label: TKey };

/** Every destination the header can show, in order. */
export const destinations: NavItem[] = [
  { to: "/upload", label: "nav.upload" },
  { to: "/jobs", label: "nav.jobs" },
  { to: "/design", label: "shell.nav.design" },
];

/**
 * The destinations that have a route in this build. A page appears in the header when its
 * route is in the router (/design exists only in development builds), so the header never
 * links to a page that is not there.
 */
export function useNav(): NavItem[] {
  const router = useRouter();
  return destinations.filter((item) => item.to in router.routesByPath);
}
