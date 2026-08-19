import { createFileRoute } from '@tanstack/react-router';
import type { ElementType } from 'react';
import { useState } from 'react';
import { AlertTriangle, Plus, Shield } from 'lucide-react';
import { useParams } from '@/lib/navigation';
import { useTabParam } from '@/lib/use-tab-param';
import { Link } from '@/lib/link';
import { ActionButton } from '@/components/ui/action-button';
import { PageHeader, PageShell } from '@/components/ui/page';
import { TabStrip, TabsContent } from '@/components/ui/tabs';
import { useCluster } from '@/lib/hooks';
import type { AlertRule } from '@/types';
import { RulesTab } from '@/routes/dashboard/alerting/-rules-tab';
import { EventsTab } from '@/routes/dashboard/alerting/-events-tab';
import { AlertRuleModal } from '@/routes/dashboard/alerting/-rule-modal';

type TabKey = 'rules' | 'active';

const TAB_KEYS = ['rules', 'active'] as const;

const tabs: { key: TabKey; label: string; icon: ElementType }[] = [
  { key: 'rules', label: 'Alert Rules', icon: Shield },
  { key: 'active', label: 'Active Alerts', icon: AlertTriangle },
];

function ClusterAlertingPage() {
  const params = useParams();
  const clusterId = params.id as string;
  const { data: cluster } = useCluster(clusterId);
  const [activeTab, setActiveTab] = useTabParam(TAB_KEYS, 'rules');
  const [showRuleModal, setShowRuleModal] = useState(false);
  const [editingRule, setEditingRule] = useState<AlertRule | null>(null);

  return (
    <PageShell>
      <PageHeader
        title="Alerting"
        description={`Rules and firing alerts for ${cluster?.displayName || cluster?.name || 'this cluster'}`}
        actions={
          <div className="flex items-center gap-2">
            <Link
              href="/dashboard/alerting"
              className="inline-flex h-9 items-center justify-center rounded-md border border-border bg-background px-3 text-sm font-medium text-foreground transition-colors hover:bg-accent"
            >
              Fleet inbox
            </Link>
            {activeTab === 'rules' ? (
              <ActionButton
                intent="primary"
                icon={<Plus className="h-4 w-4" />}
                onClick={() => { setEditingRule(null); setShowRuleModal(true); }}
              >
                Create Rule
              </ActionButton>
            ) : null}
          </div>
        }
      />

      <TabStrip tabs={tabs} value={activeTab} onChange={setActiveTab} />

      <TabsContent>
        {activeTab === 'rules' && (
          <RulesTab
            clusterId={clusterId}
            onEdit={(rule) => { setEditingRule(rule); setShowRuleModal(true); }}
          />
        )}
        {activeTab === 'active' && <EventsTab clusterId={clusterId} />}
      </TabsContent>

      {showRuleModal && (
        <AlertRuleModal
          rule={editingRule}
          clusterId={clusterId}
          onClose={() => { setShowRuleModal(false); setEditingRule(null); }}
        />
      )}
    </PageShell>
  );
}

export const Route = createFileRoute('/dashboard/clusters/$id/alerting/')({
  validateSearch: (search: Record<string, unknown>) =>
    search as { tab?: string } & Record<string, unknown>,
  component: ClusterAlertingPage,
});
