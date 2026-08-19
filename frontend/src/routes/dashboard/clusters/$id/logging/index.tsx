import { createFileRoute } from '@tanstack/react-router';
import { useState } from 'react';
import { Plus } from 'lucide-react';
import { useParams } from '@/lib/navigation';
import { Link } from '@/lib/link';
import { ActionButton } from '@/components/ui/action-button';
import { PageHeader, PageShell } from '@/components/ui/page';
import { useCluster } from '@/lib/hooks';
import { PipelinesTab } from '@/routes/dashboard/logging/-pipelines-tab';
import { CreatePipelineModal } from '@/routes/dashboard/logging/-pipeline-modal';

function ClusterLoggingPage() {
  const params = useParams();
  const clusterId = params.id as string;
  const { data: cluster } = useCluster(clusterId);
  const [showPipelineModal, setShowPipelineModal] = useState(false);

  return (
    <PageShell>
      <PageHeader
        title="Logging"
        description={`Log pipelines for ${cluster?.displayName || cluster?.name || 'this cluster'}`}
        actions={
          <div className="flex items-center gap-2">
            <Link
              href="/dashboard/logging"
              className="inline-flex h-9 items-center justify-center rounded-md border border-border bg-background px-3 text-sm font-medium text-foreground transition-colors hover:bg-accent"
            >
              Destinations
            </Link>
            <ActionButton
              intent="primary"
              icon={<Plus className="h-4 w-4" />}
              onClick={() => setShowPipelineModal(true)}
            >
              Create Pipeline
            </ActionButton>
          </div>
        }
      />

      <PipelinesTab clusterId={clusterId} />

      {showPipelineModal && (
        <CreatePipelineModal
          clusterId={clusterId}
          onClose={() => setShowPipelineModal(false)}
        />
      )}
    </PageShell>
  );
}

export const Route = createFileRoute('/dashboard/clusters/$id/logging/')({
  component: ClusterLoggingPage,
});
