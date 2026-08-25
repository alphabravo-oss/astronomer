import type { DestructiveImpactPreview } from "@/components/ui/confirm-dialog";

interface ResourceIdentity {
  name: string;
  namespace?: string;
  kind?: string;
}

const DEFAULT_RECOVERY =
  "Recreate the object from Git or a known-good manifest. Runtime state is not restored automatically.";

const RESOURCE_KIND_LABELS: Record<string, string> = {
  namespaces: "Namespace",
  pods: "Pod",
  deployments: "Deployment",
  daemonsets: "DaemonSet",
  statefulsets: "StatefulSet",
  jobs: "Job",
  cronjobs: "CronJob",
  replicasets: "ReplicaSet",
  services: "Service",
  ingresses: "Ingress",
  gateways: "Gateway",
  gatewayclasses: "GatewayClass",
  httproutes: "HTTPRoute",
  grpcroutes: "GRPCRoute",
  tlsroutes: "TLSRoute",
  tcproutes: "TCPRoute",
  udproutes: "UDPRoute",
  referencegrants: "ReferenceGrant",
  networkpolicies: "NetworkPolicy",
  persistentvolumes: "PersistentVolume",
  persistentvolumeclaims: "PersistentVolumeClaim",
  secrets: "Secret",
  configmaps: "ConfigMap",
  serviceaccounts: "ServiceAccount",
  "k8s-roles": "Role",
  "k8s-clusterroles": "ClusterRole",
  "k8s-rolebindings": "RoleBinding",
  "k8s-clusterrolebindings": "ClusterRoleBinding",
  crds: "CustomResourceDefinition",
  hpa: "HorizontalPodAutoscaler",
  poddisruptionbudgets: "PodDisruptionBudget",
};

function scopeFor(resourceType: string, identity: ResourceIdentity): string {
  const kind =
    identity.kind ||
    RESOURCE_KIND_LABELS[resourceType] ||
    resourceType.replace(/s$/, "");
  const name = identity.namespace
    ? `${identity.namespace}/${identity.name}`
    : identity.name;
  return `${kind} ${name}`;
}

/**
 * Explain Kubernetes deletion semantics before an operator confirms an action.
 * This is deliberately conservative: reclaim policies, owner references, and
 * external controllers can differ per cluster, so unknown behavior is called
 * out instead of being presented as a guarantee.
 */
