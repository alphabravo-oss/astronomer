'use client';

import { useCallback } from 'react';
import { usePathname, useRouter, useSearchParams } from '@/lib/navigation';

/**
 * Deep-linkable tab state backed by a URL query param (default `?tab=`).
 *
 * Reads the active tab from the current URL, validating it against the
 * allowed `keys`; anything unrecognised (or missing) resolves to `fallback`.
 * The returned setter rewrites the URL via `router.replace` so switching
 * tabs is a same-page history *replacement* (no scroll jump, no new history
 * entry) and every other query param is preserved.
 *
 * Usage:
 *   const [tab, setTab] = useTabParam(['cis', 'templates', 'policies'], 'cis');
 *
 * Next 16 App Router note: this is a client-only hook (uses
 * useSearchParams/usePathname/useRouter via @/lib/navigation), so the host
 * component must be a Client Component and ideally wrapped in <Suspense>
 * (dashboard pages here already render under a client layout).
 */
export function useTabParam<T extends string>(
  keys: readonly T[],
  fallback: T,
  paramName = 'tab',
): [T, (tab: T) => void] {
  const router = useRouter();
  const pathname = usePathname();
  const searchParams = useSearchParams();

  // Resolve the active tab from the URL, falling back when absent/invalid.
  const raw = searchParams.get(paramName);
  const tab = raw && (keys as readonly string[]).includes(raw) ? (raw as T) : fallback;

  const setTab = useCallback(
    (next: T) => {
      // Clone current params so unrelated query state (filters, ids, …) survives.
      const params = new URLSearchParams(searchParams.toString());
      params.set(paramName, next);
      // Shallow same-page replace: no full navigation, no scroll reset.
      router.replace(`${pathname}?${params.toString()}`, { scroll: false });
    },
    [router, pathname, searchParams, paramName],
  );

  return [tab, setTab];
}
