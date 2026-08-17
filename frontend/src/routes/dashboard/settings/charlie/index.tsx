import { createFileRoute } from "@tanstack/react-router";
import { type KeyboardEvent } from "react";
import {
  ArrowLeft,
  Loader2,
  Sparkles,
} from "lucide-react";
import { Link } from "@/lib/link";
import { useRouter, useSearchParams } from "@/lib/navigation";
import { useFeatureFlags } from "@/lib/hooks";
import { useAuthStore } from "@/lib/store";
import { cn } from "@/lib/utils";
import {
  PermissionState,
  StatePanel,
} from "@/components/ui/empty-state";
import {
  CHARLIE_ADMIN_TABS,
  adjacentTab,
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
export { charlieModeWorkReady, ModeTab } from "@/components/charlie/settings/mode-tab";
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
  const router = useRouter();
  const params = useSearchParams();
  const requestedTab = normalizeCharlieAdminTab(params.get("tab"));

  if (flags.isError)
    return (
      <Unavailable
        name="Charlie feature state"
        retry={() => void flags.refetch()}
      />
    );
  if (flags.data?.["feature.charlie"] !== true && flags.data?.["feature.charlie"] !== false)
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
    router.push(`/dashboard/settings/charlie?${mergeCharlieSearch(params, { tab: next })}`);
  const onTabKey = (event: KeyboardEvent<HTMLButtonElement>) => {
    const next = adjacentTab(activeTabs, tab, event.key);
    if (!next) return;
    event.preventDefault();
    select(next);
    document.getElementById(`charlie-admin-tab-${next}`)?.focus();
  };
  return (
    <div className="space-y-6">
      <div className="flex items-start gap-3">
        <Link
          href="/dashboard/settings"
          aria-label="Back to settings"
          className="mt-1 rounded p-1 text-muted-foreground hover:bg-accent"
        >
          <ArrowLeft className="h-4 w-4" />
        </Link>
        <div>
          <h1 className="text-2xl font-semibold">Charlie</h1>
          <p className="mt-1 text-sm text-muted-foreground">
            Connect and govern the external Charlie service and its Astronomer
            product agent.
          </p>
        </div>
      </div>
      {!featureEnabled && (
        <StatePanel
          icon={Sparkles}
          tone="warning"
          title="Charlie is disabled"
          description="Only locally stored connection metadata and network-quiesced diagnostics are available. Astronomer makes no product-agent or Charlie central request until an administrator explicitly enables the feature."
        />
      )}
      <div
        role="tablist"
        aria-label="Charlie administration"
        className="flex overflow-x-auto border-b border-border"
      >
        {activeTabs.map((value) => (
          <button
            key={value}
            id={`charlie-admin-tab-${value}`}
            type="button"
            role="tab"
            aria-selected={tab === value}
            aria-controls={`charlie-admin-panel-${value}`}
            tabIndex={tab === value ? 0 : -1}
            onKeyDown={onTabKey}
            onClick={() => select(value)}
            className={cn(
              "min-h-11 border-b-2 px-4 text-sm",
              tab === value
                ? "border-primary text-foreground"
                : "border-transparent text-muted-foreground hover:text-foreground",
            )}
          >
            {tabLabels[value]}
          </button>
        ))}
      </div>
      <div
        id={`charlie-admin-panel-${tab}`}
        role="tabpanel"
        tabIndex={0}
        aria-labelledby={`charlie-admin-tab-${tab}`}
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
