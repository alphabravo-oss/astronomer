import { createFileRoute, useNavigate } from "@tanstack/react-router";
import { useState, type ElementType } from "react";
import { FileText, Key, LifeBuoy, Settings } from "lucide-react";
import { useTabParam } from "@/lib/use-tab-param";
import { PageHeader, PageShell } from "@/components/ui/page";
import { TabStrip, TabsContent } from "@/components/ui/tabs";
import { InfoCallout } from "@/components/ui/info-callout";
import { useSSOProviders } from "@/lib/hooks/user-settings";
import { Link as RouterLink } from "@tanstack/react-router";
import { AuditTab } from "./-audit-tab";
import { GeneralEditModal } from "./-general-edit-modal";
import { GeneralTab } from "./-general-tab";
import { SupportTab } from "./-support-tab";
import { TokensTab } from "./-tokens-tab";

type TabKey = "general" | "tokens" | "audit" | "support";

const TAB_KEYS = ["general", "tokens", "audit", "support"] as const;

const tabs: { key: TabKey; label: string; icon: ElementType }[] = [
  { key: "general", label: "General", icon: Settings },
  { key: "tokens", label: "API tokens", icon: Key },
  { key: "audit", label: "Audit log", icon: FileText },
  { key: "support", label: "Support", icon: LifeBuoy },
];

function SettingsPage() {
  const navigate = useNavigate();
  const [activeTab, setActiveTab] = useTabParam(TAB_KEYS, "general");
  const [showEditGeneral, setShowEditGeneral] = useState(false);
  // The SSO signpost is a banner only while no provider is configured.
  const { data: ssoProviders } = useSSOProviders();
  const ssoUnconfigured =
    ssoProviders !== undefined &&
    !ssoProviders.some((provider) => provider.enabled);

  return (
    <PageShell>
      <PageHeader
        title="General"
        description="Platform identity, audit defaults, API tokens and support."
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
            {ssoUnconfigured ? (
              <InfoCallout
                action={
                  <RouterLink
                    to="/dashboard/settings/auth"
                    className="text-sm font-medium underline-offset-4 hover:underline"
                  >
                    Set up single sign-on
                  </RouterLink>
                }
              >
                No single sign-on provider is configured. Users sign in with
                local passwords until one is added under Authentication.
              </InfoCallout>
            ) : null}
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
