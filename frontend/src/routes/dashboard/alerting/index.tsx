import { createFileRoute } from '@tanstack/react-router';
import type { ElementType } from 'react';
import { useState } from 'react';
import { AlertTriangle, Ban, Bell, Plus, Shield, VolumeX } from 'lucide-react';
import { useTabParam } from '@/lib/use-tab-param';
import { Link } from '@/lib/link';
import { ActionButton } from '@/components/ui/action-button';
import { PageHeader, PageShell } from '@/components/ui/page';
import { TabStrip, TabsContent } from '@/components/ui/tabs';
import type { AlertRule } from '@/types';
import { InhibitionPanel } from './-inhibition-panel';
import { RulesTab } from './-rules-tab';
import { EventsTab } from './-events-tab';
import { ChannelsTab } from './-channels-tab';
import { SilencesTab } from './-silences-tab';
import { AlertRuleModal } from './-rule-modal';
import { NotificationChannelModal } from './-channel-modal';
import { SilenceModal } from './-silence-modal';

type TabKey = 'rules' | 'active' | 'channels' | 'silences' | 'inhibitions';

const TAB_KEYS = ['rules', 'active', 'channels', 'silences', 'inhibitions'] as const;

const tabs: { key: TabKey; label: string; icon: ElementType }[] = [
  { key: 'rules', label: 'Alert Rules', icon: Shield },
  { key: 'active', label: 'Active Alerts', icon: AlertTriangle },
  { key: 'channels', label: 'Notification Channels', icon: Bell },
  { key: 'silences', label: 'Silences', icon: VolumeX },
  { key: 'inhibitions', label: 'Inhibitions', icon: Ban },
];

function AlertingPage() {
  const [activeTab, setActiveTab] = useTabParam(TAB_KEYS, 'rules');
  const [showRuleModal, setShowRuleModal] = useState(false);
  const [editingRule, setEditingRule] = useState<AlertRule | null>(null);
  const [showChannelModal, setShowChannelModal] = useState(false);
  const [showSilenceModal, setShowSilenceModal] = useState(false);

  return (
    <PageShell>
      <PageHeader
        title="Alerting"
        description="Alert rules, notifications, and silence management"
        actions={
          <>
            <Link
              href="/dashboard/alerting/baselines"
              className="inline-flex h-9 items-center justify-center gap-2 rounded-md border border-border bg-background px-3 text-sm font-medium text-foreground transition-colors hover:bg-accent"
            >
              Anomaly Baselines
            </Link>
            {activeTab === 'rules' && (
              <ActionButton
                intent="primary"
                icon={<Plus className="h-4 w-4" />}
                onClick={() => { setEditingRule(null); setShowRuleModal(true); }}
              >
                Create Rule
              </ActionButton>
            )}
            {activeTab === 'channels' && (
              <ActionButton
                intent="primary"
                icon={<Plus className="h-4 w-4" />}
                onClick={() => setShowChannelModal(true)}
              >
                Add Channel
              </ActionButton>
            )}
            {activeTab === 'silences' && (
              <ActionButton
                intent="primary"
                icon={<Plus className="h-4 w-4" />}
                onClick={() => setShowSilenceModal(true)}
              >
                Create Silence
              </ActionButton>
            )}
          </>
        }
      />

      <TabStrip tabs={tabs} value={activeTab} onChange={setActiveTab} />

      <TabsContent>
        {activeTab === 'rules' && (
          <RulesTab
            onEdit={(rule) => { setEditingRule(rule); setShowRuleModal(true); }}
          />
        )}
        {activeTab === 'active' && <EventsTab />}
        {activeTab === 'channels' && <ChannelsTab />}
        {activeTab === 'silences' && <SilencesTab />}
        {activeTab === 'inhibitions' && <InhibitionPanel />}
      </TabsContent>

      {showRuleModal && (
        <AlertRuleModal
          rule={editingRule}
          onClose={() => { setShowRuleModal(false); setEditingRule(null); }}
        />
      )}
      {showChannelModal && (
        <NotificationChannelModal onClose={() => setShowChannelModal(false)} />
      )}
      {showSilenceModal && (
        <SilenceModal onClose={() => setShowSilenceModal(false)} />
      )}
    </PageShell>
  );
}

export const Route = createFileRoute('/dashboard/alerting/')({
  // ?tab= deep-link (P2.4): typed passthrough — useTabParam's allowlist stays the real validator.
  validateSearch: (search: Record<string, unknown>) =>
    search as { tab?: string } & Record<string, unknown>,
  component: AlertingPage,
});
