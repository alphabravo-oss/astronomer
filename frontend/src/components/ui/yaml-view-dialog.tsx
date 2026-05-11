'use client';

import { useState, useEffect } from 'react';
import { useK8sGetYaml, useK8sApplyYaml } from '@/lib/hooks';
import { YamlEditor } from '@/components/ui/yaml-editor';
import { Loader2, X, Pencil, Eye } from 'lucide-react';
import { cn } from '@/lib/utils';

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
  const [editMode, setEditMode] = useState(initialEditMode);
  const [editedYaml, setEditedYaml] = useState('');

  const { data: yaml, isLoading, error, refetch } = useK8sGetYaml(clusterId, k8sPath, open);
  const applyYaml = useK8sApplyYaml();

  // Sync fetched YAML into editor state
  useEffect(() => {
    if (yaml) setEditedYaml(yaml);
  }, [yaml]);

  // Reset state when dialog opens
  useEffect(() => {
    if (open) {
      setEditMode(initialEditMode);
      refetch();
    }
  }, [open, initialEditMode, refetch]);

  // Close on Escape
  useEffect(() => {
    if (!open) return;
    function handleKey(e: KeyboardEvent) {
      if (e.key === 'Escape') onClose();
    }
    document.addEventListener('keydown', handleKey);
    return () => document.removeEventListener('keydown', handleKey);
  }, [open, onClose]);

  if (!open) return null;

  const handleSave = (yamlStr: string) => {
    applyYaml.mutate(
      { clusterId, path: k8sPath, yaml: yamlStr },
      {
        onSuccess: () => {
          refetch();
          setEditMode(false);
        },
      }
    );
  };

  return (
    <div className="fixed inset-0 z-50 flex items-center justify-center">
      {/* Backdrop */}
      <div className="absolute inset-0 bg-black/50 backdrop-blur-sm" onClick={onClose} />

      {/* Dialog */}
      <div className="relative bg-card border border-border rounded-lg shadow-xl
        w-[90vw] max-w-4xl h-[80vh] flex flex-col animate-fade-in">
        {/* Header */}
        <div className="flex items-center justify-between px-4 py-3 border-b border-border">
          <div className="flex items-center gap-3 min-w-0">
            <h3 className="text-sm font-semibold text-foreground truncate">{title}</h3>
            {allowEdit && (
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
            )}
          </div>
          <button onClick={onClose} className="p-1 rounded hover:bg-accent text-muted-foreground hover:text-foreground transition-colors">
            <X className="h-4 w-4" />
          </button>
        </div>

        {/* Body */}
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
            <YamlEditor
              value={editMode ? editedYaml : (yaml || '')}
              onChange={editMode ? setEditedYaml : undefined}
              readOnly={!editMode}
              onSave={editMode ? handleSave : undefined}
              saving={applyYaml.isPending}
              className="h-full"
            />
          )}
        </div>
      </div>
    </div>
  );
}