export function resourceDeletionImpact(
  resourceType: string,
  identity: ResourceIdentity | null | undefined,
): DestructiveImpactPreview | undefined {
  if (!identity) return undefined;

  const scope = scopeFor(resourceType, identity);

  switch (resourceType) {
    case "namespaces":
      return {
        scope,
        consequences: [
          "Kubernetes will cascade deletion to every namespaced object inside it.",
          "Workloads, services, secrets, policies, and claims in this namespace will become unavailable.",
          "Storage data may also be deleted, depending on each volume's reclaim policy.",
        ],
        recovery:
          "There is no in-place undo. Restore the namespace and its objects from Git or backup; storage recovery depends on the provider and reclaim policy.",
      };
    case "pods":
      return {
        scope,
        consequences: [
          "The running containers and their ephemeral data will stop.",
          "An owning workload controller may create a replacement pod with a different identity.",
          "Standalone pods are not recreated automatically.",
        ],
        recovery:
          "A controller-managed pod should reconcile; otherwise recreate it from a manifest. Ephemeral container data cannot be recovered.",
      };
    case "deployments":
    case "daemonsets":
    case "statefulsets":
    case "jobs":
    case "cronjobs":
    case "replicasets":
      return {
        scope,
        consequences: [
          "Managed pods will be terminated through Kubernetes deletion propagation.",
          "Traffic and scheduled or background processing provided by this workload may stop.",
          "Services and volumes are separate objects; their retention depends on ownership and storage policy.",
        ],
        recovery: DEFAULT_RECOVERY,
      };
    case "services":
      return {
        scope,
        consequences: [
          "The virtual IP, in-cluster DNS routing, and load-balancer association will be removed.",
          "Back-end pods remain running but clients can no longer reach them through this Service.",
          "A replacement load balancer may receive a different external address.",
        ],
        recovery: DEFAULT_RECOVERY,
      };
    case "ingresses":
    case "gateways":
    case "httproutes":
    case "grpcroutes":
    case "tlsroutes":
    case "tcproutes":
    case "udproutes":
      return {
        scope,
        consequences: [
          "Traffic configured by this object may stop routing immediately.",
          "Referenced back-end Services and workloads are not deleted.",
          "External DNS and load-balancer cleanup depends on the installed controller.",
        ],
        recovery: DEFAULT_RECOVERY,
      };
    case "gatewayclasses":
      return {
        scope,
        consequences: [
          "Gateways that reference this class may become unaccepted or stop reconciling.",
          "Existing data-plane behavior depends on the Gateway controller.",
          "Referenced Gateways and Routes are not deleted by this action alone.",
        ],
        recovery: DEFAULT_RECOVERY,
      };
    case "referencegrants":
      return {
        scope,
        consequences: [
          "Cross-namespace references authorized by this grant will be revoked.",
          "Affected Routes may become unresolved and stop forwarding traffic.",
          "Referenced Routes, Services, and Secrets are not deleted.",
        ],
        recovery: DEFAULT_RECOVERY,
      };
    case "networkpolicies":
      return {
        scope,
        consequences: [
          "The ingress or egress restrictions enforced by this policy will be removed.",
          "Selected pods remain running, but their effective network access may broaden.",
          "Other policies selecting the same pods continue to apply.",
        ],
        recovery:
          "Reapply the policy immediately from Git or a known-good manifest to restore its traffic controls.",
      };
    case "persistentvolumes":
      return {
        scope,
        consequences: [
          "Claims or workloads bound to this volume can lose access to storage.",
          "The backing disk or share may be retained or deleted according to its reclaim policy.",
          "Deleting the Kubernetes object does not guarantee that underlying data is recoverable.",
        ],
        recovery:
          "Verify the reclaim policy and provider snapshot before continuing. Object recreation alone may not restore the underlying data.",
      };
    case "persistentvolumeclaims":
      return {
        scope,
        consequences: [
          "Workloads mounting this claim can fail or lose storage access.",
          "The bound volume and underlying data may be retained or deleted according to reclaim and retention policies.",
          "Recreating a claim with the same name does not guarantee rebinding to the same data.",
        ],
        recovery:
          "Verify the backing volume's reclaim policy and snapshot state. Restore data through the storage provider if the volume is deleted.",
      };
    case "secrets":
      return {
        scope,
        consequences: [
          "New pods and controllers that read this Secret may fail authentication or startup.",
          "Existing processes may retain previously loaded values until they restart.",
          "Secret contents are not recoverable from this deletion flow.",
        ],
        recovery:
          "Recreate the Secret from the authoritative secret manager; do not rely on browser or audit history to recover its values.",
      };
    case "configmaps":
      return {
        scope,
        consequences: [
          "Pods and controllers that require this configuration may fail on restart or reconciliation.",
          "Mounted or cached values can remain visible to existing processes temporarily.",
          "Dependent objects are not deleted automatically unless an owner reference says otherwise.",
        ],
        recovery: DEFAULT_RECOVERY,
      };
    case "serviceaccounts":
    case "k8s-roles":
    case "k8s-clusterroles":
    case "k8s-rolebindings":
    case "k8s-clusterrolebindings":
      return {
        scope,
        consequences: [
          "Subjects or workloads that depend on this identity or grant may immediately lose API access.",
          "Running workloads remain, but authorization failures can break reconciliation and operations.",
          "Other identities and grants are not removed by this action alone.",
        ],
        recovery: DEFAULT_RECOVERY,
      };
    case "crds":
      return {
        scope,
        consequences: [
          "The custom API endpoint will be removed from the cluster.",
          "All custom resources stored for this definition may be deleted.",
          "Controllers and applications using this API can fail until the definition and objects are restored.",
        ],
        recovery:
          "Back up custom resources first. Reinstalling the definition alone does not restore deleted custom-resource data.",
      };
    case "hpa":
      return {
        scope,
        consequences: [
          "Automatic replica adjustment for the target workload will stop.",
          "The target workload and its current replica count remain unchanged.",
          "Capacity will no longer adapt to the configured metrics.",
        ],
        recovery: DEFAULT_RECOVERY,
      };
    case "poddisruptionbudgets":
      return {
        scope,
        consequences: [
          "Voluntary disruptions will no longer be limited by this budget.",
          "Selected pods and workloads remain running.",
          "Node drain and maintenance may evict more replicas concurrently.",
        ],
        recovery: DEFAULT_RECOVERY,
      };
    default:
      return {
        scope,
        consequences: [
          "The Kubernetes API object will be permanently deleted.",
          "Objects with owner references may be deleted through propagation.",
          "Controllers that depend on this object may report errors or recreate it.",
        ],
        recovery: DEFAULT_RECOVERY,
      };
  }
}

export function nodeDrainImpact(
  name: string | undefined,
): DestructiveImpactPreview | undefined {
  if (!name) return undefined;
  return {
    scope: `Node ${name}`,
    consequences: [
      "The node will be cordoned and stop accepting newly scheduled pods.",
      "Evictable non-DaemonSet pods will be terminated and rescheduled where capacity and policy allow.",
      "PodDisruptionBudgets, local storage, or unhealthy workloads can delay or block eviction.",
    ],
    recovery:
      "Uncordon the node to make it schedulable again. Evicted pods keep their replacement identities and ephemeral data is not restored.",
  };
}
