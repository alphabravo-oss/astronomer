import { AlertTriangle, ChevronLeft, ChevronRight, Rocket } from "lucide-react";
import { DataTable, type Column } from "@/components/ui/data-table";
import { PageSection } from "@/components/ui/page";
import { ActionButton } from "@/components/ui/action-button";
import { MetricCard } from "@/components/ui/metric-card";
import { DeliveryPhaseBadge } from "@/components/delivery/shared";
import type { PlacementDecision, PlacementPreview } from "@/lib/api/delivery-targets";

const columns: Column<PlacementDecision>[] = [
  {
    key: "cluster",
    header: "Cluster",
    accessor: (row) => (
      <div>
        <p className="font-medium">{row.clusterName || row.clusterId}</p>
        <p className="font-mono text-xs text-muted-foreground">
          {row.clusterId}
        </p>
      </div>
    ),
  },
  {
    key: "decision",
    header: "Decision",
    accessor: (row) => <DeliveryPhaseBadge value={row.reason} />,
  },
  {
    key: "reason",
    header: "Details",
    accessor: (row) =>
      row.missingCapabilities?.join(", ") ||
      row.compatibilityReason ||
      row.matchReasons?.join(", ") ||
      "Eligible",
  },
];

export function PreviewPanel({
  preview,
  canLaunch,
  onLaunch,
  pageIndex,
  loadingPage,
  canGoBack,
  onPrevious,
  onNext,
}: {
  preview: PlacementPreview;
  canLaunch: boolean;
  onLaunch: () => void;
  pageIndex: number;
  loadingPage: boolean;
  canGoBack: boolean;
  onPrevious: () => void;
  onNext: () => void;
}) {
  return (
    <PageSection
      title="Authoritative placement preview"
      description="This is the server-evaluated, project-scoped membership snapshot. Launch is bound to its digest."
      actions={
        <ActionButton
          disabled={!canLaunch || preview.selectedCount === 0}
          onClick={onLaunch}
          intent="primary"
          icon={<Rocket className="h-4 w-4" />}
        >
          Launch rollout
        </ActionButton>
      }
    >
      <div className="grid gap-3 sm:grid-cols-4">
        <MetricCard dense label="Selected" value={preview.selectedCount} />
        <MetricCard dense label="Excluded / blocked" value={preview.excludedCount} />
        <MetricCard dense label="Target generation" value={preview.targetGeneration} />
        <MetricCard
          dense
          label="All-cluster confirmation"
          value={preview.requiresAllConfirmation ? "Required" : "No"}
        />
      </div>
      {preview.risks.length > 0 && (
        <div
          role="alert"
          className="rounded-md border border-status-warning/30 bg-status-warning/10 p-3 text-sm text-status-warning"
        >
          <p className="flex items-center gap-2 font-medium">
            <AlertTriangle className="h-4 w-4" /> Review before launch
          </p>
          <ul className="mt-2 list-disc space-y-1 pl-5">
            {preview.risks.map((risk) => (
              <li key={risk}>{risk.replaceAll("_", " ")}</li>
            ))}
          </ul>
        </div>
      )}
      <p className="font-mono text-xs text-muted-foreground">
        Preview digest: {preview.previewDigest}
      </p>
      <DataTable
        data={preview.decisions}
        columns={columns}
        keyExtractor={(row) => row.clusterId}
        searchable={false}
        pageSize={Math.max(preview.decisions.length, 1)}
        emptyState={{
          title: "No clusters were evaluated",
          description:
            "Resources will appear here when they are available in this scope.",
        }}
      />
      <div
        className="flex flex-col gap-3 border-t border-border pt-3 text-sm sm:flex-row sm:items-center sm:justify-between"
        aria-live="polite"
      >
        <p className="text-muted-foreground">
          {preview.decisionCount === 0
            ? "No placement decisions"
            : `Showing ${preview.decisionOffset + 1}–${preview.decisionOffset + preview.decisions.length} of ${preview.decisionCount} decisions`}
        </p>
        <div className="flex items-center gap-2">
          <ActionButton
            disabled={!canGoBack || loadingPage}
            onClick={onPrevious}
            aria-label="Previous placement decision page"
            icon={<ChevronLeft className="h-4 w-4" />}
          >
            Previous
          </ActionButton>
          <span className="min-w-16 text-center text-xs text-muted-foreground">
            Page {pageIndex + 1}
          </span>
          <ActionButton
            disabled={
              !preview.hasMoreDecisions || !preview.nextCursor || loadingPage
            }
            onClick={onNext}
            aria-label="Next placement decision page"
          >
            Next <ChevronRight className="h-4 w-4" />
          </ActionButton>
        </div>
      </div>
    </PageSection>
  );
}
