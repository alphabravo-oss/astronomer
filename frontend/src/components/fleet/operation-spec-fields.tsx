'use client';

import { useEffect, useState } from 'react';
import { useQuery } from '@tanstack/react-query';
import { getTools, listClusterTemplates } from '@/lib/api';
import { queryKeys } from '@/lib/hooks';
import type { FleetOperationType } from '@/lib/api/fleet-operations';

interface OperationSpecFieldsProps {
  operationType: FleetOperationType;
  onChange: (spec: Record<string, unknown> | undefined) => void;
}

const clusterTemplateKeys = {
  list: (params?: Record<string, unknown>) => ['cluster-templates', 'list', params] as const,
};

/**
 * Per-type operation_spec sub-form.
 *   - tool_* : { slug } picked from the tools catalog.
 *   - apply_template : { template_id } picked from cluster templates.
 *   - rotate_agent_token : no spec.
 */
export function OperationSpecFields({ operationType, onChange }: OperationSpecFieldsProps) {
  const isToolOp =
    operationType === 'tool_upgrade' ||
    operationType === 'tool_install' ||
    operationType === 'tool_uninstall';
  const isTemplateOp = operationType === 'apply_template';

  const tools = useQuery({
    queryKey: queryKeys.tools.list(),
    queryFn: getTools,
    enabled: isToolOp,
  });
  const templates = useQuery({
    queryKey: clusterTemplateKeys.list(),
    queryFn: () => listClusterTemplates(),
    enabled: isTemplateOp,
  });

  const [slug, setSlug] = useState('');
  const [templateId, setTemplateId] = useState('');

  // Reset selections when the operation type changes and re-emit spec.
  useEffect(() => {
    setSlug('');
    setTemplateId('');
    onChange(operationType === 'rotate_agent_token' ? undefined : {});
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [operationType]);

  useEffect(() => {
    if (isToolOp) onChange(slug ? { slug } : {});
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [slug, isToolOp]);

  useEffect(() => {
    if (isTemplateOp) onChange(templateId ? { template_id: templateId } : {});
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [templateId, isTemplateOp]);

  if (isToolOp) {
    return (
      <div className="space-y-1.5">
        <label className="text-sm font-medium text-foreground">Tool</label>
        <select
          aria-label="tool slug"
          value={slug}
          onChange={(e) => setSlug(e.target.value)}
          className="h-9 w-full rounded-md border border-border bg-background px-3 text-sm focus:outline-none focus:ring-1 focus:ring-ring"
        >
          <option value="">Select a tool…</option>
          {(tools.data ?? []).map((t) => (
            <option key={t.slug} value={t.slug}>
              {t.name} ({t.slug})
            </option>
          ))}
        </select>
        <p className="text-xs text-muted-foreground">
          The tool is applied on every matched cluster using its default preset.
        </p>
      </div>
    );
  }

  if (isTemplateOp) {
    return (
      <div className="space-y-1.5">
        <label className="text-sm font-medium text-foreground">Template</label>
        <select
          aria-label="template id"
          value={templateId}
          onChange={(e) => setTemplateId(e.target.value)}
          className="h-9 w-full rounded-md border border-border bg-background px-3 text-sm focus:outline-none focus:ring-1 focus:ring-ring"
        >
          <option value="">Select a template…</option>
          {(templates.data?.data ?? []).map((t) => (
            <option key={t.id} value={t.id}>
              {t.displayName || t.name}
            </option>
          ))}
        </select>
      </div>
    );
  }

  // rotate_agent_token — no spec.
  return (
    <p className="text-sm text-muted-foreground">
      This operation rotates the agent token on every matched cluster. No further
      configuration is required.
    </p>
  );
}
