import { useEffect, useRef } from "react";
import { useRouter } from "@tanstack/react-router";

/**
 * Mirrors one table's view state into a single `tv-<persistKey>` query param
 * with `replace` navigation (no history entries), preserving every other
 * param, like `useSearchParam`. Rendered only for tables with a `persistKey`,
 * so tables without one never touch the router.
 *
 * On mount it hands the param value found in the URL to `onInitial` (a reload
 * or shared link restores the view). Without a router (tests, embedded
 * tables) it reads `window.location` once and writes nothing.
 */
export function DataTableUrlSync({
  param,
  encoded,
  onInitial,
}: {
  param: string;
  /** Current state, encoded; empty when the table is in its default state. */
  encoded: string;
  onInitial: (value: string | null) => void;
}) {
  const router = useRouter({ warn: false });
  const last = useRef(encoded);
  const timer = useRef<ReturnType<typeof setTimeout> | undefined>(undefined);

  useEffect(() => {
    const search = router?.state.location.searchStr ?? window.location.search;
    onInitial(new URLSearchParams(search).get(param));
    return () => clearTimeout(timer.current);
    // Mount-only: later column or router changes must not re-apply the URL.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, []);

  useEffect(() => {
    if (encoded === last.current) return;
    last.current = encoded;
    if (!router) return;
    clearTimeout(timer.current);
    timer.current = setTimeout(() => {
      const { pathname, searchStr } = router.state.location;
      const params = new URLSearchParams(searchStr);
      if ((params.get(param) ?? "") === encoded) return;
      if (encoded) params.set(param, encoded);
      else params.delete(param);
      const qs = params.toString();
      void router.navigate({
        to: qs ? `${pathname}?${qs}` : pathname,
        replace: true,
        resetScroll: false,
      });
    }, 300);
  }, [encoded, param, router]);

  return null;
}
