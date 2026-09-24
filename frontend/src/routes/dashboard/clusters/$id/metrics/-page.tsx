import { ClusterMetricsPage } from "@/components/monitoring/cluster-metrics-page";
import { ClusterGrafanaView } from "@/components/monitoring/cluster-grafana-view";
import { TabStrip } from "@/components/ui/tabs";
import { useTabParam } from "@/lib/use-tab-param";

export function ClusterMetricsWorkspace({ clusterId }: { clusterId: string }) {
  const [view, setView] = useTabParam(
    ["overview", "grafana"],
    "overview",
    "view",
  );
  return (
    <div className="space-y-6">
      <TabStrip
        aria-label="Metrics views"
        tabs={[
          { key: "overview", label: "Overview" },
          { key: "grafana", label: "Grafana" },
        ]}
        value={view}
        onChange={setView}
      />
      {view === "overview" ? (
        <ClusterMetricsPage clusterId={clusterId} />
      ) : (
        <ClusterGrafanaView clusterId={clusterId} view="metrics" />
      )}
    </div>
  );
}
