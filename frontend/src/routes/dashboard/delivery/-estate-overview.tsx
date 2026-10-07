import { clusterHref, useEstateFocus } from "./-estate-focus";
import { estateColumns as columns } from "./-estate-columns";
import {
  EstateKpiGrid,
  EstateZeroState,
  isEstateEmpty,
} from "./-estate-summary";
import { type UseQueryResult } from "@tanstack/react-query";
import { AlertTriangle, X } from "lucide-react";
import { Link as RouterLink } from "@tanstack/react-router";
import { DataTable } from "@/components/ui/data-table";
import { PageSection, PageShell } from "@/components/ui/page";
import { DeliveryPhaseBadge, ErrorMessage } from "@/components/delivery/shared";
import { EstateHeader } from "@/components/delivery/estate-header";
import {
  type DeliveryEstate,
  type DeliveryEstateAttention,
  type DeliveryEstateCount,
} from "@/lib/api/delivery-system";
import { cn } from "@/lib/utils";
import { ActionButton } from "@/components/ui/action-button";

export function isForbiddenError(error: unknown): boolean {
  return Boolean(
    error &&
    typeof error === "object" &&
    "response" in error &&
    (error as { response?: { status?: number } }).response?.status === 403,
  );
}

const estateFocusLabels: Record<string, string> = {
  adopted: "Adopted clusters",
  flux_ready: "Flux-ready clusters",
  incompatible: "Incompatible clusters",
  disconnected: "Disconnected clusters",
  assignments: "Clusters with assignments",
  failed: "Clusters with failed assignments",
  drifted: "Clusters with drift",
};

export function EstateDeliveryOverview({
  query,
}: {
  query: UseQueryResult<DeliveryEstate>;
}) {
  const summary = query.data?.summary;
  const { focus, visible, setFocus, navigate } = useEstateFocus(
    query.data?.clusters ?? [],
  );

  return (
    <PageShell>
      <EstateHeader />
      {query.isError && !isForbiddenError(query.error) && (
        <ErrorMessage error={query.error} />
      )}
      {isEstateEmpty(query.data) ? (
        <EstateZeroState />
      ) : (
        <EstateKpiGrid summary={summary} focus={focus} setFocus={setFocus} />
      )}
      <PageSection
        title="Needs attention"
        description="Disconnected agents, failed assignments, incompatible controllers, drift, and stale inventory."
      >
        <AttentionList
          items={query.data?.attention ?? []}
          loading={query.isLoading}
        />
      </PageSection>
      <PageSection
        title="Distributions"
        description="Adopted clusters only. Click a value to filter the table."
      >
        <div className="grid gap-4 md:grid-cols-3">
          <DistributionList
            title="Compatibility"
            items={query.data?.distributions.compatibility ?? []}
            activeKey={
              focus.startsWith("compatibility:")
                ? focus.slice("compatibility:".length)
                : ""
            }
            onSelect={(key) => setFocus(`compatibility:${key}`)}
          />
          <DistributionList
            title="Privilege"
            items={query.data?.distributions.privilege ?? []}
            activeKey={
              focus.startsWith("privilege:")
                ? focus.slice("privilege:".length)
                : ""
            }
            onSelect={(key) => setFocus(`privilege:${key}`)}
          />
          <DistributionList
            title="Assignment phases"
            items={query.data?.distributions.assignmentPhases ?? []}
            activeKey={
              focus.startsWith("phase:") ? focus.slice("phase:".length) : ""
            }
            onSelect={(key) => setFocus(`phase:${key}`)}
          />
        </div>
      </PageSection>
      <div id="estate-clusters">
        <PageSection
          title="Clusters"
          description="Click a row to open that cluster's Flux workspace."
          actions={
            focus ? (
              <ActionButton
                intent="bare"
                size="none"
                onClick={() => setFocus("")}
                className="inline-flex h-8 items-center gap-1 rounded-md border border-border px-2 text-xs text-muted-foreground hover:bg-accent"
              >
                <X className="h-3 w-3" />
                {estateFocusLabels[focus] ?? focus.replaceAll("_", " ")}
              </ActionButton>
            ) : null
          }
        >
          <DataTable
            data={visible}
            columns={columns}
            keyExtractor={(row) => row.id}
            loading={query.isLoading}
            isError={query.isError && !isForbiddenError(query.error)}
            onRetry={() => void query.refetch()}
            onRowClick={(row) => void navigate({ to: clusterHref(row.id) })}
            filtersActive={!!focus}
            onClearFilters={() => setFocus("")}
            emptyState={{
              title: "No clusters registered",
              description:
                "Register a cluster to inspect its delivery readiness.",
              action: {
                label: "Register cluster",
                href: "/dashboard/clusters/register",
              },
            }}
          />
        </PageSection>
      </div>
    </PageShell>
  );
}

function AttentionList({
  items,
  loading,
}: {
  items: DeliveryEstateAttention[];
  loading: boolean;
}) {
  if (!loading && items.length === 0) {
    return (
      <div className="rounded-lg border border-border bg-card p-6 text-sm text-muted-foreground">
        No adopted clusters need attention.
      </div>
    );
  }
  return (
    <div className="space-y-2">
      {items.map((item) => (
        <RouterLink
          key={`${item.clusterId}-${item.reason}`}
          to={clusterHref(item.clusterId)}
          className={
            item.severity === "error"
              ? "flex items-center justify-between rounded-md border border-status-error/30 bg-status-error/10 p-3"
              : "flex items-center justify-between rounded-md border border-status-warning/30 bg-status-warning/10 p-3"
          }
        >
          <span className="flex items-center gap-2">
            <AlertTriangle
              className={
                item.severity === "error"
                  ? "h-4 w-4 text-status-error"
                  : "h-4 w-4 text-status-warning"
              }
            />
            <span>
              <strong>{item.clusterName}</strong> — {item.detail}
            </span>
          </span>
          <DeliveryPhaseBadge value={item.reason} />
        </RouterLink>
      ))}
    </div>
  );
}

function DistributionList({
  title,
  items,
  activeKey,
  onSelect,
}: {
  title: string;
  items: DeliveryEstateCount[];
  activeKey: string;
  onSelect: (key: string) => void;
}) {
  return (
    <div className="rounded-lg border border-border bg-card p-4">
      <h3 className="text-sm font-medium text-foreground">{title}</h3>
      {items.length === 0 ? (
        <p className="mt-3 text-sm text-muted-foreground">
          No adopted clusters.
        </p>
      ) : (
        <ul className="mt-3 space-y-2" aria-label={title}>
          {items.map((item) => (
            <li key={item.key}>
              <ActionButton
                intent="bare"
                size="none"
                onClick={() => onSelect(item.key)}
                className={cn(
                  "flex w-full items-center justify-between rounded-md px-1 py-1 text-sm font-normal hover:bg-accent",
                  activeKey === item.key && "bg-accent",
                )}
              >
                <DeliveryPhaseBadge value={item.key} />
                <span className="tabular-nums text-muted-foreground">
                  {item.count}
                </span>
              </ActionButton>
            </li>
          ))}
        </ul>
      )}
    </div>
  );
}
