'use client';

import { useState } from 'react';
import { normalizeToolStatus } from '@/lib/tool-status';
import { StatusBadge } from '@/components/ui/status-badge';
import type { ClusterTool, ClusterToolStatus, ToolStatus } from '@/types';
import {
  Activity,
  ScrollText,
  ShieldCheck,
  ShieldAlert,
  Archive,
  Network,
  Wrench,
  Loader2,
  AlertTriangle,
} from 'lucide-react';

const toolIcons: Record<string, typeof Wrench> = {
  monitoring: Activity,
  logging: ScrollText,
  'security-trivy': ShieldCheck,
  'security-falco': ShieldAlert,
  backup: Archive,
  'service-mesh': Network,
};

const statusToBadge: Record<ToolStatus, { status: string; label: string }> = {
  installed: { status: 'active', label: 'Installed' },
  installing: { status: 'provisioning', label: 'Installing' },
  upgrading: { status: 'provisioning', label: 'Upgrading' },
  uninstalling: { status: 'provisioning', label: 'Uninstalling' },
  failed: { status: 'error', label: 'Failed' },
  not_installed: { status: 'disconnected', label: 'Not Installed' },
  installed_unmanaged: { status: 'warning', label: 'Unmanaged' },
  unknown: { status: 'warning', label: 'Unknown' },
};

const presetOptions = [
  { value: 'development', label: 'Development' },
  { value: 'staging', label: 'Staging' },
  { value: 'production', label: 'Production' },
];

interface ToolCardProps {
  tool: ClusterTool;
  toolStatus?: ClusterToolStatus;
  defaultPreset: string;
  onInstall: (slug: string, preset: string) => void;
  onUninstall: (slug: string) => void;
  onAdopt: (slug: string, releaseName: string) => void;
  installDisabledReason?: string;
  adoptDisabledReason?: string;
  uninstallDisabledReason?: string;
  installing?: boolean;
  uninstalling?: boolean;
  clusterDisconnected?: boolean;
}

