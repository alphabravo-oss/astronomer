import {
  explainPermission,
  type PermissionDecision,
  type PermissionScope,
  type PermissionVerb,
} from '@/lib/permissions';
import { useAuthStore } from '@/lib/store';

export function usePermissionDecision(
  resource: string,
  verb: PermissionVerb | '*',
  scope: PermissionScope = { type: 'global' }
): PermissionDecision {
  const user = useAuthStore((state) => state.user);
  return explainPermission(user, resource, verb, scope);
}

/**
 * Map a K8s resource type (as used in the cluster browser routes) to the
 * canonical permission resource the RBAC layer gates on. Shared by the resource
 * list page and the generic ResourceDetail so client gating matches.
 */
export function canonicalPermissionResource(resourceType: string): string {
  switch (resourceType.toLowerCase()) {
    case 'services':
    case 'service':
    case 'endpoints':
    case 'endpoint':
      return 'services';
    case 'ingresses':
    case 'ingress':
    case 'gateways':
    case 'gateway':
    case 'httproutes':
    case 'httproute':
    case 'gatewayclasses':
    case 'gatewayclass':
    case 'grpcroutes':
    case 'grpcroute':
    case 'tcproutes':
    case 'tcproute':
    case 'udproutes':
    case 'udproute':
    case 'tlsroutes':
    case 'tlsroute':
    case 'referencegrants':
    case 'referencegrant':
      return 'ingresses';
    case 'networkpolicies':
    case 'networkpolicy':
      return 'network_policies';
    case 'persistentvolumes':
    case 'persistentvolume':
    case 'persistentvolumeclaims':
    case 'persistentvolumeclaim':
    case 'storageclasses':
    case 'storageclass':
      return 'storage';
    case 'configmaps':
    case 'configmap':
      return 'configmaps';
    case 'secrets':
    case 'secret':
      return 'secrets';
    case 'pods':
    case 'pod':
      return 'pods';
    case 'nodes':
    case 'node':
      return 'nodes';
    case 'deployments':
    case 'deployment':
    case 'daemonsets':
    case 'daemonset':
    case 'statefulsets':
    case 'statefulset':
    case 'replicasets':
    case 'replicaset':
    case 'jobs':
    case 'job':
    case 'cronjobs':
    case 'cronjob':
    case 'hpa':
    case 'horizontalpodautoscalers':
    case 'horizontalpodautoscaler':
    case 'poddisruptionbudgets':
    case 'poddisruptionbudget':
      return 'workloads';
    default:
      return 'clusters';
  }
}

/**
 * Permission decision for a single verb against a resource type's canonical
 * permission resource, scoped to a cluster. ponytail: ResourceDetail only needs
 * read/update, so it calls this twice rather than the full list-page bundle.
 */
export function useClusterResourcePermission(
  clusterId: string,
  resourceType: string,
  verb: PermissionVerb | '*',
): PermissionDecision {
  return usePermissionDecision(canonicalPermissionResource(resourceType), verb, {
    type: 'cluster',
    id: clusterId,
  });
}
