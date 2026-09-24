import { ExternalLink, LockKeyhole } from "lucide-react";
import { Link } from "@tanstack/react-router";
import { DataTable } from "@/components/ui/data-table";
import { PageSection } from "@/components/ui/page";
import {
  DeliveryPhaseBadge,
  Detail,
  DetailGrid,
} from "@/components/delivery/shared";
import type { DeliverySystemComponent } from "@/lib/api/delivery-system";
import { formatBytes, formatRelativeTime } from "@/lib/utils";
import { replicaRedundancy } from "@/lib/system-component-availability";
import { systemResourceColumns, systemVolumeColumns } from "./-columns";

function ComponentSummary({
  component,
}: {
  component: DeliverySystemComponent;
}) {
  return (
    <DetailGrid>
      <Detail
        label="Health"
        value={<DeliveryPhaseBadge value={component.health} />}
      />
      <Detail label="Owner" value={component.owner} />
      <Detail label="Management method" value={component.managementMethod} />
      <Detail label="Category" value={component.category} />
      <Detail label="Kind" value={component.kind} />
      <Detail
        label="Namespace"
        value={component.namespace || "Cluster scoped"}
      />
      <Detail label="Version" value={component.version || "Unknown"} mono />
      <Detail
        label="Compatibility"
        value={
          <DeliveryPhaseBadge value={component.compatibility || "unknown"} />
        }
      />
      <Detail
        label="Update state"
        value={
          <DeliveryPhaseBadge value={component.updateState || "unknown"} />
        }
      />
      <Detail
        label="Age"
        value={
          component.createdAt
            ? formatRelativeTime(component.createdAt)
            : "Unknown"
        }
      />
      <Detail
        label="Replicas"
        value={
          component.desiredReplicas
            ? component.readyReplicas + "/" + component.desiredReplicas
            : "Not applicable"
        }
      />
      <Detail
        label="Replica redundancy (not a topology check)"
        value={replicaRedundancy(component)}
      />
      <Detail
        label="Storage class"
        value={component.storageClass || "Not applicable"}
      />
      <Detail
        label="Storage driver"
        value={component.storageDriver || "Not applicable"}
      />
      <Detail
        label="Default storage"
        value={component.defaultStorage ? "Yes" : "No"}
      />
      <Detail
        label="Provisioned storage"
        value={
          component.storageProvisionedBytes
            ? formatBytes(component.storageProvisionedBytes)
            : "Not reported"
        }
      />
      <Detail
        label="Used storage"
        value={
          component.storageUsedBytes
            ? formatBytes(component.storageUsedBytes)
            : "Not reported"
        }
      />
      <Detail
        label="Storage replicas"
        value={component.storageReplicaCount || "Not reported"}
      />
    </DetailGrid>
  );
}

