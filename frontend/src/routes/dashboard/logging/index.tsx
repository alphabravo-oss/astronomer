import { createFileRoute } from '@tanstack/react-router';
import { useState } from 'react';
import type { ElementType } from 'react';
import { useTabParam } from '@/lib/use-tab-param';
import { PageHeader, PageShell } from '@/components/ui/page';
import { TabStrip, TabsContent } from '@/components/ui/tabs';
import { ActionButton } from '@/components/ui/action-button';
import { Database, Activity, Plus } from 'lucide-react';
import { OutputsTab } from './-outputs-tab';
import { OperationsTab } from './-operations-tab';
import { CreateOutputModal } from './-output-modal';

type TabKey = 'outputs' | 'operations';

const TAB_KEYS = ['outputs', 'operations'] as const;

const tabs: { key: TabKey; label: string; icon: ElementType }[] = [
  { key: 'outputs', label: 'Destinations', icon: Database },
  { key: 'operations', label: 'Operations', icon: Activity },
];

function LoggingPage() {
  const [activeTab, setActiveTab] = useTabParam(TAB_KEYS, 'outputs');
  const [showOutputModal, setShowOutputModal] = useState(false);

  return (
    <PageShell>
      <PageHeader
        title="Logging"
        description="Shared log destinations. Pipelines are configured on each cluster."
        actions={
          activeTab === 'outputs' ? (
            <ActionButton
              intent="primary"
              icon={<Plus className="h-4 w-4" />}
              onClick={() => setShowOutputModal(true)}
            >
              Create Destination
            </ActionButton>
          ) : undefined
        }
      />

      <TabStrip tabs={tabs} value={activeTab} onChange={setActiveTab} />

      <TabsContent>
        {activeTab === 'outputs' && <OutputsTab />}
        {activeTab === 'operations' && <OperationsTab />}
      </TabsContent>

      {showOutputModal && <CreateOutputModal onClose={() => setShowOutputModal(false)} />}
    </PageShell>
  );
}

export const Route = createFileRoute('/dashboard/logging/')({
  // ?tab= deep-link (P2.4): typed passthrough — useTabParam's allowlist stays the real validator.
  validateSearch: (search: Record<string, unknown>) =>
    search as { tab?: string } & Record<string, unknown>,
  component: LoggingPage,
});
