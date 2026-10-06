import type { ReactNode } from "react";
import {
  AlertTriangle,
  GitBranch,
  Layers,
  Radio,
  Rocket,
  ServerCog,
  Unplug,
} from "lucide-react";
import { EmptyState } from "@/components/ui/empty-state";
import type {
  DeliveryEstate,
  DeliveryEstateSummary,
} from "@/lib/api/delivery-system";
import { cn } from "@/lib/utils";
import { ActionButton } from "@/components/ui/action-button";

/** True when nothing is adopted and every summary counter is zero. */
export function isEstateEmpty(estate: DeliveryEstate | undefined): boolean {
  if (!estate?.summary || estate.clusters.length > 0) return false;
  return Object.values(estate.summary).every(
    (value) => typeof value !== "number" || value === 0,
  );
}

export function EstateZeroState() {
  return (
    <EmptyState
      icon={Radio}
      title="No clusters adopted yet"
      description="Adopt a cluster to see Flux readiness, assignments, and drift here."
      actionLabel="Adopt a cluster"
      actionHref="/dashboard/clusters/register"
    />
  );
}

export function EstateTile({
  title,
  value,
  icon,
  active,
  onClick,
}: {
  title: string;
  value: string | number;
  icon: ReactNode;
  active?: boolean;
  onClick: () => void;
}) {
  return (
    <ActionButton
      intent="bare"
      size="none"
      onClick={onClick}
      className={cn(
        "flex items-start justify-between whitespace-normal rounded-lg border border-border bg-card p-4 text-left font-normal transition-colors hover:bg-accent/40 focus:outline-hidden focus:ring-2 focus:ring-ring",
        active && "ring-2 ring-ring",
      )}
    >
      <div className="min-w-0">
        <p className="text-xs font-medium text-muted-foreground">{title}</p>
        <p className="mt-1 text-2xl font-semibold tabular-nums tracking-tight text-foreground">
          {value}
        </p>
      </div>
      <div className="rounded-md bg-muted p-2 text-muted-foreground">
        {icon}
      </div>
    </ActionButton>
  );
}

export function EstateKpiGrid({
  summary,
  focus,
  setFocus,
}: {
  summary: DeliveryEstateSummary | undefined;
  focus: string;
  setFocus: (focus: string) => void;
}) {
  const group = (
    title: string,
    tiles: Array<[string, ReactNode, number | undefined, string]>,
  ) => (
    <div className="space-y-2">
      <h2 className="text-sm font-semibold text-foreground">{title}</h2>
      <div className="grid grid-cols-2 gap-3">
        {tiles.map(([label, icon, value, key]) => (
          <EstateTile
            key={label}
            icon={icon}
            title={label}
            value={value ?? "—"}
            active={focus === key}
            onClick={() => setFocus(key)}
          />
        ))}
      </div>
    </div>
  );
  const icon = "h-4 w-4";
  return (
    <div className="grid gap-(--gap-section) lg:grid-cols-2">
      {group("Cluster health", [
        [
          "Adopted",
          <Radio key="i" className={icon} />,
          summary?.managedClusters,
          "adopted",
        ],
        [
          "Flux ready",
          <ServerCog key="i" className={icon} />,
          summary?.fluxReady,
          "flux_ready",
        ],
        [
          "Incompatible",
          <AlertTriangle key="i" className={icon} />,
          summary?.incompatible,
          "incompatible",
        ],
        [
          "Disconnected",
          <Unplug key="i" className={icon} />,
          summary?.disconnected,
          "disconnected",
        ],
      ])}
      {group("Assignments", [
        [
          "Assigned",
          <Layers key="i" className={icon} />,
          summary?.assignments,
          "assignments",
        ],
        [
          "Failed",
          <AlertTriangle key="i" className={icon} />,
          summary?.failed,
          "failed",
        ],
        [
          "Drifted",
          <GitBranch key="i" className={icon} />,
          summary?.drifted,
          "drifted",
        ],
        [
          "Active rollouts",
          <Rocket key="i" className={icon} />,
          summary?.activeRollouts,
          "assignments",
        ],
      ])}
    </div>
  );
}
