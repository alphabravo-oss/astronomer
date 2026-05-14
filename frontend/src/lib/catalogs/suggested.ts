// Curated list of well-known Helm/OCI repositories that admins can
// one-click enable from the Catalog page. The 5 entries flagged with
// `seeded: true` are added by the backend migration on a fresh install
// so they should appear as "Added" out of the box.
//
// EXPLICITLY EXCLUDED (do not add without operator discussion):
//   - Bitnami: Broadcom deprecated; charts now pull stale unpatched images.
//   - rancher/charts: most entries hard-require Rancher Manager.

import type { HelmRepoType } from '@/types';

export interface SuggestedCatalog {
  /** Canonical helm_repositories.name we want to register as. */
  name: string;
  /** User-facing display label. */
  displayName: string;
  /** Index/registry URL — used for exact-match lookup against existing rows. */
  url: string;
  repoType: HelmRepoType;
  /** Short one-liner shown on the card. */
  description: string;
  /** Marker shown on a fresh install: already inserted by the seed migration. */
  seeded?: boolean;
  /** Requires a paid subscription to actually pull images (DHI). */
  subscriptionRequired?: boolean;
}

export const SUGGESTED_CATALOGS: SuggestedCatalog[] = [
  {
    name: 'prometheus-community',
    displayName: 'Prometheus Community',
    url: 'https://prometheus-community.github.io/helm-charts',
    repoType: 'helm',
    description: 'kube-prometheus-stack, prometheus, alertmanager, exporters',
    seeded: true,
  },
  {
    name: 'grafana',
    displayName: 'Grafana',
    url: 'https://grafana.github.io/helm-charts',
    repoType: 'helm',
    description: 'loki-stack, tempo, grafana, mimir',
    seeded: true,
  },
  {
    name: 'jetstack',
    displayName: 'Jetstack',
    url: 'https://charts.jetstack.io',
    repoType: 'helm',
    description: 'cert-manager',
    seeded: true,
  },
  {
    name: 'aqua',
    displayName: 'Aqua Security',
    url: 'https://aquasecurity.github.io/helm-charts',
    repoType: 'helm',
    description: 'trivy-operator',
    seeded: true,
  },
  {
    name: 'fluent',
    displayName: 'Fluent',
    url: 'https://fluent.github.io/helm-charts',
    repoType: 'helm',
    description: 'fluent-bit, fluentd',
    seeded: true,
  },
  {
    name: 'argo',
    displayName: 'Argo',
    url: 'https://argoproj.github.io/argo-helm',
    repoType: 'helm',
    description: 'argo-cd, argo-workflows, argo-rollouts',
  },
  {
    name: 'longhorn',
    displayName: 'Longhorn',
    url: 'https://charts.longhorn.io',
    repoType: 'helm',
    description: 'distributed block storage',
  },
  {
    name: 'neuvector',
    displayName: 'NeuVector',
    url: 'https://neuvector.github.io/neuvector-helm',
    repoType: 'helm',
    description: 'runtime container security',
  },
  {
    name: 'gatekeeper',
    displayName: 'OPA Gatekeeper',
    url: 'https://open-policy-agent.github.io/gatekeeper/charts',
    repoType: 'helm',
    description: 'policy enforcement',
  },
  {
    name: 'dhi',
    displayName: 'Docker Hardened Images',
    url: 'oci://dhi.io',
    repoType: 'oci',
    description: "Docker's hardened image catalog (paid tier)",
    subscriptionRequired: true,
  },
];

/**
 * Normalize a URL for comparison: strip trailing slash, lowercase scheme/host.
 * Helm repo URLs are not consistently normalized server-side; this avoids
 * spurious "not added" states because of a trailing slash.
 */
export function normalizeRepoUrl(url: string): string {
  return url.trim().replace(/\/+$/, '').toLowerCase();
}
