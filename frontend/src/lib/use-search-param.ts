import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import { useNavigate, useLocation } from "@tanstack/react-router";

/**
 * A single string URL query param, generalising `useTabParam` (P023.7) for
 * free-text filters rather than an allowlisted set of values.
 *
 * Reads the current value from the URL; the returned setter replaces the URL
 * (no new history entry, no scroll jump) and preserves every other query
 * param. Pass `debounceMs` to delay the URL write (e.g. while the caller
 * still wants every keystroke locally) — the returned value updates
 * immediately regardless, only the URL/history write is delayed.
 *
 * This hook reads and replaces the current TanStack Router location
 * directly, so it must run below the application's RouterProvider.
 */
export function useSearchParam(
  key: string,
  options?: { debounceMs?: number },
): [string, (value: string) => void] {
  const debounceMs = options?.debounceMs ?? 0;
  const navigate = useNavigate();
  const pathname = useLocation({ select: (location) => location.pathname });
  const searchStr = useLocation({ select: (location) => location.searchStr });
  const searchParams = useMemo(
    () => new URLSearchParams(searchStr),
    [searchStr],
  );
  const urlValue = searchParams.get(key) ?? "";

  // The URL is the source of truth; `value` only shadows it while a
  // debounced write is pending. Reset the shadow during render (React's
  // documented way to resync state from a changed input) rather than in an
  // effect, so an external URL change (back/forward, another setter) is
  // never masked by a stale pending value.
  const [seenUrlValue, setSeenUrlValue] = useState(urlValue);
  const [pending, setPending] = useState<string | null>(null);
  if (urlValue !== seenUrlValue) {
    setSeenUrlValue(urlValue);
    setPending(null);
  }
  const value = pending ?? urlValue;

  const timer = useRef<ReturnType<typeof setTimeout> | undefined>(undefined);
  useEffect(() => () => clearTimeout(timer.current), []);

  const commit = useCallback(
    (next: string) => {
      const params = new URLSearchParams(searchParams.toString());
      if (next) params.set(key, next);
      else params.delete(key);
      const qs = params.toString();
      void navigate({
        to: qs ? `${pathname}?${qs}` : pathname,
        replace: true,
        resetScroll: false,
      });
    },
    [navigate, pathname, searchParams, key],
  );

  const setSearchParam = useCallback(
    (next: string) => {
      setPending(next);
      clearTimeout(timer.current);
      if (debounceMs > 0) {
        timer.current = setTimeout(() => commit(next), debounceMs);
      } else {
        commit(next);
      }
    },
    [commit, debounceMs],
  );

  return [value, setSearchParam];
}
