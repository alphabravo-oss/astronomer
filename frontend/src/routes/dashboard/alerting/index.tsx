import { createFileRoute } from '@tanstack/react-router';
import type { ElementType } from 'react';
import { useState } from 'react';
import { AlertTriangle, Ban, Bell, Plus, VolumeX } from 'lucide-react';
import { useTabParam } from '@/lib/use-tab-param';
import { Link } from '@/lib/link';
import { ActionButton } from '@/components/ui/action-button';
import { PageHeader, PageShell } from '@/components/ui/page';
import { TabStrip, TabsContent } from '@/components/ui/tabs';
import { InhibitionPanel } from './-inhibition-panel';
import { EventsTab } from './-events-tab';
import { ChannelsTab } from './-channels-tab';
import { SilencesTab } from './-silences-tab';
import { NotificationChannelModal } from './-channel-modal';
import { SilenceModal } from './-silence-modal';

type TabKey = 'active' | 'channels' | 'silences' | 'inhibitions';

const TAB_KEYS = ['active', 'channels', 'silences', 'inhibitions'] as const;

const tabs: { key: TabKey; label: string; icon: ElementType }[] = [
  { key: 'active', label: 'Active Alerts', icon: AlertTriangle },
  { key: 'channels', label: 'Notification Channels', icon: Bell },
  { key: 'silences', label: 'Silences', icon: VolumeX },
  { key: 'inhibitions', label: 'Inhibitions', icon: Ban },
];

function AlertingPage() {
  const [activeTab, setActiveTab] = useTabParam(TAB_KEYS, 'active');
  const [showChannelModal, setShowChannelModal] = useState(false);
  const [showSilenceModal, setShowSilenceModal] = useState(false);

  return (
    <PageShell>
      <PageHeader
        title="Alerting"
        description="Fleet inbox and routing. Alert rules are defined on each cluster."
        actions={
          <>
            <Link
              href="/dashboard/alerting/baselines"
              className="inline-flex h-9 items-center justify-center gap-2 rounded-md border border-border bg-background px-3 text-sm font-medium text-foreground transition-colors hover:bg-accent"
            >
              Anomaly Baselines
            </Link>
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
        {activeTab === 'active' && <EventsTab />}
        {activeTab === 'channels' && <ChannelsTab />}
        {activeTab === 'silences' && <SilencesTab />}
        {activeTab === 'inhibitions' && <InhibitionPanel />}
      </TabsContent>

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
