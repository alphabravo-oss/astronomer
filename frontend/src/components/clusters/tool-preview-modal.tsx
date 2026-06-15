'use client';

import { useState, useEffect } from 'react';
import { useQuery } from '@tanstack/react-query';
import * as apiClient from '@/lib/api';
import { queryKeys } from '@/lib/query-keys';
import { ModalShell } from '@/components/ui/modal-shell';
import { Loader2 } from 'lucide-react';
import type { PermissionDecision } from '@/lib/permissions';
import { toastWarning } from '@/lib/toast';

interface ToolPreviewModalProps {
  toolSlug: string;
  toolName: string;
  clusterId: string;
  preset: string;
  onConfirm: (valuesOverride?: string) => void;
  onClose: () => void;
  installing?: boolean;
  confirmDecision?: PermissionDecision;
}

function permissionDeniedReason(decision: PermissionDecision): string {
  return decision.disabledReason || decision.reason;
}

export function ToolPreviewModal({
  toolSlug,
  toolName,
  clusterId,
  preset,
  onConfirm,
  onClose,
  installing,
  confirmDecision,
}: ToolPreviewModalProps) {
  const { data: preview, isLoading } = useQuery({
    queryKey: queryKeys.tools.preview(toolSlug, clusterId, preset),
    queryFn: () => apiClient.previewToolInstall(toolSlug, { cluster_id: clusterId, preset }),
  });

  const [valuesOverride, setValuesOverride] = useState('');

  useEffect(() => {
    if (preview?.charts?.[0]?.values_yaml) {
      setValuesOverride(preview.charts[0].values_yaml);
    }
  }, [preview]);
  const confirmBlockedReason = confirmDecision && !confirmDecision.allowed
    ? permissionDeniedReason(confirmDecision)
    : undefined;
  const handleConfirm = () => {
    if (confirmBlockedReason) {
      toastWarning(confirmBlockedReason);
      return;
    }
    onConfirm(valuesOverride || undefined);
  };

  return (
    <ModalShell
      title={`Install ${toolName}`}
      onClose={onClose}
      size="lg"
      panelClassName="max-w-2xl max-h-[85vh] bg-popover flex flex-col overflow-hidden"
      bodyClassName="flex-1 overflow-y-auto"
      footerClassName="bg-muted/30"
      headerActions={<p className="text-xs text-muted-foreground">Preset: <span className="capitalize">{preset}</span></p>}
      footer={(
        <div className="flex items-center justify-end gap-2">
          <button
            onClick={onClose}
            className="h-9 px-4 rounded-lg border border-border text-sm font-medium
              text-muted-foreground hover:text-foreground hover:bg-accent transition-colors"
          >
            Cancel
          </button>
          <button
            onClick={handleConfirm}
            disabled={installing || isLoading || !!confirmBlockedReason}
            title={confirmBlockedReason}
            className="inline-flex items-center gap-2 h-9 px-4 rounded-lg bg-primary text-primary-foreground
              text-sm font-medium hover:opacity-90 transition-opacity disabled:opacity-50"
          >
            {installing && <Loader2 className="h-3.5 w-3.5 animate-spin" />}
            Install
          </button>
        </div>
      )}
    >
          {isLoading ? (
            <div className="flex items-center justify-center py-12">
              <Loader2 className="h-6 w-6 animate-spin text-muted-foreground" />
            </div>
          ) : preview ? (
            <>
              {/* Charts info */}
              {preview.charts.map((chart, i) => (
                <div key={i} className="space-y-3">
                  <div className="grid grid-cols-3 gap-3">
                    <div>
                      <label className="text-xs font-medium text-muted-foreground">Chart</label>
                      <p className="text-sm text-foreground font-mono">{chart.chart_name}</p>
                    </div>
                    <div>
                      <label className="text-xs font-medium text-muted-foreground">Version</label>
                      <p className="text-sm text-foreground font-mono">{chart.chart_version}</p>
                    </div>
                    <div>
                      <label className="text-xs font-medium text-muted-foreground">Namespace</label>
                      <p className="text-sm text-foreground font-mono">{chart.namespace}</p>
                    </div>
                  </div>
                </div>
              ))}

              {/* Values editor */}
              <div className="space-y-1.5">
                <label className="text-sm font-medium text-foreground">Values (YAML)</label>
                <textarea
                  value={valuesOverride}
                  onChange={(e) => setValuesOverride(e.target.value)}
                  rows={16}
                  className="w-full px-3 py-2 rounded-md border border-border bg-background text-sm font-mono
                    placeholder:text-muted-foreground focus:outline-none focus:ring-1 focus:ring-ring resize-none"
                />
              </div>
            </>
          ) : (
            <div className="flex items-center justify-center py-12 text-sm text-muted-foreground">
              Failed to load preview
            </div>
          )}
    </ModalShell>
  );
}
