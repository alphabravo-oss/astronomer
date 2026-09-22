import { createFileRoute, useNavigate } from "@tanstack/react-router";
import { useState, type ElementType } from "react";
import { FileText, Key, LifeBuoy, Settings } from "lucide-react";
import { useTabParam } from "@/lib/use-tab-param";
import { PageHeader, PageShell } from "@/components/ui/page";
import { TabStrip, TabsContent } from "@/components/ui/tabs";
import { StatePanel } from "@/components/ui/empty-state";
import { AuditTab } from "./-audit-tab";
import { GeneralEditModal } from "./-general-edit-modal";
import { GeneralTab } from "./-general-tab";
import { SupportTab } from "./-support-tab";
import { TokensTab } from "./-tokens-tab";

type TabKey = "general" | "tokens" | "audit" | "support";

const TAB_KEYS = ["general", "tokens", "audit", "support"] as const;

const tabs: { key: TabKey; label: string; icon: ElementType }[] = [
  { key: "general", label: "General", icon: Settings },
  { key: "tokens", label: "API Tokens", icon: Key },
  { key: "audit", label: "Audit Log", icon: FileText },
  { key: "support", label: "Support", icon: LifeBuoy },
];

function SettingsPage() {
  const navigate = useNavigate();
  const [activeTab, setActiveTab] = useTabParam(TAB_KEYS, "general");
  const [showEditGeneral, setShowEditGeneral] = useState(false);

  return (
    <PageShell>
      <PageHeader
        title="General"
        description="Platform configuration and administration"
      />

      <div className="overflow-x-auto">
        <TabStrip
          tabs={tabs}
          value={activeTab}
          onChange={setActiveTab}
          className="min-w-max"
        />
      </div>

      <TabsContent>
        {activeTab === "general" && (
          <div className="space-y-4">
            <StatePanel
              tone="info"
              title="Looking for SSO providers?"
              description="Single sign-on providers are configured under Settings › Authentication."
              actionLabel="Go to Authentication"
              actionHref="/dashboard/settings/auth"
              className="items-start py-4 text-left"
            />
            <GeneralTab onEdit={() => setShowEditGeneral(true)} />
          </div>
        )}
        {activeTab === "tokens" && (
          <TokensTab
            onCreate={() =>
              void navigate({
                to: "/dashboard/settings/general/tokens/new",
              })
            }
          />
        )}
        {activeTab === "audit" && <AuditTab />}
        {activeTab === "support" && <SupportTab />}
      </TabsContent>

      {showEditGeneral && (
        <GeneralEditModal onClose={() => setShowEditGeneral(false)} />
      )}
    </PageShell>
  );
}

export const Route = createFileRoute("/dashboard/settings/general/")({
  // ?tab= deep-link (P2.4): typed passthrough — useTabParam's allowlist stays the real validator.
  validateSearch: (search: Record<string, unknown>) =>
    search as { tab?: string } & Record<string, unknown>,
  component: SettingsPage,
});
