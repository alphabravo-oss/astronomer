'use client';

/**
 * Rendered-Helm-values preview for a monitoring stack.
 *
 * The preview endpoints are the only way to see what install/upgrade would
 * actually apply before applying it, so this dialog is reachable from the
 * lifecycle panel without leaving the page — and it is available to a caller
 * holding nothing but monitoring:read, which is what the endpoint requires.
 *
 * The values map arrives already sanitized server-side (sanitizeMonitoringValues
 * strips credentials), so rendering it verbatim is safe.
 */
import { dump } from 'js-yaml';
import { AlertTriangle, FileCode2 } from 'lucide-react';

import { ActionButton } from '@/components/ui/action-button';
import { CodeBlock } from '@/components/ui/code-block';
import { ModalShell } from '@/components/ui/modal-shell';
import type { MonitoringStackPreview } from '@/lib/api/monitoring-stack';

export interface StackPreviewDialogProps {
  open: boolean;
  onClose: () => void;
  title: string;
  preview: MonitoringStackPreview | null;
  isLoading: boolean;
  error?: Error | null;
  /** Rendered under the values, e.g. "Install" / "Upgrade" for the apply step. */
  actions?: React.ReactNode;
}

export function renderPreviewValues(values: Record<string, unknown> | undefined): string {
  if (!values || Object.keys(values).length === 0) return '# The backend rendered no values.';
  try {
    return dump(values, { lineWidth: 120, noRefs: true, sortKeys: true });
  } catch {
    // A values map the YAML serializer chokes on is still worth showing.
    return JSON.stringify(values, null, 2);
  }
}

export function StackPreviewDialog({
  open,
  onClose,
  title,
  preview,
  isLoading,
  error,
  actions,
}: StackPreviewDialogProps) {
  if (!open) return null;

  return (
    <ModalShell
      title={`Preview — ${title}`}
      subtitle={
        preview
          ? `${preview.chart.chartName} from ${preview.chart.repoUrl}`
          : 'Rendering chart values…'
      }
      titleIcon={<FileCode2 className="h-4 w-4 text-muted-foreground" />}
      onClose={onClose}
      size="xl"
      footer={
        <div className="flex flex-wrap items-center justify-between gap-2">
          <span className="font-mono text-[10px] text-muted-foreground">
            {preview?.desiredSpecHash ? `spec ${preview.desiredSpecHash.slice(0, 12)}` : ''}
          </span>
          <div className="flex items-center gap-2">
            {actions}
            <ActionButton size="sm" intent="ghost" onClick={onClose}>
              Close
            </ActionButton>
          </div>
        </div>
      }
    >
      {isLoading && <p className="text-sm text-muted-foreground">Rendering chart values…</p>}

      {error && (
        <p className="rounded-md border border-status-error/30 bg-status-error/10 px-3 py-2 text-sm text-status-error">
          {error.message}
        </p>
      )}

      {preview?.requiresReplace && (
        <div className="flex items-start gap-2 rounded-md border border-status-warning/30 bg-status-warning/10 px-3 py-2 text-xs text-status-warning">
          <AlertTriangle className="mt-0.5 h-3.5 w-3.5 shrink-0" />
          <div className="min-w-0">
            <p className="font-medium">This change cannot be applied in place.</p>
            <p className="mt-0.5">
              Upgrade will be rejected; use Replace, which uninstalls the release and reinstalls it.
              {preview.replaceReasons?.length ? ` Reasons: ${preview.replaceReasons.join(', ')}.` : ''}
            </p>
          </div>
        </div>
      )}

      {/*
        no-release-history-or-revision-rollback-ui (separate, still-open audit
        item): a values DIFF belongs right here — this dialog already has the
        desired values, and the Helm release history endpoint would supply the
        deployed revision to diff them against. Deliberately not built in this
        change.
      */}

      {preview && (
        <CodeBlock
          code={renderPreviewValues(preview.values)}
          language="yaml"
          title="Rendered Helm values"
          className="max-h-[50vh] overflow-auto"
        />
      )}
    </ModalShell>
  );
}
