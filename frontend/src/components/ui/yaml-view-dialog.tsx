'use client';

import { useState, useEffect, useRef } from 'react';
import { useK8sGetYaml, useK8sApplyYaml, useK8sDryRunYaml } from '@/lib/hooks';
import { YamlEditor } from '@/components/ui/yaml-editor';
import { ModalShell } from '@/components/ui/modal-shell';
import { Loader2, Pencil, Eye, GitCompare, AlertTriangle } from 'lucide-react';
import { cn } from '@/lib/utils';
import * as apiClient from '@/lib/api';
import { toastWarning } from '@/lib/toast';

interface YamlViewDialogProps {
  open: boolean;
  onClose: () => void;
  clusterId: string;
  /** K8s API path (e.g. "api/v1/namespaces/default/pods/my-pod") */
  k8sPath: string;
  /** Display title */
  title: string;
  /** Start in edit mode */
  editMode?: boolean;
  /** If false, hide the edit toggle */
  allowEdit?: boolean;
}

export function YamlViewDialog({
  open,
  onClose,
  clusterId,
  k8sPath,
  title,
  editMode: initialEditMode = false,
  allowEdit = true,
}: YamlViewDialogProps) {
  if (!open) return null;

  return (
    <ModalShell
      title={title}
      onClose={onClose}
      size="xl"
      panelClassName="w-[90vw] h-[80vh] max-w-4xl flex flex-col overflow-hidden"
      bodyClassName="flex-1 min-h-0 p-0 space-y-0"
    >
      {/* ponytail: YamlPanel owns fetch/edit/dry-run and the View/Edit toggle; dialog is just chrome. */}
      <YamlPanel
        clusterId={clusterId}
        k8sPath={k8sPath}
        allowEdit={allowEdit}
        editMode={initialEditMode}
        active={open}
      />
    </ModalShell>
  );
}

interface YamlPanelProps {
  clusterId: string;
  /** K8s API path (e.g. "api/v1/namespaces/default/pods/my-pod") */
  k8sPath: string;
  /** If false, hide the edit toggle (read-only) */
  allowEdit?: boolean;
  /** Start in edit mode */
  editMode?: boolean;
  /** When false, fetching is paused (used by the dialog when closed). Defaults true. */
  active?: boolean;
}

/**
 * Embeddable YAML view/edit/dry-run panel. Used both as the body of YamlViewDialog
 * and as the YAML tab of ResourceDetail.
 */
export function YamlPanel({
  clusterId,
  k8sPath,
  allowEdit = true,
  editMode: initialEditMode = false,
  active = true,
}: YamlPanelProps) {
  const [editMode, setEditMode] = useState(initialEditMode);
  const [editedYaml, setEditedYaml] = useState('');
  const [preview, setPreview] = useState<YamlApplyPreview | null>(null);

  const { data: yaml, isLoading, error, refetch } = useK8sGetYaml(clusterId, k8sPath, active);
  const applyYaml = useK8sApplyYaml();
  const dryRunYaml = useK8sDryRunYaml();

  // Read the current edit mode through a ref inside the sync effect so the
  // effect stays keyed on [yaml] only. A background refetch (refetchOnWindowFocus
  // after an alt-tab, or a k8s.all cache invalidation from any mutation) delivers
  // a new server YAML string; without this guard the effect would overwrite the
  // editor and silently discard the operator's in-progress edits.
  const editModeRef = useRef(editMode);
  editModeRef.current = editMode;

  // Seed the editor from fetched YAML, but never while the operator is editing.
  useEffect(() => {
    if (yaml && !editModeRef.current) {
      setEditedYaml(yaml);
      setPreview(null);
    }
  }, [yaml]);

  // Reset state when (re)activated
  useEffect(() => {
    if (active) {
      setEditMode(initialEditMode);
      setPreview(null);
      refetch();
    }
  }, [active, initialEditMode, refetch]);

  const handleSave = (yamlStr: string) => {
    if (!preview || preview.previewFor !== yamlStr) {
      toastWarning('Run dry run and review the diff before saving.');
      void handleDryRun(yamlStr);
      return;
    }
    applyYaml.mutate(
      { clusterId, path: k8sPath, yaml: yamlStr },
      {
        onSuccess: () => {
          refetch();
          setEditMode(false);
          setPreview(null);
        },
      }
    );
  };

  const handleDryRun = async (yamlStr: string) => {
    setPreview(null);
    try {
      const [yamlModule, latestYaml, normalizedObject] = await Promise.all([
        import('js-yaml'),
        apiClient.k8sGetYaml(clusterId, k8sPath),
        dryRunYaml.mutateAsync({ clusterId, path: k8sPath, yaml: yamlStr }),
      ]);
      const normalizedYaml = yamlModule.dump(normalizedObject, { lineWidth: -1, noRefs: true });
      const warnings: string[] = [];
      if (yaml && normalizeText(latestYaml) !== normalizeText(yaml)) {
        warnings.push('The live object changed after this editor opened. Review the diff carefully before applying.');
      }
      setPreview({
        previewFor: yamlStr,
        changed: normalizeText(latestYaml) !== normalizeText(normalizedYaml),
        diff: buildYamlDiff(latestYaml, normalizedYaml),
        warnings,
      });
    } catch {
      setPreview(null);
    }
  };

  return (
    <div className="flex h-full min-h-0 flex-col">
      {allowEdit && (
        <div className="flex shrink-0 items-center justify-end border-b border-border px-3 py-2">
          <div className="flex items-center bg-muted rounded p-0.5">
            <button
              onClick={() => setEditMode(false)}
              className={cn(
                'inline-flex items-center gap-1 px-2 py-1 rounded text-xs font-medium transition-colors',
                !editMode ? 'bg-background text-foreground shadow-sm' : 'text-muted-foreground hover:text-foreground'
              )}
            >
              <Eye className="h-3 w-3" /> View
            </button>
            <button
              onClick={() => setEditMode(true)}
              className={cn(
                'inline-flex items-center gap-1 px-2 py-1 rounded text-xs font-medium transition-colors',
                editMode ? 'bg-background text-foreground shadow-sm' : 'text-muted-foreground hover:text-foreground'
              )}
            >
              <Pencil className="h-3 w-3" /> Edit
            </button>
          </div>
        </div>
      )}
      <div className="flex-1 min-h-0">
        {isLoading ? (
          <div className="flex items-center justify-center h-full">
            <Loader2 className="h-6 w-6 animate-spin text-muted-foreground" />
          </div>
        ) : error ? (
          <div className="flex items-center justify-center h-full text-sm text-status-error">
            Failed to load YAML: {(error as Error).message}
          </div>
        ) : (
          <div className="flex h-full flex-col">
            <div className="min-h-0 flex-1">
              <YamlEditor
                value={editMode ? editedYaml : (yaml || '')}
                onChange={editMode ? (next) => {
                  setEditedYaml(next);
                  if (preview?.previewFor !== next) setPreview(null);
                } : undefined}
                readOnly={!editMode}
                onDryRun={editMode ? handleDryRun : undefined}
                onSave={editMode ? handleSave : undefined}
                saving={applyYaml.isPending}
                dryRunning={dryRunYaml.isPending}
                saveBlocked={editMode && (!preview || preview.previewFor !== editedYaml)}
                className="h-full"
              />
            </div>
            {editMode && preview && <YamlDiffPreview preview={preview} />}
          </div>
        )}
      </div>
    </div>
  );
}

