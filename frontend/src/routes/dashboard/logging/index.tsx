import { createFileRoute } from '@tanstack/react-router';
import { useState } from 'react';
import type { ElementType } from 'react';
import { useTabParam } from '@/lib/use-tab-param';
import { PageHeader, PageShell } from '@/components/ui/page';
import { TabStrip, TabsContent } from '@/components/ui/tabs';
import { ActionButton } from '@/components/ui/action-button';
import { Database, GitBranch, Activity, Plus } from 'lucide-react';
import { OutputsTab } from './-outputs-tab';
import { PipelinesTab } from './-pipelines-tab';
import { OperationsTab } from './-operations-tab';
import { CreateOutputModal } from './-output-modal';
import { CreatePipelineModal } from './-pipeline-modal';

type TabKey = 'outputs' | 'pipelines' | 'operations';

const TAB_KEYS = ['outputs', 'pipelines', 'operations'] as const;

const tabs: { key: TabKey; label: string; icon: ElementType }[] = [
  { key: 'outputs', label: 'Outputs', icon: Database },
  { key: 'pipelines', label: 'Pipelines', icon: GitBranch },
  { key: 'operations', label: 'Operations', icon: Activity },
];

function LoggingPage() {
  const [activeTab, setActiveTab] = useTabParam(TAB_KEYS, 'outputs');
  const [showOutputModal, setShowOutputModal] = useState(false);
  const [showPipelineModal, setShowPipelineModal] = useState(false);

  return (
    <PageShell>
      <PageHeader
        title="Logging"
        description="Logging outputs and pipeline configuration"
        actions={
          activeTab === 'outputs' ? (
            <ActionButton
              intent="primary"
              icon={<Plus className="h-4 w-4" />}
              onClick={() => setShowOutputModal(true)}
            >
              Create Output
            </ActionButton>
          ) : activeTab === 'pipelines' ? (
            <ActionButton
              intent="primary"
              icon={<Plus className="h-4 w-4" />}
              onClick={() => setShowPipelineModal(true)}
            >
              Create Pipeline
            </ActionButton>
          ) : undefined
        }
      />

      <TabStrip tabs={tabs} value={activeTab} onChange={setActiveTab} />

      <TabsContent>
        {activeTab === 'outputs' && <OutputsTab />}
        {activeTab === 'pipelines' && <PipelinesTab />}
        {activeTab === 'operations' && <OperationsTab />}
      </TabsContent>

      {showOutputModal && <CreateOutputModal onClose={() => setShowOutputModal(false)} />}
      {showPipelineModal && <CreatePipelineModal onClose={() => setShowPipelineModal(false)} />}
    </PageShell>
  );
}

export const Route = createFileRoute('/dashboard/logging/')({
  // ?tab= deep-link (P2.4): typed passthrough — useTabParam's allowlist stays the real validator.
  validateSearch: (search: Record<string, unknown>) =>
    search as { tab?: string } & Record<string, unknown>,
  component: LoggingPage,
});