export function ToolCard({
  tool,
  toolStatus,
  defaultPreset,
  onInstall,
  onUninstall,
  onAdopt,
  installDisabledReason,
  adoptDisabledReason,
  uninstallDisabledReason,
  installing,
  uninstalling,
  clusterDisconnected,
}: ToolCardProps) {
  const [selectedPreset, setSelectedPreset] = useState(defaultPreset);
  const status = normalizeToolStatus(toolStatus?.status);
  const badge = statusToBadge[status] || statusToBadge.unknown;
  const Icon = toolIcons[tool.slug] || Wrench;
  const isInProgress = status === 'installing' || status === 'upgrading' || status === 'uninstalling';
  const clusterDisconnectedReason = clusterDisconnected ? 'Cluster is disconnected' : undefined;
  const enableDisabledReason = clusterDisconnectedReason || installDisabledReason;
  const retryDisabledReason = clusterDisconnectedReason || installDisabledReason;
  const adoptBlockedReason = clusterDisconnectedReason || adoptDisabledReason;
  const uninstallBlockedReason = clusterDisconnectedReason || uninstallDisabledReason;

  return (
    <div className="rounded-lg border border-border p-5 space-y-4">
      {/* Header */}
      <div className="flex items-start justify-between">
        <div className="flex items-center gap-3">
          <div className="flex-shrink-0 h-10 w-10 rounded-lg bg-muted/60 flex items-center justify-center">
            <Icon className="h-5 w-5 text-muted-foreground" />
          </div>
          <div>
            <p className="font-medium text-foreground text-sm">{tool.name}</p>
            <p className="text-xs text-muted-foreground capitalize">{tool.category}</p>
          </div>
        </div>
        <StatusBadge
          status={badge.status}
          label={badge.label}
          pulse={isInProgress}
        />
      </div>

      {/* Description */}
      <p className="text-xs text-muted-foreground line-clamp-2">{tool.description}</p>

      {/* Error message */}
      {status === 'failed' && toolStatus?.error && (
        <div className="flex items-start gap-2 p-2.5 rounded-md bg-status-error/5 border border-status-error/20">
          <AlertTriangle className="h-3.5 w-3.5 text-status-error flex-shrink-0 mt-0.5" />
          <p className="text-xs text-status-error line-clamp-2">{toolStatus.error}</p>
        </div>
      )}

      {/* Actions */}
      <div className="pt-1">
        {status === 'not_installed' && (
          <div className="flex items-center gap-2">
            <select
              value={selectedPreset}
              onChange={(e) => setSelectedPreset(e.target.value)}
              className="flex-1 h-8 px-2 rounded-md border border-border bg-background text-xs
                focus:outline-none focus:ring-1 focus:ring-ring"
            >
              {presetOptions.map((opt) => (
                <option key={opt.value} value={opt.value}>
                  {opt.label}
                </option>
              ))}
            </select>
            <button
              onClick={() => onInstall(tool.slug, selectedPreset)}
              disabled={installing || !!enableDisabledReason}
              title={enableDisabledReason}
              className="inline-flex items-center gap-1.5 h-8 px-3 rounded-md bg-primary text-primary-foreground
                text-xs font-medium hover:opacity-90 transition-opacity disabled:opacity-50"
            >
              {installing && <Loader2 className="h-3 w-3 animate-spin" />}
              Enable
            </button>
          </div>
        )}

        {status === 'unknown' && (
          <div className="flex items-center gap-2 text-xs text-muted-foreground">
            <AlertTriangle className="h-3.5 w-3.5 text-amber-500" />
            <span>Cluster disconnected — status unknown</span>
          </div>
        )}

        {status === 'installed' && (
          <div className="flex items-center justify-between">
            {toolStatus?.preset_used && (
              <span className="text-xs text-muted-foreground">
                Preset: <span className="capitalize">{toolStatus.preset_used}</span>
              </span>
            )}
            <button
              onClick={() => onUninstall(tool.slug)}
              disabled={uninstalling || !!uninstallBlockedReason}
              title={uninstallBlockedReason}
              className="inline-flex items-center gap-1.5 h-8 px-3 rounded-md border border-border
                text-xs font-medium text-muted-foreground hover:text-status-error hover:border-status-error/30
                hover:bg-status-error/5 transition-colors disabled:opacity-50"
            >
              {uninstalling && <Loader2 className="h-3 w-3 animate-spin" />}
              Disable
            </button>
          </div>
        )}

        {status === 'installed_unmanaged' && (
          <div className="flex items-center justify-between">
            <span className="text-xs text-muted-foreground">
              Release: <span className="font-mono">{toolStatus?.release_name}</span>
            </span>
            <button
              onClick={() => {
                if (toolStatus?.release_name) {
                  onAdopt(tool.slug, toolStatus.release_name);
                }
              }}
              disabled={!!adoptBlockedReason}
              title={adoptBlockedReason}
              className="inline-flex items-center gap-1.5 h-8 px-3 rounded-md bg-primary text-primary-foreground
                text-xs font-medium hover:opacity-90 transition-opacity disabled:opacity-50"
            >
              Adopt
            </button>
          </div>
        )}

        {isInProgress && (
          <div className="flex items-center gap-2 text-xs text-muted-foreground">
            <Loader2 className="h-3.5 w-3.5 animate-spin" />
            <span className="capitalize">{status.replace('_', ' ')}...</span>
          </div>
        )}

        {status === 'failed' && (
          <div className="flex items-center gap-2">
            <select
              value={selectedPreset}
              onChange={(e) => setSelectedPreset(e.target.value)}
              className="flex-1 h-8 px-2 rounded-md border border-border bg-background text-xs
                focus:outline-none focus:ring-1 focus:ring-ring"
            >
              {presetOptions.map((opt) => (
                <option key={opt.value} value={opt.value}>
                  {opt.label}
                </option>
              ))}
            </select>
            <button
              onClick={() => onInstall(tool.slug, selectedPreset)}
              disabled={installing || !!retryDisabledReason}
              title={retryDisabledReason}
              className="inline-flex items-center gap-1.5 h-8 px-3 rounded-md bg-primary text-primary-foreground
                text-xs font-medium hover:opacity-90 transition-opacity disabled:opacity-50"
            >
              {installing && <Loader2 className="h-3 w-3 animate-spin" />}
              Retry
            </button>
          </div>
        )}
      </div>
    </div>
  );
}