interface YamlApplyPreview {
  previewFor: string;
  changed: boolean;
  diff: YamlDiff;
  warnings: string[];
}

interface YamlDiff {
  added: number;
  removed: number;
  lines: Array<{ type: 'context' | 'add' | 'remove'; text: string; key: string }>;
}

function normalizeText(value: string): string {
  return value.replace(/\r\n/g, '\n').trimEnd();
}

function buildYamlDiff(before: string, after: string): YamlDiff {
  const beforeLines = normalizeText(before).split('\n');
  const afterLines = normalizeText(after).split('\n');
  let start = 0;
  while (start < beforeLines.length && start < afterLines.length && beforeLines[start] === afterLines[start]) {
    start++;
  }
  let endBefore = beforeLines.length - 1;
  let endAfter = afterLines.length - 1;
  while (endBefore >= start && endAfter >= start && beforeLines[endBefore] === afterLines[endAfter]) {
    endBefore--;
    endAfter--;
  }
  const contextStart = Math.max(0, start - 3);
  const contextEndBefore = Math.min(beforeLines.length - 1, endBefore + 3);
  const contextEndAfter = Math.min(afterLines.length - 1, endAfter + 3);
  const lines: YamlDiff['lines'] = [];
  for (let i = contextStart; i < start; i++) {
    lines.push({ type: 'context', text: beforeLines[i] ?? '', key: `c-pre-${i}` });
  }
  for (let i = start; i <= endBefore; i++) {
    lines.push({ type: 'remove', text: beforeLines[i] ?? '', key: `r-${i}` });
  }
  for (let i = start; i <= endAfter; i++) {
    lines.push({ type: 'add', text: afterLines[i] ?? '', key: `a-${i}` });
  }
  const contextAfterStart = Math.max(start, endBefore + 1);
  const contextAfterEnd = Math.max(contextEndBefore, contextEndAfter);
  for (let i = contextAfterStart; i <= contextAfterEnd && i < beforeLines.length; i++) {
    if (i < start || i <= endBefore) continue;
    lines.push({ type: 'context', text: beforeLines[i] ?? '', key: `c-post-${i}` });
  }
  return {
    added: Math.max(0, endAfter - start + 1),
    removed: Math.max(0, endBefore - start + 1),
    lines: lines.length > 0 ? lines.slice(0, 240) : [{ type: 'context', text: 'No changes after server-side normalization.', key: 'none' }],
  };
}

function YamlDiffPreview({ preview }: { preview: YamlApplyPreview }) {
  return (
    <div className="max-h-[34%] border-t border-border bg-background">
      <div className="flex items-center justify-between gap-3 border-b border-border px-3 py-2">
        <div className="flex items-center gap-2 text-sm font-medium text-foreground">
          <GitCompare className="h-4 w-4" />
          Apply preview
        </div>
        <div className="text-xs tabular-nums text-muted-foreground">
          +{preview.diff.added} / -{preview.diff.removed}
        </div>
      </div>
      {preview.warnings.length > 0 && (
        <div className="space-y-1 border-b border-status-warning/20 bg-status-warning/10 px-3 py-2 text-xs text-status-warning">
          {preview.warnings.map((warning) => (
            <div key={warning} className="flex items-start gap-2">
              <AlertTriangle className="mt-0.5 h-3.5 w-3.5 shrink-0" />
              <span>{warning}</span>
            </div>
          ))}
        </div>
      )}
      <pre className="max-h-48 overflow-auto px-3 py-2 text-xs leading-5">
        {preview.diff.lines.map((line) => (
          <div
            key={line.key}
            className={cn(
              'min-w-max font-mono',
              line.type === 'add' && 'bg-status-success/10 text-status-success',
              line.type === 'remove' && 'bg-status-error/10 text-status-error',
              line.type === 'context' && 'text-muted-foreground',
            )}
          >
            <span className="inline-block w-4 select-none">
              {line.type === 'add' ? '+' : line.type === 'remove' ? '-' : ' '}
            </span>
            {line.text}
          </div>
        ))}
      </pre>
    </div>
  );
}
