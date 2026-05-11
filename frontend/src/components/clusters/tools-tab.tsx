'use client';

import { useState } from 'react';
import { useTools, useClusterToolsStatus, useInstallTool, useUninstallTool, useAdoptTool } from '@/lib/hooks';
import { ToolCard } from '@/components/clusters/tool-card';
import { ToolPreviewModal } from '@/components/clusters/tool-preview-modal';
import { ConfirmDialog } from '@/components/ui/confirm-dialog';
import { Loader2, Wrench } from 'lucide-react';

interface ToolsTabProps {
  clusterId: string;
  clusterEnvironment: string;
  clusterStatus?: string;
}

export function ToolsTab({ clusterId, clusterEnvironment, clusterStatus }: ToolsTabProps) {
  const isDisconnected = clusterStatus === 'disconnected';
  const { data: tools, isLoading: toolsLoading } = useTools();
  const { data: statuses } = useClusterToolsStatus(clusterId);

  const installMutation = useInstallTool();
  const uninstallMutation = useUninstallTool();
  const adoptMutation = useAdoptTool();

  const [previewTool, setPreviewTool] = useState<{ slug: string; name: string; preset: string } | null>(null);
  const [uninstallSlug, setUninstallSlug] = useState<string | null>(null);

  const statusMap = new Map<string, (typeof statuses extends (infer T)[] | undefined ? T : never)>();
  statuses?.forEach((s) => statusMap.set(s.slug, s));

  const defaultPreset = ['production', 'staging', 'development'].includes(clusterEnvironment)
    ? clusterEnvironment
    : 'development';

  const handleInstall = (slug: string, preset: string) => {
    const tool = tools?.find((t) => t.slug === slug);
    if (tool) {
      setPreviewTool({ slug, name: tool.name, preset });
    }
  };

  const handleConfirmInstall = (valuesOverride?: string) => {
    if (!previewTool) return;
    installMutation.mutate(
      {
        slug: previewTool.slug,
        cluster_id: clusterId,
        preset: previewTool.preset,
        values_override: valuesOverride,
      },
      {
        onSuccess: () => setPreviewTool(null),
      }
    );
  };

  const handleUninstall = (slug: string) => {
    setUninstallSlug(slug);
  };

  const handleConfirmUninstall = () => {
    if (!uninstallSlug) return;
    uninstallMutation.mutate(
      { slug: uninstallSlug, cluster_id: clusterId },
      {
        onSuccess: () => setUninstallSlug(null),
      }
    );
  };

  const handleAdopt = (slug: string, releaseName: string) => {
    adoptMutation.mutate({ slug, cluster_id: clusterId, release_name: releaseName });
  };

  if (toolsLoading) {
    return (
      <div className="flex items-center justify-center h-48">
        <Loader2 className="h-6 w-6 animate-spin text-muted-foreground" />
      </div>
    );
  }

  if (!tools || tools.length === 0) {
    return (
      <div className="flex flex-col items-center justify-center py-16 text-muted-foreground">
        <Wrench className="h-10 w-10 mb-3" />
        <p className="text-sm">No tools available</p>
      </div>
    );
  }

  const uninstallTool = tools.find((t) => t.slug === uninstallSlug);

  return (
    <>
      <div className="grid grid-cols-1 sm:grid-cols-2 xl:grid-cols-3 gap-4">
        {tools.map((tool) => (
          <ToolCard
            key={tool.slug}
            tool={tool}
            toolStatus={statusMap.get(tool.slug)}
            defaultPreset={defaultPreset}
            onInstall={handleInstall}
            onUninstall={handleUninstall}
            onAdopt={handleAdopt}
            installing={installMutation.isPending && installMutation.variables?.slug === tool.slug}
            uninstalling={uninstallMutation.isPending && uninstallMutation.variables?.slug === tool.slug}
            clusterDisconnected={isDisconnected}
          />
        ))}
      </div>

      {/* Preview Modal */}
      {previewTool && (
        <ToolPreviewModal
          toolSlug={previewTool.slug}
          toolName={previewTool.name}
          clusterId={clusterId}
          preset={previewTool.preset}
          onConfirm={handleConfirmInstall}
          onClose={() => setPreviewTool(null)}
          installing={installMutation.isPending}
        />
      )}

      {/* Uninstall Confirmation */}
      <ConfirmDialog
        open={!!uninstallSlug}
        onClose={() => setUninstallSlug(null)}
        onConfirm={handleConfirmUninstall}
        title="Disable Tool"
        description={`This will uninstall ${uninstallTool?.name || 'this tool'} from the cluster. All related resources will be removed.`}
        confirmText="Disable"
        variant="destructive"
        loading={uninstallMutation.isPending}
      />
    </>
  );
}
