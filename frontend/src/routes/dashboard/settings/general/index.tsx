import { createFileRoute } from '@tanstack/react-router';
import { useState, type ElementType } from 'react';
import { FileText, Key, LifeBuoy, Settings, Shield } from 'lucide-react';
import { useTabParam } from '@/lib/use-tab-param';
import { PageHeader, PageShell } from '@/components/ui/page';
import { TabStrip, TabsContent } from '@/components/ui/tabs';
import { AuditTab } from './-audit-tab';
import { GeneralEditModal } from './-general-edit-modal';
import { GeneralTab } from './-general-tab';
import { SSOModal } from './-sso-modal';
import { SSOTab } from './-sso-tab';
import { SupportTab } from './-support-tab';
import { TokenModal } from './-token-modal';
import { TokensTab } from './-tokens-tab';

type TabKey = 'sso' | 'general' | 'tokens' | 'audit' | 'support';

const TAB_KEYS = ['sso', 'general', 'tokens', 'audit', 'support'] as const;

const tabs: { key: TabKey; label: string; icon: ElementType }[] = [
  { key: 'sso', label: 'SSO Providers', icon: Shield },
  { key: 'general', label: 'General', icon: Settings },
  { key: 'tokens', label: 'API Tokens', icon: Key },
  { key: 'audit', label: 'Audit Log', icon: FileText },
  { key: 'support', label: 'Support', icon: LifeBuoy },
];

function SettingsPage() {
  const [activeTab, setActiveTab] = useTabParam(TAB_KEYS, 'sso');
  const [showCreateToken, setShowCreateToken] = useState(false);
  const [showAddSSO, setShowAddSSO] = useState(false);
  const [showEditGeneral, setShowEditGeneral] = useState(false);

  return (
    <PageShell>
      <PageHeader title="Settings" description="Platform configuration and administration" />

      <div className="overflow-x-auto">
        <TabStrip tabs={tabs} value={activeTab} onChange={setActiveTab} className="min-w-max" />
      </div>

      <TabsContent>
        {activeTab === 'sso' && <SSOTab onAdd={() => setShowAddSSO(true)} />}
        {activeTab === 'general' && <GeneralTab onEdit={() => setShowEditGeneral(true)} />}
        {activeTab === 'tokens' && <TokensTab onCreate={() => setShowCreateToken(true)} />}
        {activeTab === 'audit' && <AuditTab />}
        {activeTab === 'support' && <SupportTab />}
      </TabsContent>

      {showEditGeneral && <GeneralEditModal onClose={() => setShowEditGeneral(false)} />}
      {showAddSSO && <SSOModal onClose={() => setShowAddSSO(false)} />}
      {showCreateToken && <TokenModal onClose={() => setShowCreateToken(false)} />}
    </PageShell>
  );
}

export const Route = createFileRoute('/dashboard/settings/general/')({
  // ?tab= deep-link (P2.4): typed passthrough — useTabParam's allowlist stays the real validator.
  validateSearch: (search: Record<string, unknown>) =>
    search as { tab?: string } & Record<string, unknown>,
  component: SettingsPage,
});
