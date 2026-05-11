/**
 * Maps K8s resource types to their API paths.
 * Used by the k8s proxy layer to construct correct URLs.
 */

interface K8sResourceDef {
  apiBase: string;
  namespaced: boolean;
  plural: string;
}

const resourceDefs: Record<string, K8sResourceDef> = {
  // Core (api/v1)
  pods:                    { apiBase: 'api/v1', namespaced: true,  plural: 'pods' },
  services:                { apiBase: 'api/v1', namespaced: true,  plural: 'services' },
  configmaps:              { apiBase: 'api/v1', namespaced: true,  plural: 'configmaps' },
  secrets:                 { apiBase: 'api/v1', namespaced: true,  plural: 'secrets' },
  namespaces:              { apiBase: 'api/v1', namespaced: false, plural: 'namespaces' },
  nodes:                   { apiBase: 'api/v1', namespaced: false, plural: 'nodes' },
  persistentvolumes:       { apiBase: 'api/v1', namespaced: false, plural: 'persistentvolumes' },
  persistentvolumeclaims:  { apiBase: 'api/v1', namespaced: true,  plural: 'persistentvolumeclaims' },
  serviceaccounts:         { apiBase: 'api/v1', namespaced: true,  plural: 'serviceaccounts' },
  resourcequotas:          { apiBase: 'api/v1', namespaced: true,  plural: 'resourcequotas' },
  limitranges:             { apiBase: 'api/v1', namespaced: true,  plural: 'limitranges' },
  endpoints:               { apiBase: 'api/v1', namespaced: true,  plural: 'endpoints' },
  events:                  { apiBase: 'api/v1', namespaced: true,  plural: 'events' },

  // Apps (apis/apps/v1)
  deployments:             { apiBase: 'apis/apps/v1', namespaced: true, plural: 'deployments' },
  statefulsets:             { apiBase: 'apis/apps/v1', namespaced: true, plural: 'statefulsets' },
  daemonsets:               { apiBase: 'apis/apps/v1', namespaced: true, plural: 'daemonsets' },
  replicasets:              { apiBase: 'apis/apps/v1', namespaced: true, plural: 'replicasets' },

  // Batch (apis/batch/v1)
  jobs:                    { apiBase: 'apis/batch/v1', namespaced: true, plural: 'jobs' },
  cronjobs:                { apiBase: 'apis/batch/v1', namespaced: true, plural: 'cronjobs' },

  // Networking (apis/networking.k8s.io/v1)
  ingresses:               { apiBase: 'apis/networking.k8s.io/v1', namespaced: true, plural: 'ingresses' },
  networkpolicies:         { apiBase: 'apis/networking.k8s.io/v1', namespaced: true, plural: 'networkpolicies' },

  // Policy (apis/policy/v1)
  poddisruptionbudgets:    { apiBase: 'apis/policy/v1', namespaced: true, plural: 'poddisruptionbudgets' },

  // Autoscaling (apis/autoscaling/v2)
  hpa:                     { apiBase: 'apis/autoscaling/v2', namespaced: true, plural: 'horizontalpodautoscalers' },

  // RBAC (apis/rbac.authorization.k8s.io/v1)
  'k8s-clusterroles':         { apiBase: 'apis/rbac.authorization.k8s.io/v1', namespaced: false, plural: 'clusterroles' },
  'k8s-clusterrolebindings':  { apiBase: 'apis/rbac.authorization.k8s.io/v1', namespaced: false, plural: 'clusterrolebindings' },
  'k8s-roles':                { apiBase: 'apis/rbac.authorization.k8s.io/v1', namespaced: true,  plural: 'roles' },
  'k8s-rolebindings':         { apiBase: 'apis/rbac.authorization.k8s.io/v1', namespaced: true,  plural: 'rolebindings' },

  // Storage (apis/storage.k8s.io/v1)
  storageclasses:          { apiBase: 'apis/storage.k8s.io/v1', namespaced: false, plural: 'storageclasses' },

  // CRDs
  crds:                    { apiBase: 'apis/apiextensions.k8s.io/v1', namespaced: false, plural: 'customresourcedefinitions' },

  // Gateway API. Keep apiBase in sync with internal/handler/resources.go.
  gateways:        { apiBase: 'apis/gateway.networking.k8s.io/v1',       namespaced: true,  plural: 'gateways' },
  httproutes:      { apiBase: 'apis/gateway.networking.k8s.io/v1',       namespaced: true,  plural: 'httproutes' },
  gatewayclasses:  { apiBase: 'apis/gateway.networking.k8s.io/v1',       namespaced: false, plural: 'gatewayclasses' },
  grpcroutes:      { apiBase: 'apis/gateway.networking.k8s.io/v1',       namespaced: true,  plural: 'grpcroutes' },
  tlsroutes:       { apiBase: 'apis/gateway.networking.k8s.io/v1',       namespaced: true,  plural: 'tlsroutes' },
  referencegrants: { apiBase: 'apis/gateway.networking.k8s.io/v1',       namespaced: true,  plural: 'referencegrants' },
  tcproutes:       { apiBase: 'apis/gateway.networking.k8s.io/v1alpha2', namespaced: true,  plural: 'tcproutes' },
  udproutes:       { apiBase: 'apis/gateway.networking.k8s.io/v1alpha2', namespaced: true,  plural: 'udproutes' },
};

/**
 * Get the K8s API path for listing all resources of a type (optionally in a namespace).
 */
export function k8sListPath(resourceType: string, namespace?: string): string {
  const def = resourceDefs[resourceType];
  if (!def) throw new Error(`Unknown resource type: ${resourceType}`);

  if (def.namespaced && namespace) {
    return `${def.apiBase}/namespaces/${namespace}/${def.plural}`;
  }
  return `${def.apiBase}/${def.plural}`;
}

/**
 * Get the K8s API path for a specific named resource.
 */
export function k8sResourcePath(resourceType: string, name: string, namespace?: string): string {
  const def = resourceDefs[resourceType];
  if (!def) throw new Error(`Unknown resource type: ${resourceType}`);

  if (def.namespaced && namespace) {
    return `${def.apiBase}/namespaces/${namespace}/${def.plural}/${name}`;
  }
  return `${def.apiBase}/${def.plural}/${name}`;
}

/**
 * Check if a resource type is namespaced.
 */
export function isNamespaced(resourceType: string): boolean {
  return resourceDefs[resourceType]?.namespaced ?? false;
}

/**
 * Get the resource definition for a type.
 */
export function getResourceDef(resourceType: string): K8sResourceDef | undefined {
  return resourceDefs[resourceType];
}
