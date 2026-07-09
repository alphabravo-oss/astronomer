'use client';

import { useMemo, useState } from 'react';
import { useQuery } from '@tanstack/react-query';
import yaml from 'js-yaml';
import * as apiClient from '@/lib/api';
import { queryKeys } from '@/lib/query-keys';
import { ModalShell } from '@/components/ui/modal-shell';
import { Loader2, FileCode2, SlidersHorizontal } from 'lucide-react';
import type { PermissionDecision } from '@/lib/permissions';
import type { ClusterTool, ToolFormField } from '@/types';
import { toastWarning } from '@/lib/toast';

interface ToolInstallModalProps {
  tool: ClusterTool;
  clusterId: string;
  preset: string;
  onConfirm: (valuesOverride: string | undefined) => void;
  onClose: () => void;
  installing?: boolean;
  confirmDecision?: PermissionDecision;
}

const EMPTY_TOOL_FIELDS: ToolFormField[] = [];

// setPath writes value into a nested object at a dot-path, creating intermediate
// objects as needed: setPath({}, "a.b.c", 1) => { a: { b: { c: 1 } } }.
function setPath(root: Record<string, unknown>, path: string, value: unknown) {
  const parts = path.split('.');
  let node = root;
  for (let i = 0; i < parts.length - 1; i++) {
    const k = parts[i];
    if (typeof node[k] !== 'object' || node[k] === null) node[k] = {};
    node = node[k] as Record<string, unknown>;
  }
  node[parts[parts.length - 1]] = value;
}

function coerce(field: ToolFormField, raw: string): unknown {
  if (field.type === 'number') {
    const n = Number(raw);
    return Number.isFinite(n) ? n : raw;
  }
  if (field.type === 'boolean') return raw === 'true';
  return raw;
}

// Build a values-override object from the form field values, including every
// field with a non-empty value (their real chart paths, so Helm accepts them).
function buildOverride(fields: ToolFormField[], values: Record<string, string>): Record<string, unknown> {
  const out: Record<string, unknown> = {};
  for (const f of fields) {
    const raw = values[f.path];
    if (raw === undefined || raw === '') continue;
    setPath(out, f.path, coerce(f, raw));
    if (f.type === 'storage' && f.storage_class_path && values[f.storage_class_path]) {
      setPath(out, f.storage_class_path, values[f.storage_class_path]);
    }
  }
  return out;
}

function groupFields(fields: ToolFormField[]): Array<[string, ToolFormField[]]> {
  const order = ['Scaling', 'Storage', 'Resources', 'Networking', 'General'];
  const byGroup = new Map<string, ToolFormField[]>();
  for (const f of fields) {
    const g = f.group || 'General';
    if (!byGroup.has(g)) byGroup.set(g, []);
    byGroup.get(g)!.push(f);
  }
  return Array.from(byGroup.entries()).sort(
    (a, b) => (order.indexOf(a[0]) + 1 || 99) - (order.indexOf(b[0]) + 1 || 99),
  );
}

function permissionDeniedReason(decision: PermissionDecision): string {
  return decision.disabledReason || decision.reason;
}

