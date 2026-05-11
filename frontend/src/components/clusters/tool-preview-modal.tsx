'use client';

import { useState, useEffect } from 'react';
import { useQuery } from '@tanstack/react-query';
import * as apiClient from '@/lib/api';
import { X, Loader2 } from 'lucide-react';

interface ToolPreviewModalProps {
  toolSlug: string;
  toolName: string;
  clusterId: string;
  preset: string;
  onConfirm: (valuesOverride?: string) => void;
  onClose: () => void;
  installing?: boolean;
}

export function ToolPreviewModal({
  toolSlug,
  toolName,
  clusterId,
  preset,
  onConfirm,
  onClose,
  installing,
}: ToolPreviewModalProps) {
  const { data: preview, isLoading } = useQuery({
    queryKey: ['tools', 'preview', toolSlug, clusterId, preset],
    queryFn: () => apiClient.previewToolInstall(toolSlug, { cluster_id: clusterId, preset }),
  });

  const [valuesOverride, setValuesOverride] = useState('');

  useEffect(() => {
    if (preview?.charts?.[0]?.values_yaml) {
      setValuesOverride(preview.charts[0].values_yaml);
    }
  }, [preview]);

  return (
    <div className="fixed inset-0 z-50 flex items-center justify-center">
      <div className="fixed inset-0 bg-black/60 backdrop-blur-sm" onClick={onClose} />
      <div className="relative w-full max-w-2xl max-h-[85vh] rounded-xl border border-border bg-popover shadow-2xl flex flex-col">
        {/* Header */}
        <div className="flex items-center justify-between px-6 py-4 border-b border-border flex-shrink-0">
          <div>
            <h3 className="text-lg font-semibold text-foreground">
              Install {toolName}
            </h3>
            <p className="text-xs text-muted-foreground mt-0.5">
              Preset: <span className="capitalize">{preset}</span>
            </p>
          </div>
          <button onClick={onClose} className="text-muted-foreground hover:text-foreground transition-colors">
            <X className="h-5 w-5" />
          </button>
        </div>

        {/* Content */}
        <div className="flex-1 overflow-y-auto p-6 space-y-4">
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
        </div>

        {/* Footer */}
        <div className="flex items-center justify-end gap-2 px-6 py-4 border-t border-border flex-shrink-0 bg-muted/30">
          <button
            onClick={onClose}
            className="h-9 px-4 rounded-lg border border-border text-sm font-medium
              text-muted-foreground hover:text-foreground hover:bg-accent transition-colors"
          >
            Cancel
          </button>
          <button
            onClick={() => onConfirm(valuesOverride || undefined)}
            disabled={installing || isLoading}
            className="inline-flex items-center gap-2 h-9 px-4 rounded-lg bg-primary text-primary-foreground
              text-sm font-medium hover:opacity-90 transition-opacity disabled:opacity-50"
          >
            {installing && <Loader2 className="h-3.5 w-3.5 animate-spin" />}
            Install
          </button>
        </div>
      </div>
    </div>
  );
}
