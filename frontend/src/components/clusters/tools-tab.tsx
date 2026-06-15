'use client';

import { Link } from '@/lib/link';
import { useState } from 'react';
import { useTools, useClusterToolsStatus, useInstallTool, useUninstallTool, useAdoptTool } from '@/lib/hooks';
import { usePermissionDecision } from '@/lib/permission-hooks';
import type { PermissionDecision } from '@/lib/permissions';
import { ToolCard } from '@/components/clusters/tool-card';
import { ToolInstallModal } from '@/components/clusters/tool-install-modal';
import { ToolInstallProgress } from '@/components/clusters/tool-install-progress';
import { ConfirmDialog } from '@/components/ui/confirm-dialog';
import type { ClusterTool } from '@/types';
import { Loader2, Wrench, Sparkles } from 'lucide-react';
import { toastWarning } from '@/lib/toast';

interface ToolsTabProps {
  clusterId: string;
  clusterEnvironment: string;
  clusterStatus?: string;
}

function permissionDeniedReason(decision: PermissionDecision): string {
  return decision.disabledReason || decision.reason;
}

function toastPermissionDenied(decision: PermissionDecision) {
  toastWarning(permissionDeniedReason(decision));
}

export function ToolsTab({ clusterId, clusterEnvironment, clusterStatus }: ToolsTabProps) {
  const isDisconnected = clusterStatus === 'disconnected';
  const { data: tools, isLoading: toolsLoading } = useTools();
  const { data: statuses } = useClusterToolsStatus(clusterId);
  const catalogScope = { type: 'cluster' as const, id: clusterId };
  const catalogCreateDecision = usePermissionDecision('catalog', 'create', catalogScope);
  const catalogDeleteDecision = usePermissionDecision('catalog', 'delete', catalogScope);

  const installMutation = useInstallTool();
  const uninstallMutation = useUninstallTool();
  const adoptMutation = useAdoptTool();

  const [installTool, setInstallTool] = useState<{ tool: ClusterTool; preset: string } | null>(null);
  const [activeOperation, setActiveOperation] = useState<{ id: string; name: string } | null>(null);
  const [uninstallSlug, setUninstallSlug] = useState<string | null>(null);

  const statusMap = new Map<string, (typeof statuses extends (infer T)[] | undefined ? T : never)>();
  statuses?.forEach((s) => statusMap.set(s.slug, s));

  const defaultPreset = ['production', 'staging', 'development'].includes(clusterEnvironment)
    ? clusterEnvironment
    : 'development';

  const handleInstall = (slug: string, preset: string) => {
    if (!catalogCreateDecision.allowed) {
      toastPermissionDenied(catalogCreateDecision);
      return;
    }
    const tool = tools?.find((t) => t.slug === slug);
    if (tool) {
      setInstallTool({ tool, preset });
    }
  };

  const handleConfirmInstall = (valuesOverride?: string) => {
    if (!installTool) return;
    if (!catalogCreateDecision.allowed) {
      toastPermissionDenied(catalogCreateDecision);
      return;
    }
    const name = installTool.tool.name;
    installMutation.mutate(
      {
        slug: installTool.tool.slug,
        cluster_id: clusterId,
        preset: installTool.preset,
        values_override: valuesOverride,
      },
      {
        onSuccess: (op) => {
          setInstallTool(null);
          // Open the live progress drawer keyed on the returned operation id.
          if (op?.id) setActiveOperation({ id: op.id, name });
        },
      }
    );
  };

  const handleUninstall = (slug: string) => {
    if (!catalogDeleteDecision.allowed) {
      toastPermissionDenied(catalogDeleteDecision);
      return;
    }
    setUninstallSlug(slug);
  };

  const handleConfirmUninstall = () => {
    if (!uninstallSlug) return;
    if (!catalogDeleteDecision.allowed) {
      toastPermissionDenied(catalogDeleteDecision);
      return;
    }
    uninstallMutation.mutate(
      { slug: uninstallSlug, cluster_id: clusterId },
      {
        onSuccess: () => setUninstallSlug(null),
      }
    );
  };

  const handleAdopt = (slug: string, releaseName: string) => {
    if (!catalogCreateDecision.allowed) {
      toastPermissionDenied(catalogCreateDecision);
      return;
    }
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

  // Sprint 074 — empty-state CTA. When NO tool is yet installed on this
  // cluster, surface a prominent "Apply Platform Baseline template"
  // pointer at the cluster's template-attach page. The baseline (sprint
  // 074 seed) wires trivy-operator + kube-state-metrics + node-exporter
  // + fluent-bit + ingress-nginx + cert-manager + gatekeeper in one click. Operators registered
  // BEFORE sprint 074 don't have the auto-attach for free — this banner
  // bridges them.
  const noToolsInstalled =
    !statuses ||
    statuses.length === 0 ||
    statuses.every((s) => !['installed', 'installing'].includes(String(s.status || '').toLowerCase()));

  return (
    <>
      {noToolsInstalled && !isDisconnected && (
        <div className="mb-4 flex items-start gap-3 rounded-lg border border-primary/30 bg-primary/5 p-4">
          <Sparkles className="h-5 w-5 mt-0.5 text-primary flex-shrink-0" />
          <div className="flex-1">
            <p className="text-sm font-medium">No tools installed yet</p>
            <p className="text-xs text-muted-foreground mt-1">
              Apply the <strong>Platform Baseline</strong> template to install Astronomer&apos;s
              recommended operators in one step: image-scanning (trivy-operator), metrics
              (kube-state-metrics, node-exporter), log forwarding (fluent-bit), ingress
              (ingress-nginx), TLS (cert-manager), and policy (Gatekeeper). New clusters get this automatically.
            </p>
            <Link
              href={`/dashboard/clusters/${clusterId}/template`}
              className="mt-2 inline-flex items-center gap-1 text-xs font-medium text-primary hover:underline"
            >
              Apply Platform Baseline →
            </Link>
          </div>
        </div>
      )}

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
            installDisabledReason={!catalogCreateDecision.allowed ? permissionDeniedReason(catalogCreateDecision) : undefined}
            adoptDisabledReason={!catalogCreateDecision.allowed ? permissionDeniedReason(catalogCreateDecision) : undefined}
            uninstallDisabledReason={!catalogDeleteDecision.allowed ? permissionDeniedReason(catalogDeleteDecision) : undefined}
            installing={installMutation.isPending && installMutation.variables?.slug === tool.slug}
            uninstalling={uninstallMutation.isPending && uninstallMutation.variables?.slug === tool.slug}
            clusterDisconnected={isDisconnected}
          />
        ))}
      </div>

      {/* Install config modal — form + Edit YAML */}
      {installTool && (
        <ToolInstallModal
          tool={installTool.tool}
          clusterId={clusterId}
          preset={installTool.preset}
          onConfirm={handleConfirmInstall}
          onClose={() => setInstallTool(null)}
          installing={installMutation.isPending}
          confirmDecision={catalogCreateDecision}
        />
      )}

      {/* Live install-progress drawer (Rancher-style bottom terminal) */}
      {activeOperation && (
        <ToolInstallProgress
          operationId={activeOperation.id}
          toolName={activeOperation.name}
          onClose={() => setActiveOperation(null)}
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
        confirmDisabledReason={!catalogDeleteDecision.allowed ? permissionDeniedReason(catalogDeleteDecision) : undefined}
        variant="destructive"
        loading={uninstallMutation.isPending}
      />
    </>
  );
}
