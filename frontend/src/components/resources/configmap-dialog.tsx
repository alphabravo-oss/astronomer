'use client';

import { useState, useEffect } from 'react';
import { KeyValueEditor, type KeyValuePair } from '@/components/resources/key-value-editor';
import { YamlEditor } from '@/components/ui/yaml-editor';
import { useK8sCreate, useK8sApplyYaml, useK8sGetYaml, useClusterNamespaces } from '@/lib/hooks';
import { k8sResourcePath } from '@/lib/k8s-paths';
import { Loader2, X } from 'lucide-react';
import { cn } from '@/lib/utils';

interface ConfigMapDialogProps {
  open: boolean;
  onClose: () => void;
  clusterId: string;
  /** If provided, edit mode for this configmap */
  editName?: string;
  editNamespace?: string;
}

export function ConfigMapDialog({
  open,
  onClose,
  clusterId,
  editName,
  editNamespace,
}: ConfigMapDialogProps) {
  const isEdit = !!editName;
  const [name, setName] = useState('');
  const [namespace, setNamespace] = useState('default');
  const [pairs, setPairs] = useState<KeyValuePair[]>([{ key: '', value: '' }]);
  const [mode, setMode] = useState<'form' | 'yaml'>('form');
  const [yamlContent, setYamlContent] = useState('');

  const { data: namespaces } = useClusterNamespaces(clusterId);
  const k8sCreate = useK8sCreate();
  const applyYaml = useK8sApplyYaml();
  const { data: existingYaml, isLoading: loadingYaml } = useK8sGetYaml(
    clusterId,
    editName ? k8sResourcePath('configmaps', editName, editNamespace) : '',
    open && isEdit
  );

  // Load existing configmap data for edit mode
  useEffect(() => {
    if (isEdit && existingYaml) {
      setYamlContent(existingYaml);
      // Parse YAML to extract data for form mode
      import('js-yaml').then((yaml) => {
        try {
          const obj = yaml.load(existingYaml) as { metadata?: { name?: string; namespace?: string }; data?: Record<string, string> };
          setName(obj?.metadata?.name || editName || '');
          setNamespace(obj?.metadata?.namespace || editNamespace || 'default');
          const data = obj?.data || {};
          setPairs(Object.entries(data).map(([key, value]) => ({ key, value: String(value) })));
        } catch { /* ignore parse errors */ }
      });
    }
  }, [isEdit, existingYaml, editName, editNamespace]);

  // Reset form when dialog opens
  useEffect(() => {
    if (open && !isEdit) {
      setName('');
      setNamespace('default');
      setPairs([{ key: '', value: '' }]);
      setMode('form');
      setYamlContent('');
    }
  }, [open, isEdit]);

  useEffect(() => {
    if (!open) return;
    function handleKey(e: KeyboardEvent) { if (e.key === 'Escape') onClose(); }
    document.addEventListener('keydown', handleKey);
    return () => document.removeEventListener('keydown', handleKey);
  }, [open, onClose]);

  if (!open) return null;

  const handleSubmit = () => {
    if (mode === 'yaml') {
      if (isEdit) {
        applyYaml.mutate(
          { clusterId, path: k8sResourcePath('configmaps', editName!, editNamespace!), yaml: yamlContent },
          { onSuccess: onClose }
        );
      } else {
        // Parse YAML to get name/namespace, then create
        import('js-yaml').then((yaml) => {
          try {
            const obj = yaml.load(yamlContent) as { metadata?: { name?: string; namespace?: string } };
            const ns = obj?.metadata?.namespace || namespace;
            k8sCreate.mutate(
              { clusterId, path: `api/v1/namespaces/${ns}/configmaps`, body: yaml.load(yamlContent) },
              { onSuccess: onClose }
            );
          } catch (e) {
            import('sonner').then(({ toast }) => toast.error('Invalid YAML'));
          }
        });
      }
      return;
    }

    const data = Object.fromEntries(pairs.filter((p) => p.key).map((p) => [p.key, p.value]));
    const body = {
      apiVersion: 'v1',
      kind: 'ConfigMap',
      metadata: { name, namespace },
      data,
    };

    if (isEdit) {
      applyYaml.mutate(
        { clusterId, path: k8sResourcePath('configmaps', editName!, editNamespace!), yaml: '' },
        { onSuccess: onClose }
      );
      // Actually use k8sUpdate via form data
      import('@/lib/api').then((api) => {
        api.k8sUpdate(clusterId, k8sResourcePath('configmaps', editName!, editNamespace!), body)
          .then(() => { import('sonner').then(({ toast }) => toast.success('ConfigMap updated')); onClose(); })
          .catch((e) => { import('sonner').then(({ toast }) => toast.error(`Failed: ${e.message}`)); });
      });
    } else {
      k8sCreate.mutate(
        { clusterId, path: `api/v1/namespaces/${namespace}/configmaps`, body },
        { onSuccess: onClose }
      );
    }
  };

  const isPending = k8sCreate.isPending || applyYaml.isPending;

  return (
    <div className="fixed inset-0 z-50 flex items-center justify-center">
      <div className="absolute inset-0 bg-black/50 backdrop-blur-sm" onClick={onClose} />
      <div className="relative bg-card border border-border rounded-lg shadow-xl w-[90vw] max-w-2xl max-h-[80vh] flex flex-col animate-fade-in">
        <div className="flex items-center justify-between px-4 py-3 border-b border-border">
          <h3 className="text-sm font-semibold text-foreground">{isEdit ? 'Edit ConfigMap' : 'Create ConfigMap'}</h3>
          <div className="flex items-center gap-2">
            <div className="flex items-center bg-muted rounded p-0.5">
              <button onClick={() => setMode('form')}
                className={cn('px-2 py-1 rounded text-xs font-medium transition-colors', mode === 'form' ? 'bg-background text-foreground shadow-sm' : 'text-muted-foreground')}>
                Form
              </button>
              <button onClick={() => {
                // Sync form to YAML
                if (mode === 'form') {
                  const data = Object.fromEntries(pairs.filter((p) => p.key).map((p) => [p.key, p.value]));
                  import('js-yaml').then((yaml) => {
                    setYamlContent(yaml.dump({ apiVersion: 'v1', kind: 'ConfigMap', metadata: { name, namespace }, data }, { lineWidth: -1 }));
                  });
                }
                setMode('yaml');
              }}
                className={cn('px-2 py-1 rounded text-xs font-medium transition-colors', mode === 'yaml' ? 'bg-background text-foreground shadow-sm' : 'text-muted-foreground')}>
                YAML
              </button>
            </div>
            <button onClick={onClose} className="p-1 rounded hover:bg-accent text-muted-foreground"><X className="h-4 w-4" /></button>
          </div>
        </div>

        <div className="flex-1 overflow-y-auto p-4">
          {loadingYaml ? (
            <div className="flex items-center justify-center h-32"><Loader2 className="h-5 w-5 animate-spin text-muted-foreground" /></div>
          ) : mode === 'yaml' ? (
            <div className="h-96 border border-border rounded overflow-hidden">
              <YamlEditor value={yamlContent} onChange={setYamlContent} />
            </div>
          ) : (
            <div className="space-y-4">
              <div>
                <label className="block text-xs text-muted-foreground mb-1">Name</label>
                <input type="text" value={name} onChange={(e) => setName(e.target.value)} disabled={isEdit}
                  placeholder="my-configmap"
                  className="w-full h-8 px-3 rounded border border-border bg-background text-sm font-mono focus:outline-none focus:ring-1 focus:ring-ring disabled:opacity-50" />
              </div>
              <div>
                <label className="block text-xs text-muted-foreground mb-1">Namespace</label>
                <select value={namespace} onChange={(e) => setNamespace(e.target.value)} disabled={isEdit}
                  className="w-full h-8 px-3 rounded border border-border bg-background text-sm focus:outline-none focus:ring-1 focus:ring-ring disabled:opacity-50">
                  {(namespaces || []).map((ns) => (
                    <option key={ns.name} value={ns.name}>{ns.name}</option>
                  ))}
                </select>
              </div>
              <div>
                <label className="block text-xs text-muted-foreground mb-2">Data</label>
                <KeyValueEditor pairs={pairs} onChange={setPairs} keyPlaceholder="Key" valuePlaceholder="Value" />
              </div>
            </div>
          )}
        </div>

        <div className="flex items-center justify-end gap-2 px-4 py-3 border-t border-border">
          <button onClick={onClose} className="h-8 px-3 rounded text-sm text-muted-foreground hover:text-foreground hover:bg-accent transition-colors">Cancel</button>
          <button onClick={handleSubmit} disabled={isPending || (!name && mode === 'form')}
            className="inline-flex items-center gap-1.5 h-8 px-4 rounded text-sm font-medium bg-primary text-primary-foreground hover:bg-primary/90 disabled:opacity-50 transition-colors">
            {isPending && <Loader2 className="h-3.5 w-3.5 animate-spin" />}
            {isEdit ? 'Update' : 'Create'}
          </button>
        </div>
      </div>
    </div>
  );
}
