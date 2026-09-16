
import { useCallback, useMemo } from "react";
import { useNavigate, useLocation } from "@tanstack/react-router";

/**
 * Deep-linkable tab state backed by a URL query param (default `?tab=`).
 *
 * Reads the active tab from the current URL, validating it against the
 * allowed `keys`; anything unrecognised (or missing) resolves to `fallback`.
 * The returned setter replaces the URL so switching
 * tabs is a same-page history *replacement* (no scroll jump, no new history
 * entry) and every other query param is preserved.
 *
 * Usage:
 *   const [tab, setTab] = useTabParam(['cis', 'templates', 'policies'], 'cis');
 *
 * This hook reads and replaces the current TanStack Router location directly,
 * so it must run below the application's RouterProvider.
 */
export function useTabParam<T extends string>(
  keys: readonly T[],
  fallback: T,
  paramName = "tab",
): [T, (tab: T) => void] {
  const navigate = useNavigate();
  const pathname = useLocation({ select: (location) => location.pathname });
  const searchStr = useLocation({ select: (location) => location.searchStr });
  const searchParams = useMemo(() => new URLSearchParams(searchStr), [searchStr]);

  // Resolve the active tab from the URL, falling back when absent/invalid.
  const raw = searchParams.get(paramName);
  const tab =
    raw && (keys as readonly string[]).includes(raw) ? (raw as T) : fallback;

  const setTab = useCallback(
    (next: T) => {
      // Clone current params so unrelated query state (filters, ids, …) survives.
      const params = new URLSearchParams(searchParams.toString());
      params.set(paramName, next);
      // Shallow same-page replace: no full navigation, no scroll reset.
      void navigate({
        to: `${pathname}?${params.toString()}`,
        replace: true,
        resetScroll: false,
      });
    },
    [navigate, pathname, searchParams, paramName],
  );

  return [tab, setTab];
}