export function ToolInstallModal({
  tool,
  clusterId,
  preset,
  onConfirm,
  onClose,
  installing,
  confirmDecision,
}: ToolInstallModalProps) {
  const fields = tool.form_schema?.fields ?? EMPTY_TOOL_FIELDS;
  const hasForm = fields.length > 0;
  const [mode, setMode] = useState<'form' | 'yaml'>(hasForm ? 'form' : 'yaml');

  // Form state: path -> string value, seeded from schema defaults.
  const [values, setValues] = useState<Record<string, string>>(() => {
    const init: Record<string, string> = {};
    for (const f of fields) if (f.default !== undefined) init[f.path] = f.default;
    return init;
  });
  const [yamlText, setYamlText] = useState('');

  // Chart metadata (name/version/namespace) for the header.
  const { data: preview, isLoading } = useQuery({
    queryKey: queryKeys.tools.preview(tool.slug, clusterId, preset),
    queryFn: () => apiClient.previewToolInstall(tool.slug, { cluster_id: clusterId, preset }),
  });
  const chart = preview?.charts?.[0];

  const groups = useMemo(() => groupFields(fields), [fields]);

  const switchToYaml = () => {
    // Regenerate the YAML from the current form values so the two stay in sync.
    const override = buildOverride(fields, values);
    setYamlText(Object.keys(override).length ? yaml.dump(override) : '');
    setMode('yaml');
  };

  const confirmBlockedReason =
    confirmDecision && !confirmDecision.allowed ? permissionDeniedReason(confirmDecision) : undefined;

  const handleConfirm = () => {
    if (confirmBlockedReason) {
      toastWarning(confirmBlockedReason);
      return;
    }
    let override: string | undefined;
    if (mode === 'yaml') {
      override = yamlText.trim() || undefined;
    } else {
      const obj = buildOverride(fields, values);
      override = Object.keys(obj).length ? yaml.dump(obj) : undefined;
    }
    onConfirm(override);
  };

  return (
    <ModalShell
      title={`Install ${tool.name}`}
      onClose={onClose}
      size="lg"
      panelClassName="max-w-2xl max-h-[88vh] bg-popover flex flex-col overflow-hidden"
      bodyClassName="flex-1 overflow-y-auto"
      footerClassName="bg-muted/30"
      headerActions={
        chart ? (
          <p className="text-xs text-muted-foreground font-mono">
            {chart.chart_name}
            {chart.chart_version ? `@${chart.chart_version}` : ''} · {chart.namespace}
          </p>
        ) : undefined
      }
      footer={
        <div className="flex items-center justify-between gap-2">
          {hasForm ? (
            <button
              onClick={() => (mode === 'form' ? switchToYaml() : setMode('form'))}
              className="inline-flex items-center gap-1.5 h-9 px-3 rounded-lg border border-border text-xs font-medium
                text-muted-foreground hover:text-foreground hover:bg-accent transition-colors"
            >
              {mode === 'form' ? <FileCode2 className="h-3.5 w-3.5" /> : <SlidersHorizontal className="h-3.5 w-3.5" />}
              {mode === 'form' ? 'Edit YAML' : 'Back to form'}
            </button>
          ) : (
            <span />
          )}
          <div className="flex items-center gap-2">
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
        </div>
      }
    >
      {mode === 'form' ? (
        <div className="space-y-6">
          <p className="text-xs text-muted-foreground">
            Configure the common settings below, or switch to <span className="font-medium">Edit YAML</span> for full
            control. Anything you leave at its default is taken from the chart.
          </p>
          {groups.map(([group, groupFieldsList]) => (
            <section key={group} className="space-y-3">
              <h3 className="text-xs font-semibold uppercase tracking-wide text-muted-foreground">{group}</h3>
              <div className="space-y-3">
                {groupFieldsList.map((f) => (
                  <FormFieldRow
                    key={f.path}
                    field={f}
                    value={values[f.path] ?? ''}
                    classValue={f.storage_class_path ? values[f.storage_class_path] ?? '' : ''}
                    onChange={(v) => setValues((prev) => ({ ...prev, [f.path]: v }))}
                    onClassChange={(v) =>
                      f.storage_class_path &&
                      setValues((prev) => ({ ...prev, [f.storage_class_path as string]: v }))
                    }
                  />
                ))}
              </div>
            </section>
          ))}
        </div>
      ) : (
        <div className="space-y-1.5">
          <label className="text-sm font-medium text-foreground">Values override (YAML)</label>
          <p className="text-xs text-muted-foreground">Merged on top of the chart defaults and the selected preset.</p>
          <textarea
            value={yamlText}
            onChange={(e) => setYamlText(e.target.value)}
            rows={18}
            placeholder={'# e.g.\nreplicas: 2\nresources:\n  requests:\n    cpu: 100m'}
            className="w-full px-3 py-2 rounded-md border border-border bg-background text-sm font-mono
              placeholder:text-muted-foreground focus:outline-none focus:ring-1 focus:ring-ring resize-none"
          />
        </div>
      )}
    </ModalShell>
  );
}

function FormFieldRow({
  field,
  value,
  classValue,
  onChange,
  onClassChange,
}: {
  field: ToolFormField;
  value: string;
  classValue: string;
  onChange: (v: string) => void;
  onClassChange: (v: string) => void;
}) {
  const inputCls =
    'h-8 px-2 rounded-md border border-border bg-background text-sm focus:outline-none focus:ring-1 focus:ring-ring';
  return (
    <div className="grid grid-cols-[minmax(0,1fr)_220px] items-center gap-3">
      <div>
        <label className="text-sm text-foreground">{field.label}</label>
        {field.help && <p className="text-xs text-muted-foreground mt-0.5">{field.help}</p>}
        <p className="text-[10px] text-muted-foreground/70 font-mono mt-0.5">{field.path}</p>
      </div>
      {field.type === 'boolean' ? (
        <label className="inline-flex items-center gap-2 justify-self-end cursor-pointer">
          <input
            type="checkbox"
            checked={value === 'true'}
            onChange={(e) => onChange(e.target.checked ? 'true' : 'false')}
            className="h-4 w-4 rounded border-border"
          />
          <span className="text-xs text-muted-foreground">{value === 'true' ? 'Enabled' : 'Disabled'}</span>
        </label>
      ) : field.type === 'select' ? (
        <select value={value} onChange={(e) => onChange(e.target.value)} className={`${inputCls} w-full`}>
          {(field.options ?? []).map((opt) => (
            <option key={opt} value={opt}>
              {opt}
            </option>
          ))}
        </select>
      ) : field.type === 'storage' ? (
        <div className="flex gap-2">
          <input
            value={value}
            onChange={(e) => onChange(e.target.value)}
            placeholder={field.placeholder || '10Gi'}
            className={`${inputCls} w-24`}
          />
          <input
            value={classValue}
            onChange={(e) => onClassChange(e.target.value)}
            placeholder="storageClass"
            className={`${inputCls} flex-1`}
          />
        </div>
      ) : (
        <input
          type={field.type === 'number' ? 'number' : 'text'}
          value={value}
          onChange={(e) => onChange(e.target.value)}
          placeholder={field.placeholder}
          className={`${inputCls} w-full`}
        />
      )}
    </div>
  );
}
