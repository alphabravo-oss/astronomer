'use client';

import { useState, useEffect } from 'react';
import { KeyValueEditor, type KeyValuePair } from '@/components/resources/key-value-editor';
import { YamlEditor } from '@/components/ui/yaml-editor';
import { useK8sCreate, useClusterNamespaces } from '@/lib/hooks';
import { Loader2, X } from 'lucide-react';
import { cn } from '@/lib/utils';

interface SecretDialogProps {
  open: boolean;
  onClose: () => void;
  clusterId: string;
}

const secretTypes = [
  { value: 'Opaque', label: 'Opaque (generic)' },
  { value: 'kubernetes.io/basic-auth', label: 'Basic Auth' },
  { value: 'kubernetes.io/tls', label: 'TLS' },
  { value: 'kubernetes.io/dockerconfigjson', label: 'Docker Registry' },
  { value: 'kubernetes.io/ssh-auth', label: 'SSH Auth' },
];

export function SecretDialog({ open, onClose, clusterId }: SecretDialogProps) {
  const [name, setName] = useState('');
  const [namespace, setNamespace] = useState('default');
  const [secretType, setSecretType] = useState('Opaque');
  const [pairs, setPairs] = useState<KeyValuePair[]>([{ key: '', value: '' }]);
  const [mode, setMode] = useState<'form' | 'yaml'>('form');
  const [yamlContent, setYamlContent] = useState('');

  const { data: namespaces } = useClusterNamespaces(clusterId);
  const k8sCreate = useK8sCreate();

  useEffect(() => {
    if (open) {
      setName('');
      setNamespace('default');
      setSecretType('Opaque');
      setPairs([{ key: '', value: '' }]);
      setMode('form');
      setYamlContent('');
    }
  }, [open]);

  useEffect(() => {
    if (!open) return;
    function handleKey(e: KeyboardEvent) { if (e.key === 'Escape') onClose(); }
    document.addEventListener('keydown', handleKey);
    return () => document.removeEventListener('keydown', handleKey);
  }, [open, onClose]);

  if (!open) return null;

  const handleSubmit = () => {
    if (mode === 'yaml') {
      import('js-yaml').then((yaml) => {
        try {
          const obj = yaml.load(yamlContent) as { metadata?: { namespace?: string } };
          const ns = obj?.metadata?.namespace || namespace;
          k8sCreate.mutate(
            { clusterId, path: `api/v1/namespaces/${ns}/secrets`, body: yaml.load(yamlContent) },
            { onSuccess: onClose }
          );
        } catch {
          import('sonner').then(({ toast }) => toast.error('Invalid YAML'));
        }
      });
      return;
    }

    // Base64 encode values
    const data = Object.fromEntries(
      pairs.filter((p) => p.key).map((p) => [p.key, btoa(p.value)])
    );
    const body = {
      apiVersion: 'v1',
      kind: 'Secret',
      type: secretType,
      metadata: { name, namespace },
      data,
    };
    k8sCreate.mutate(
      { clusterId, path: `api/v1/namespaces/${namespace}/secrets`, body },
      { onSuccess: onClose }
    );
  };

  return (
    <div className="fixed inset-0 z-50 flex items-center justify-center">
      <div className="absolute inset-0 bg-black/50 backdrop-blur-sm" onClick={onClose} />
      <div className="relative bg-card border border-border rounded-lg shadow-xl w-[90vw] max-w-2xl max-h-[80vh] flex flex-col animate-fade-in">
        <div className="flex items-center justify-between px-4 py-3 border-b border-border">
          <h3 className="text-sm font-semibold text-foreground">Create Secret</h3>
          <div className="flex items-center gap-2">
            <div className="flex items-center bg-muted rounded p-0.5">
              <button onClick={() => setMode('form')}
                className={cn('px-2 py-1 rounded text-xs font-medium transition-colors', mode === 'form' ? 'bg-background text-foreground shadow-sm' : 'text-muted-foreground')}>
                Form
              </button>
              <button onClick={() => {
                if (mode === 'form') {
                  const data = Object.fromEntries(pairs.filter((p) => p.key).map((p) => [p.key, btoa(p.value)]));
                  import('js-yaml').then((yaml) => {
                    setYamlContent(yaml.dump({ apiVersion: 'v1', kind: 'Secret', type: secretType, metadata: { name, namespace }, data }, { lineWidth: -1 }));
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
          {mode === 'yaml' ? (
            <div className="h-96 border border-border rounded overflow-hidden">
              <YamlEditor value={yamlContent} onChange={setYamlContent} />
            </div>
          ) : (
            <div className="space-y-4">
              <div>
                <label className="block text-xs text-muted-foreground mb-1">Name</label>
                <input type="text" value={name} onChange={(e) => setName(e.target.value)}
                  placeholder="my-secret"
                  className="w-full h-8 px-3 rounded border border-border bg-background text-sm font-mono focus:outline-none focus:ring-1 focus:ring-ring" />
              </div>
              <div>
                <label className="block text-xs text-muted-foreground mb-1">Namespace</label>
                <select value={namespace} onChange={(e) => setNamespace(e.target.value)}
                  className="w-full h-8 px-3 rounded border border-border bg-background text-sm focus:outline-none focus:ring-1 focus:ring-ring">
                  {(namespaces || []).map((ns) => (
                    <option key={ns.name} value={ns.name}>{ns.name}</option>
                  ))}
                </select>
              </div>
              <div>
                <label className="block text-xs text-muted-foreground mb-1">Type</label>
                <select value={secretType} onChange={(e) => setSecretType(e.target.value)}
                  className="w-full h-8 px-3 rounded border border-border bg-background text-sm focus:outline-none focus:ring-1 focus:ring-ring">
                  {secretTypes.map((t) => (
                    <option key={t.value} value={t.value}>{t.label}</option>
                  ))}
                </select>
              </div>
              <div>
                <label className="block text-xs text-muted-foreground mb-2">Data (values will be base64-encoded)</label>
                <KeyValueEditor pairs={pairs} onChange={setPairs} keyPlaceholder="Key" valuePlaceholder="Value (plain text)" masked />
              </div>
            </div>
          )}
        </div>

        <div className="flex items-center justify-end gap-2 px-4 py-3 border-t border-border">
          <button onClick={onClose} className="h-8 px-3 rounded text-sm text-muted-foreground hover:text-foreground hover:bg-accent transition-colors">Cancel</button>
          <button onClick={handleSubmit} disabled={k8sCreate.isPending || (!name && mode === 'form')}
            className="inline-flex items-center gap-1.5 h-8 px-4 rounded text-sm font-medium bg-primary text-primary-foreground hover:bg-primary/90 disabled:opacity-50 transition-colors">
            {k8sCreate.isPending && <Loader2 className="h-3.5 w-3.5 animate-spin" />}
            Create
          </button>
        </div>
      </div>
    </div>
  );
}
