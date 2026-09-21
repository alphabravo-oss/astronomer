import { createFileRoute } from "@tanstack/react-router";
import { Loader2, Sparkles } from "lucide-react";
import { useNavigate, useLocation } from "@tanstack/react-router";
import { useFeatureFlags } from "@/lib/hooks/clusters";
import { useAuthStore } from "@/lib/store";
import { PermissionState, StatePanel } from "@/components/ui/empty-state";
import { ResourceMasthead } from "@/components/ui/page";
import { TabStrip } from "@/components/ui/tabs";
import {
  CHARLIE_ADMIN_TABS,
  canManageCharlie,
  mergeCharlieSearch,
  normalizeCharlieAdminTab,
  type CharlieAdminTab,
} from "@/components/charlie/admin-utils";
import { ConnectionTab } from "@/components/charlie/settings/connection-tab";
import { AgentTab } from "@/components/charlie/settings/agent-tab";
import { ModeTab } from "@/components/charlie/settings/mode-tab";
import { KubernetesTab } from "@/components/charlie/settings/kubernetes-tab";
import { AlertsTab } from "@/components/charlie/settings/alerts-tab";
import { AutomationTab } from "@/components/charlie/settings/automation-tab";
import { AccessTab } from "@/components/charlie/settings/access-tab";
import { DiagnosticsTab } from "@/components/charlie/settings/diagnostics-tab";
import { Unavailable } from "@/components/charlie/settings/shared";

export { AgentTab } from "@/components/charlie/settings/agent-tab";
export {
  charlieModeWorkReady,
  ModeTab,
} from "@/components/charlie/settings/mode-tab";
export { KubernetesTab } from "@/components/charlie/settings/kubernetes-tab";
export { ConnectionTab } from "@/components/charlie/settings/connection-tab";
export { AlertsTab } from "@/components/charlie/settings/alerts-tab";
export { AutomationTab } from "@/components/charlie/settings/automation-tab";
export { AccessTab } from "@/components/charlie/settings/access-tab";
export { DiagnosticsTab } from "@/components/charlie/settings/diagnostics-tab";

export const Route = createFileRoute("/dashboard/settings/charlie/")({
  component: CharlieAdminPage,
});

const tabLabels: Record<CharlieAdminTab, string> = {
  connection: "Connection",
  agent: "Agent",
  mode: "Mode",
  kubernetes: "Kubernetes",
  alerts: "Alerts",
  automation: "Automation",
  access: "Access",
  diagnostics: "Diagnostics",
};

function CharlieAdminPage() {
  return <CharlieAdminContent />;
}

export function CharlieAdminContent() {
  const flags = useFeatureFlags();
  const user = useAuthStore((s) => s.user);
  const navigate = useNavigate();
  const params = new URLSearchParams(
    useLocation({ select: (location) => location.searchStr }),
  );
  const requestedTab = normalizeCharlieAdminTab(params.get("tab"));

  if (flags.isError)
    return (
      <Unavailable
        name="Charlie feature state"
        retry={() => void flags.refetch()}
      />
    );
  if (
    flags.data?.["feature.charlie"] !== true &&
    flags.data?.["feature.charlie"] !== false
  )
    return (
      <StatePanel
        icon={Loader2}
        iconClassName="animate-spin motion-reduce:animate-none"
        title="Loading Charlie settings"
      />
    );
  if (!canManageCharlie(user))
    return (
      <PermissionState
        title="Charlie administration restricted"
        description="Requires the global charlie:manage permission. Read and approval permissions do not grant configuration access."
      />
    );
  const featureEnabled = flags.data?.["feature.charlie"] === true;
  const activeTabs: readonly CharlieAdminTab[] = featureEnabled
    ? CHARLIE_ADMIN_TABS
    : ["connection", "diagnostics"];
  const tab = activeTabs.includes(requestedTab) ? requestedTab : "connection";
  const select = (next: CharlieAdminTab) =>
    void navigate({
      to: `/dashboard/settings/charlie?${mergeCharlieSearch(params, { tab: next })}`,
    });
  return (
    <div className="space-y-6">
      <ResourceMasthead
        backTo="/dashboard/settings"
        backLabel="Back to settings"
        title="Charlie"
        description="Connect and govern the external Charlie service and its Astronomer product agent."
      />
      {!featureEnabled && (
        <StatePanel
          icon={Sparkles}
          tone="warning"
          title="Charlie is disabled"
          description="Only locally stored connection metadata and network-quiesced diagnostics are available. Astronomer makes no product-agent or Charlie central request until an administrator explicitly enables the feature."
        />
      )}
      <TabStrip
        tabs={activeTabs.map((value) => ({
          key: value,
          label: tabLabels[value],
        }))}
        value={tab}
        onChange={select}
        aria-label="Charlie administration"
      />
      <div
        id={`charlie-admin-panel-${tab}`}
        role="tabpanel"
        tabIndex={0}
        aria-labelledby={`tab-${tab}`}
      >
        {tab === "connection" && <ConnectionTab localOnly={!featureEnabled} />}
        {tab === "agent" && <AgentTab />}
        {tab === "mode" && <ModeTab />}
        {tab === "kubernetes" && <KubernetesTab />}
        {tab === "alerts" && <AlertsTab />}
        {tab === "automation" && <AutomationTab />}
        {tab === "access" && <AccessTab />}
        {tab === "diagnostics" && <DiagnosticsTab />}
      </div>
    </div>
  );
}
