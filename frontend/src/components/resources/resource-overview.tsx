import { formatRelativeTime } from "@/lib/utils";
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from "@/components/ui/operator-table";
import type { K8sObject } from "@/components/resources/resource-detail-model";
import { asRecord } from "@/components/resources/resource-detail-model";
import {
  KeyValueTable,
  Section,
} from "@/components/resources/resource-overview-primitives";
import {
  CRDOverview,
  GatewayClassOverview,
  GatewayOverview,
  NetworkPolicyOverview,
  PDBOverview,
  PVOverview,
  ResourceQuotaOverview,
  RoleBindingOverview,
  RoleOverview,
  RouteOverview,
  ServiceAccountOverview,
  StorageClassOverview,
} from "@/components/resources/resource-overview-additional";
import {
  ConfigMapOverview,
  CronJobOverview,
  HPAOverview,
  IngressOverview,
  JobOverview,
  PVCOverview,
  ReplicaSetOverview,
  ServiceOverview,
  WorkloadOverview,
} from "@/components/resources/resource-overview-kinds";
import { PodResourceOverview } from "@/components/resources/pod-resource-overview";
import { SecretDataOverview } from "@/components/resources/secret-data-overview";
import { OverviewUnavailable } from "@/components/resources/overview-unavailable";
import { LabelsAnnotationsEditor } from "@/components/resources/labels-annotations-editor";