function ComponentOperations({
  component,
  clusterId,
  workloadHref,
}: {
  component: DeliverySystemComponent;
  clusterId: string;
  workloadHref: string;
}) {
  return (
    <div className="grid gap-6 xl:grid-cols-2">
      <PageSection
        title="Resource posture"
        description="Aggregate requests and limits across desired workload replicas."
      >
        <DetailGrid>
          <Detail label="CPU request" value={component.cpuRequest || "Unset"} />
          <Detail label="CPU limit" value={component.cpuLimit || "Unset"} />
          <Detail
            label="Memory request"
            value={component.memoryRequest || "Unset"}
          />
          <Detail
            label="Memory limit"
            value={component.memoryLimit || "Unset"}
          />
        </DetailGrid>
      </PageSection>
      <PageSection
        title="Lifecycle ownership"
        description="Actions are intentionally limited by the component's authoritative owner."
      >
        {(component.supportedActions ?? []).length ? (
          <div className="flex flex-wrap gap-2">
            {(component.supportedActions ?? []).map((action) => (
              <span
                key={action}
                className="rounded-full border border-border px-2 py-1 text-xs font-medium capitalize"
              >
                {action}
              </span>
            ))}
          </div>
        ) : (
          <div className="flex items-start gap-2 text-sm text-muted-foreground">
            <LockKeyhole className="mt-0.5 h-4 w-4" />
            Cluster- and externally owned components are view-only.
          </div>
        )}
        {workloadHref ? (
          <Link
            to={workloadHref}
            className="mt-4 inline-flex items-center gap-1.5 text-sm font-medium text-link hover:underline"
          >
            Open workload
            <ExternalLink className="h-3.5 w-3.5" />
          </Link>
        ) : null}
        {component.namespace ? (
          <div className="mt-4 flex flex-wrap gap-3 text-sm">
            <Link
              to="/dashboard/clusters/$id/$resource/$"
              params={{
                id: clusterId,
                resource: "namespaces",
                _splat: component.namespace,
              }}
              className="font-medium text-link hover:underline"
            >
              Namespace
            </Link>
            <Link
              to="/dashboard/clusters/$id/$resource"
              params={{ id: clusterId, resource: "pods" }}
              search={{ namespace: component.namespace }}
              className="font-medium text-link hover:underline"
            >
              Pods and logs
            </Link>
            <Link
              to="/dashboard/clusters/$id/$resource"
              params={{ id: clusterId, resource: "persistentvolumeclaims" }}
              search={{ namespace: component.namespace }}
              className="font-medium text-link hover:underline"
            >
              Storage
            </Link>
            <Link
              to="/dashboard/clusters/$id/$resource"
              params={{ id: clusterId, resource: "events" }}
              search={{ namespace: component.namespace }}
              className="font-medium text-link hover:underline"
            >
              Events
            </Link>
          </div>
        ) : null}
      </PageSection>
    </div>
  );
}

function ComponentEvidence({
  component,
  clusterId,
}: {
  component: DeliverySystemComponent;
  clusterId: string;
}) {
  const volumeColumns = systemVolumeColumns(clusterId);
  const resourceColumns = systemResourceColumns(clusterId);
  return (
    <>
      {(component.volumes ?? []).length ? (
        <PageSection
          title="Persistent volume claims"
          description="Live Kubernetes claim capacity, binding, expansion, and VolumeSnapshot evidence. Capacity is not filesystem utilization."
        >
          <DataTable
            data={component.volumes ?? []}
            columns={volumeColumns}
            keyExtractor={(row) => row.namespace + "/" + row.name}
            searchable
            searchPlaceholder="Search claims, namespaces, classes, or drivers…"
            pageSize={25}
            persistKey={
              "delivery-system-component-volumes:" +
              clusterId +
              ":" +
              component.id
            }
            resizable
          />
        </PageSection>
      ) : null}
      {(component.resources ?? []).length ? (
        <PageSection
          title="Related resources"
          description="Operator-owned objects behind this aggregate observation. Open a resource for its complete spec, status, events, and YAML."
        >
          <DataTable
            data={component.resources ?? []}
            columns={resourceColumns}
            keyExtractor={(row) =>
              row.group +
              "/" +
              row.version +
              "/" +
              row.namespace +
              "/" +
              row.name
            }
            searchable
            searchPlaceholder="Search related resources…"
            pageSize={25}
            persistKey={
              "delivery-system-component-resources:" +
              clusterId +
              ":" +
              component.id
            }
            resizable
          />
        </PageSection>
      ) : null}
      <PageSection
        title="Images"
        description="Observed image references; no registry credentials are collected."
      >
        {(component.images ?? []).length ? (
          <div className="space-y-2">
            {(component.images ?? []).map((image) => (
              <code
                key={image}
                className="block overflow-x-auto rounded-md border border-border bg-muted/30 p-3 text-xs"
              >
                {image}
              </code>
            ))}
          </div>
        ) : (
          <p className="text-sm text-muted-foreground">
            No container image applies to this component.
          </p>
        )}
      </PageSection>
    </>
  );
}

export function SystemComponentContent({
  component,
  clusterId,
  workloadHref,
}: {
  component: DeliverySystemComponent;
  clusterId: string;
  workloadHref: string;
}) {
  return (
    <>
      <ComponentSummary component={component} />
      <ComponentOperations
        component={component}
        clusterId={clusterId}
        workloadHref={workloadHref}
      />
      <ComponentEvidence component={component} clusterId={clusterId} />
    </>
  );
}
