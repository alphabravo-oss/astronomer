'use client';

import { useState, useEffect } from 'react';
import { YamlEditor } from '@/components/ui/yaml-editor';
import { ModalShell } from '@/components/ui/modal-shell';
import { useK8sCreate } from '@/lib/hooks';
import { k8sTemplates } from '@/lib/k8s-templates';
import { Loader2 } from 'lucide-react';
import { toastApiError, toastError } from '@/lib/toast';

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
          toastError(`Unknown resource kind: ${kind}`);
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
      toastApiError('Invalid YAML', e);
    }
  };

  return (
    <ModalShell
      title={title}
      onClose={onClose}
      size="xl"
      panelClassName="w-[90vw] h-[80vh] max-w-4xl flex flex-col overflow-hidden"
      bodyClassName="flex-1 min-h-0 p-0 space-y-0"
      footer={(
        <div className="flex items-center justify-between">
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
      )}
    >
      <YamlEditor
        value={yamlContent}
        onChange={setYamlContent}
        className="h-full"
      />
    </ModalShell>
  );
}