export function ResourceOverview({
  obj,
  resourceType,
  clusterId = "",
  namespace,
  name,
}: {
  obj?: K8sObject;
  resourceType: string;
  clusterId?: string;
  namespace?: string;
  name?: string;
}) {
  const meta = obj?.metadata;
  if (!meta) {
    return <OverviewUnavailable resourceType={resourceType} />;
  }

  // ponytail: small per-kind branches keyed by resourceType for the few
  // highest-value kinds; everything else falls through to the generic view.
  // Each branch renders its tailored section ABOVE the shared GenericOverview.
  const kindSpecific = (() => {
    switch (resourceType) {
      case "pods":
        return (
          <PodResourceOverview
            obj={obj!}
            clusterId={clusterId}
            namespace={namespace ?? meta.namespace ?? ""}
            name={name ?? meta.name ?? ""}
          />
        );
      case "services":
        return <ServiceOverview obj={obj!} />;
      case "configmaps":
        return <ConfigMapOverview obj={obj!} />;
      case "ingresses":
        return <IngressOverview obj={obj!} />;
      case "persistentvolumeclaims":
        return <PVCOverview obj={obj!} />;
      case "deployments":
      case "statefulsets":
      case "daemonsets":
        return <WorkloadOverview obj={obj!} />;
      case "replicasets":
        return <ReplicaSetOverview obj={obj!} />;
      case "jobs":
        return <JobOverview obj={obj!} />;
      case "cronjobs":
        return <CronJobOverview obj={obj!} />;
      case "hpa":
        return <HPAOverview obj={obj!} />;
      case "secrets":
        return <SecretDataOverview obj={obj!} clusterId={clusterId} />;
      case "persistentvolumes":
        return <PVOverview obj={obj!} />;
      case "networkpolicies":
        return <NetworkPolicyOverview obj={obj!} />;
      case "storageclasses":
        return <StorageClassOverview obj={obj!} />;
      case "k8s-clusterroles":
      case "k8s-roles":
        return <RoleOverview obj={obj!} />;
      case "k8s-clusterrolebindings":
      case "k8s-rolebindings":
        return <RoleBindingOverview obj={obj!} />;
      case "serviceaccounts":
        return <ServiceAccountOverview obj={obj!} />;
      case "poddisruptionbudgets":
        return <PDBOverview obj={obj!} />;
      case "resourcequotas":
        return <ResourceQuotaOverview obj={obj!} />;
      case "crds":
        return <CRDOverview obj={obj!} />;
      case "gateways":
        return <GatewayOverview obj={obj!} />;
      case "gatewayclasses":
        return <GatewayClassOverview obj={obj!} />;
      case "httproutes":
      case "grpcroutes":
      case "tlsroutes":
      case "tcproutes":
      case "udproutes":
        return <RouteOverview obj={obj!} />;
      default:
        return null;
    }
  })();

  const resolvedNamespace = namespace ?? meta.namespace;
  const resolvedName = name ?? meta.name ?? "";

  if (kindSpecific) {
    return (
      <div className="space-y-(--gap-section)">
        {kindSpecific}
        {/* Tailored overview already summarises status; skip the generic dump. */}
        <GenericOverview
          obj={obj}
          resourceType={resourceType}
          clusterId={clusterId}
          namespace={resolvedNamespace}
          name={resolvedName}
          showStatus={false}
          showData={resourceType !== "secrets"}
        />
      </div>
    );
  }

  return (
    <GenericOverview
      obj={obj}
      resourceType={resourceType}
      clusterId={clusterId}
      namespace={resolvedNamespace}
      name={resolvedName}
      showStatus
    />
  );
}
function GenericOverview({
  obj,
  resourceType,
  clusterId,
  namespace,
  name,
  showStatus,
  showData = true,
}: {
  obj?: K8sObject;
  resourceType: string;
  clusterId: string;
  namespace?: string;
  name: string;
  showStatus?: boolean;
  showData?: boolean;
}) {
  const meta = obj?.metadata;
  if (!meta) {
    return <OverviewUnavailable resourceType={resourceType} />;
  }

  // Top-level spec scalars — surfaces .spec for arbitrary CRs / unmapped kinds
  // that have no tailored overview. Skip nested objects/arrays and the fields
  // that kind overviews / actions already handle (replicas/paused/suspend).
  const SPEC_SKIP = new Set(["replicas", "paused", "suspend"]);
  const specEntries = showStatus
    ? Object.entries(asRecord(obj?.spec))
        .filter(
          ([k, v]) =>
            !SPEC_SKIP.has(k) &&
            (typeof v === "string" ||
              typeof v === "number" ||
              typeof v === "boolean"),
        )
        .map(([k, v]) => [k, String(v)] as [string, string])
    : [];

  // Top-level status scalars — surfaces .status for arbitrary CRs / unmapped
  // kinds that have no tailored overview. Conditions live in their own tab.
  const statusEntries = showStatus
    ? Object.entries(asRecord(obj?.status))
        .filter(
          ([k, v]) =>
            k !== "conditions" &&
            (typeof v === "string" ||
              typeof v === "number" ||
              typeof v === "boolean"),
        )
        .map(([k, v]) => [k, String(v)] as [string, string])
    : [];

  const metadataEntries: Array<[string, string]> = [];
  if (meta.name) metadataEntries.push(["name", meta.name]);
  if (meta.namespace) metadataEntries.push(["namespace", meta.namespace]);
  if (meta.uid) metadataEntries.push(["uid", meta.uid]);
  if (meta.creationTimestamp) {
    metadataEntries.push(["created", meta.creationTimestamp]);
    metadataEntries.push(["age", formatRelativeTime(meta.creationTimestamp)]);
  }

  const owners = meta.ownerReferences ?? [];

  // ponytail: mask secret 'data' values; only secrets carries this.
  const isSecret = resourceType === "secrets";
  const dataEntries =
    showData && obj?.data
      ? (Object.entries(obj.data) as Array<[string, string]>)
      : [];

  return (
    <div className="space-y-(--gap-section)">
      <Section title="Metadata">
        <KeyValueTable entries={metadataEntries} />
      </Section>

      <LabelsAnnotationsEditor
        clusterId={clusterId}
        resourceType={resourceType}
        namespace={namespace}
        name={name}
        labels={meta.labels ?? {}}
        annotations={meta.annotations ?? {}}
      />

      {owners.length > 0 && (
        <Section title="Owner References">
          <Table>
            <TableHeader>
              <TableRow>
                <TableHead>Kind</TableHead>
                <TableHead>Name</TableHead>
              </TableRow>
            </TableHeader>
            <TableBody>
              {owners.map((ref) => (
                <TableRow key={ref.uid || `${ref.kind}/${ref.name}`}>
                  <TableCell className="text-xs">{ref.kind}</TableCell>
                  <TableCell className="font-mono text-xs">
                    {ref.name}
                  </TableCell>
                </TableRow>
              ))}
            </TableBody>
          </Table>
        </Section>
      )}

      {/* Conditions moved to a dedicated tab (ConditionsTab) — Rancher-style. */}

      {specEntries.length > 0 && (
        <Section title="Spec">
          <KeyValueTable entries={specEntries} />
        </Section>
      )}

      {statusEntries.length > 0 && (
        <Section title="Status">
          <KeyValueTable entries={statusEntries} />
        </Section>
      )}

      {dataEntries.length > 0 && (
        <Section title="Data">
          <KeyValueTable entries={dataEntries} mask={isSecret} />
        </Section>
      )}
    </div>
  );
}
