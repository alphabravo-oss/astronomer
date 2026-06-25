// §HostMounts — host-side mount registry.
//
// `useEnabledExtensions()` fetches the viewer-readable /mounts/ projection and
// indexes it by mount point so a host page can ask "what mounts at point X?".
// The indexing itself is a pure function (`indexMounts`) so it can be unit
// tested without React. Both Tier 1 (declarative) and Tier 2 (bundle) mounts
// flow through the same index; the tier is read off each mount, never authored
// here (see the design doc: "Tier is derived, not authored").

import { useQuery } from '@tanstack/react-query';
import { queryKeys } from '@/lib/query-keys';
import * as extensionsApi from '@/lib/api/extensions';
import type {
  ExtensionMount,
  ExtensionMountsResponse,
  ExtensionPointKind,
} from '@/lib/api/extensions';

// Indexed view of every enabled mount, keyed by the four mount points. Each
// bucket is always present (possibly empty) so consumers never null-check.
export type ExtensionRegistry = Record<ExtensionPointKind, ExtensionMount[]>;

export const EXTENSION_POINTS: ExtensionPointKind[] = [
  'sidebar',
  'dashboardWidget',
  'clusterTab',
  'settingsPage',
];

export function emptyRegistry(): ExtensionRegistry {
  return {
    sidebar: [],
    dashboardWidget: [],
    clusterTab: [],
    settingsPage: [],
  };
}

// Map the wire response's four buckets onto the canonical point kinds.
// The endpoint uses `dashboardWidgets`/`settings`; the registry (and the rest
// of the host runtime) speaks `dashboardWidget`/`settingsPage`, matching the
// `point` field each mount already carries.
function bucketsOf(
  res: ExtensionMountsResponse | undefined,
): Record<ExtensionPointKind, ExtensionMount[]> {
  return {
    sidebar: res?.sidebar ?? [],
    dashboardWidget: res?.dashboardWidgets ?? [],
    clusterTab: res?.clusterTabs ?? [],
    settingsPage: res?.settings ?? [],
  };
}

// Pure: build the indexed registry from a /mounts/ response.
//
// Defensive on two fronts because the payload is a projection of a third-party
// manifest: (1) a mount is dropped unless it carries a `render` (a point with
// no render mounts nothing — the legacy-registry case from the design doc);
// (2) a mount is filed under its own `point` field when that disagrees with the
// bucket it arrived in, so a malformed projection can't smuggle a clusterTab
// into the sidebar list.
export function indexMounts(res: ExtensionMountsResponse | undefined): ExtensionRegistry {
  const registry = emptyRegistry();
  const buckets = bucketsOf(res);
  for (const kind of EXTENSION_POINTS) {
    for (const mount of buckets[kind]) {
      if (!mount || !mount.render) continue;
      if (!mount.render.declarative && !mount.render.bundle) continue;
      const target = mount.point && registry[mount.point] ? mount.point : kind;
      registry[target].push(mount);
    }
  }
  return registry;
}

// React Query hook: the single source of the enabled-extension registry. The
// ExtensionProvider builds its context value from this; pages may also call it
// directly. Cached under queryKeys.extensions.mounts so Disable (which drops a
// mount from /mounts/) takes effect on the next refetch/invalidate.
export function useEnabledExtensions() {
  return useQuery({
    queryKey: queryKeys.extensions.mounts,
    queryFn: () => extensionsApi.getExtensionMounts(),
    // Mounts change rarely (admin install/enable/disable); avoid refetch churn.
    staleTime: 60_000,
    select: indexMounts,
  });
}
