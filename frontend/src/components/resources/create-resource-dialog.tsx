'use client';

import { useState, useEffect } from 'react';
import { YamlEditor } from '@/components/ui/yaml-editor';
import { useK8sCreate } from '@/lib/hooks';
import { k8sTemplates } from '@/lib/k8s-templates';
import { Loader2, X } from 'lucide-react';

interface CreateResourceDialogProps {
  open: boolean;
  onClose: () => void;
  clusterId: string;
  /** Resource type key from k8sTemplates (e.g. "deployment", "service") */
  templateKey: string;
  /** Display title */
  title: string;
  /** K8s API path to POST to (e.g. "apis/apps/v1/namespaces/default/deployments").
   *  If not provided, will be extracted from the YAML metadata. */
  apiPath?: string;
}

export function CreateResourceDialog({
  open,
  onClose,
  clusterId,
  templateKey,
  title,
  apiPath,
}: CreateResourceDialogProps) {
  const [yamlContent, setYamlContent] = useState('');
  const k8sCreate = useK8sCreate();

  useEffect(() => {
    if (open) {
      setYamlContent(k8sTemplates[templateKey] || '');
    }
  }, [open, templateKey]);

  useEffect(() => {
    if (!open) return;
    function handleKey(e: KeyboardEvent) { if (e.key === 'Escape') onClose(); }
    document.addEventListener('keydown', handleKey);
    return () => document.removeEventListener('keydown', handleKey);
  }, [open, onClose]);

  if (!open) return null;

  const handleCreate = async () => {
    const yaml = await import('js-yaml');
    try {
      const body = yaml.load(yamlContent) as {
        apiVersion?: string;
        kind?: string;
        metadata?: { name?: string; namespace?: string };
      };

      let path = apiPath;
      if (!path) {
        // Derive path from the parsed YAML
        const ns = body?.metadata?.namespace || 'default';
        const apiVersion = body?.apiVersion || '';
        const kind = body?.kind || '';

        const kindToPlural: Record<string, string> = {
          Deployment: 'deployments',
          StatefulSet: 'statefulsets',
          DaemonSet: 'daemonsets',
          Job: 'jobs',
          CronJob: 'cronjobs',
          Service: 'services',
          Ingress: 'ingresses',
          NetworkPolicy: 'networkpolicies',
          ConfigMap: 'configmaps',
          Secret: 'secrets',
          Namespace: 'namespaces',
          PodDisruptionBudget: 'poddisruptionbudgets',
          HorizontalPodAutoscaler: 'horizontalpodautoscalers',
          ServiceAccount: 'serviceaccounts',
          ResourceQuota: 'resourcequotas',
          LimitRange: 'limitranges',
        };

        const plural = kindToPlural[kind];
        if (!plural) {
          const { toast } = await import('sonner');
          toast.error(`Unknown resource kind: ${kind}`);
          return;
        }

        const apiBase = apiVersion.includes('/') ? `apis/${apiVersion}` : `api/${apiVersion}`;

        if (kind === 'Namespace') {
          path = `${apiBase}/${plural}`;
        } else {
          path = `${apiBase}/namespaces/${ns}/${plural}`;
        }
      }

      k8sCreate.mutate(
        { clusterId, path, body },
        { onSuccess: onClose }
      );
    } catch (e) {
      const { toast } = await import('sonner');
      toast.error(`Invalid YAML: ${(e as Error).message}`);
    }
  };

  return (
    <div className="fixed inset-0 z-50 flex items-center justify-center">
      <div className="absolute inset-0 bg-black/50 backdrop-blur-sm" onClick={onClose} />
      <div className="relative bg-card border border-border rounded-lg shadow-xl w-[90vw] max-w-4xl h-[80vh] flex flex-col animate-fade-in">
        <div className="flex items-center justify-between px-4 py-3 border-b border-border">
          <h3 className="text-sm font-semibold text-foreground">{title}</h3>
          <button onClick={onClose} className="p-1 rounded hover:bg-accent text-muted-foreground"><X className="h-4 w-4" /></button>
        </div>

        <div className="flex-1 min-h-0">
          <YamlEditor
            value={yamlContent}
            onChange={setYamlContent}
            className="h-full"
          />
        </div>

        <div className="flex items-center justify-between px-4 py-3 border-t border-border">
          <p className="text-xs text-muted-foreground">Edit the YAML template, then click Create.</p>
          <div className="flex items-center gap-2">
            <button onClick={onClose} className="h-8 px-3 rounded text-sm text-muted-foreground hover:text-foreground hover:bg-accent transition-colors">Cancel</button>
            <button onClick={handleCreate} disabled={k8sCreate.isPending}
              className="inline-flex items-center gap-1.5 h-8 px-4 rounded text-sm font-medium bg-primary text-primary-foreground hover:bg-primary/90 disabled:opacity-50 transition-colors">
              {k8sCreate.isPending && <Loader2 className="h-3.5 w-3.5 animate-spin" />}
              Create
            </button>
          </div>
        </div>
      </div>
    </div>
  );
}
