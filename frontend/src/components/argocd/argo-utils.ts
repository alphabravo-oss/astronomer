// Phase B1 helpers for the ArgoCD UI. Pure utilities — no React.
//
// Live applications come back from the upstream ArgoCD API in their raw
// Kubernetes-shaped form (metadata/spec/status); the rest of the UI wants
// flat camelCase rows. This module owns the translation so the data-table
// columns and detail page can share a single mental model.

import type { ArgoLiveApplication, ArgoSyncStatus, ArgoHealthStatus } from '@/types';

export interface FlatArgoApp {
  name: string;
  namespace: string;
  project: string;
  syncStatus: ArgoSyncStatus;
  healthStatus: ArgoHealthStatus;
  revision: string;
  repoURL: string;
  path: string;
  targetRevision: string;
  destinationServer: string;
  destinationNamespace: string;
  uid?: string;
  // Carried through verbatim for components that want to inspect more
  // fields without re-deriving them.
  raw: ArgoLiveApplication;
}

const SYNC_STATUSES: ArgoSyncStatus[] = ['Synced', 'OutOfSync', 'Unknown'];
const HEALTH_STATUSES: ArgoHealthStatus[] = [
  'Healthy',
  'Degraded',
  'Progressing',
  'Suspended',
  'Missing',
  'Unknown',
];

function coerceSync(s?: string): ArgoSyncStatus {
  return (SYNC_STATUSES.find((x) => x === s) ?? 'Unknown') as ArgoSyncStatus;
}

function coerceHealth(s?: string): ArgoHealthStatus {
  return (HEALTH_STATUSES.find((x) => x === s) ?? 'Unknown') as ArgoHealthStatus;
}

export function flattenArgoApp(app: ArgoLiveApplication): FlatArgoApp {
  return {
    name: app.metadata?.name ?? '',
    namespace: app.metadata?.namespace ?? '',
    project: app.spec?.project ?? 'default',
    syncStatus: coerceSync(app.status?.sync?.status),
    healthStatus: coerceHealth(app.status?.health?.status),
    revision: (app.status?.sync?.revision ?? '').slice(0, 8),
    repoURL: app.spec?.source?.repoURL ?? '',
    path: app.spec?.source?.path ?? '',
    targetRevision: app.spec?.source?.targetRevision ?? 'HEAD',
    destinationServer: app.spec?.destination?.server ?? '',
    destinationNamespace: app.spec?.destination?.namespace ?? '',
    uid: app.metadata?.uid,
    raw: app,
  };
}

/** Truncate a git URL or path for display in tight columns. */
export function shortRepo(url: string, max = 36): string {
  if (!url) return '';
  if (url.length <= max) return url;
  // Prefer dropping the protocol over a hard middle ellipsis.
  const stripped = url.replace(/^https?:\/\//, '').replace(/^git@/, '');
  if (stripped.length <= max) return stripped;
  return stripped.slice(0, max - 1) + '…';
}
