// §DataProxy (browser side) — Tier-1 data fetch helper + its React Query key.
//
// The browser names a (extension, dataSourceId, context); the server re-derives
// the upstream URL and re-runs RBAC against the caller's own bindings. This is
// the thin client wrapper the DeclarativeWidget renderers call. The bridge
// (Tier 2) reaches the same proxy via a ticket — see requestExtensionBridgeToken.

import { queryKeys } from '@/lib/query-keys';
import * as extensionsApi from '@/lib/api/extensions';
import type {
  ExtensionContext,
  ExtensionDataRequest,
  ExtensionDataResponse,
} from '@/lib/api/extensions';

export { fetchExtensionData } from '@/lib/api/extensions';

// React Query key for a Tier-1 data fetch. Context is part of the key so the
// same widget on different clusters/projects doesn't collide on one cache entry.
export function extensionDataKey(
  name: string,
  dataSourceId: string,
  context?: ExtensionContext,
) {
  return queryKeys.extensions.data(name, dataSourceId, context as Record<string, unknown> | undefined);
}

// Convenience queryFn factory for `useQuery({ queryKey, queryFn })` at a widget.
export function extensionDataQueryFn<T = unknown>(
  name: string,
  dataSourceId: string,
  req: ExtensionDataRequest = {},
): () => Promise<ExtensionDataResponse<T>> {
  return () => extensionsApi.fetchExtensionData<T>(name, dataSourceId, req);
}
