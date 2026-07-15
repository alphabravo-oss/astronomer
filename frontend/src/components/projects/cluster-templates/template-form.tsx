'use client';

/**
 * Cluster Template form — shared between the New page and the Edit page.
 *
 * The spec is large, so the form breaks into collapsible sections:
 *   • Identity (name, displayName, description, environment)
 *   • Labels (key/value pairs)
 *   • Tools (multi-select from the catalog with per-tool preset + values override)
 *   • Default project (PSS, quotas, netpol, name)
 *   • Registration policy (token rotation days, approval gate)
 *
 * Inputs are TanStack Form fields from the shared kit (`useAppForm`);
 * sections hide (not unmount) when collapsed so field validators keep
 * running. The Labels/Tools list editors stay plain controlled components
 * wired in as fields. The same component handles edit by accepting an
 * `initial` snapshot.
 */
import { useMemo, useState } from 'react';
import { ChevronDown, ChevronRight, Loader2, Plus, Trash2 } from 'lucide-react';
import { useTools } from '@/lib/hooks';
import { cn } from '@/lib/utils';
import { useAppForm } from '@/lib/form';
import type {
  ClusterTemplateWriteRequest,
  ClusterTemplateSpec,
  ClusterTemplateLabel,
  ClusterTemplateToolBinding,
  PodSecurityProfile,
  NetworkPolicyMode,
} from '@/lib/api/project-detail';

interface TemplateFormProps {
  initial?: {
    name: string;
    displayName: string;
    description?: string;
    spec: ClusterTemplateSpec;
  };
  isEdit?: boolean;
  submitting?: boolean;
  serverError?: string | null;
  onSubmit: (body: ClusterTemplateWriteRequest) => void;
  onCancel?: () => void;
}

const psaOptions: PodSecurityProfile[] = ['privileged', 'baseline', 'restricted'];
const netpolOptions: NetworkPolicyMode[] = ['isolated', 'allow-same-project', 'none'];
const envOptions: ClusterTemplateSpec['environment'][] = [
  'development',
  'staging',
  'production',
  'other',
];

// This form's inputs are one notch tighter than the kit default — merged
// over the kit's base input class (twMerge, later wins).
const tplInputClassName = 'h-9 rounded-md focus:ring-1';

export function TemplateForm({
  initial,
  isEdit,
  submitting,
  serverError,
  onSubmit,
  onCancel,
}: TemplateFormProps) {
  const form = useAppForm({
    defaultValues: {
      name: initial?.name ?? '',
      displayName: initial?.displayName ?? '',
      description: initial?.description ?? '',
      environment: initial?.spec.environment ?? ('development' as const),
      labels: initial?.spec.labels ?? ([] as ClusterTemplateLabel[]),
      tools: initial?.spec.tools ?? ([] as ClusterTemplateToolBinding[]),
      projectName: initial?.spec.defaultProject.name ?? '',
      podSecurityProfile: initial?.spec.defaultProject.podSecurityProfile ?? ('baseline' as const),
      // Quotas are kept as strings in form state and converted at submit
      // (empty = unlimited = null on the wire, exactly as before).
      resourceQuotaCpu: initial?.spec.defaultProject.resourceQuotaCpu ?? '',
      resourceQuotaMemory: initial?.spec.defaultProject.resourceQuotaMemory ?? '',
      resourceQuotaPods:
        initial?.spec.defaultProject.resourceQuotaPods != null
          ? String(initial.spec.defaultProject.resourceQuotaPods)
          : '',
      networkPolicyMode: initial?.spec.defaultProject.networkPolicyMode ?? ('isolated' as const),
      tokenRotationDays: initial?.spec.registrationPolicy.tokenRotationDays ?? 90,
      requireApproval: initial?.spec.registrationPolicy.requireApproval ?? false,
    },
    onSubmit: ({ value }) => {
      onSubmit({
        name: value.name.trim(),
        displayName: value.displayName.trim(),
        description: value.description.trim() || undefined,
        spec: {
          environment: value.environment,
          labels: value.labels,
          tools: value.tools,
          defaultProject: {
            name: value.projectName,
            podSecurityProfile: value.podSecurityProfile,
            resourceQuotaCpu: value.resourceQuotaCpu.trim() || null,
            resourceQuotaMemory: value.resourceQuotaMemory.trim() || null,
            resourceQuotaPods: value.resourceQuotaPods.trim()
              ? Number(value.resourceQuotaPods.trim())
              : null,
            networkPolicyMode: value.networkPolicyMode,
          },
          registrationPolicy: {
            tokenRotationDays: value.tokenRotationDays,
            requireApproval: value.requireApproval,
          },
        },
      });
    },
  });

  return (
    <div className="space-y-4">
      <Section title="Identity" defaultOpen>
        <div className="grid grid-cols-1 md:grid-cols-2 gap-4">
          <form.AppField
            name="name"
            validators={{
              onSubmit: ({ value }) => {
                if (!value.trim()) return 'Name is required';
                if (!isEdit && !/^[a-z0-9-]+$/.test(value.trim())) {
                  return 'Name must be lowercase letters, digits, and dashes';
                }
                return undefined;
              },
            }}
          >
            {(field) => (
              <field.TextField
                label="Name"
                required
                disabled={isEdit}
                placeholder="prod-template"
                transform={(v) => v.toLowerCase().replace(/[^a-z0-9-]/g, '-')}
                className={tplInputClassName}
              />
            )}
          </form.AppField>
          <form.AppField
            name="displayName"
            validators={{
              onSubmit: ({ value }) => (!value.trim() ? 'Display name is required' : undefined),
            }}
          >
            {(field) => (
              <field.TextField
                label="Display name"
                required
                placeholder="Production Template"
                className={tplInputClassName}
              />
            )}
          </form.AppField>
        </div>
        <form.AppField name="description">
          {(field) => (
            <field.TextField
              label="Description"
              placeholder="What does this template represent?"
              className={tplInputClassName}
            />
          )}
        </form.AppField>
        <form.AppField name="environment">
          {(field) => (
            <field.SelectField label="Environment" className={tplInputClassName}>
              {envOptions.map((opt) => (
                <option key={opt} value={opt}>
                  {opt}
                </option>
              ))}
            </field.SelectField>
          )}
        </form.AppField>
      </Section>

      <Section title="Labels">
        <form.AppField name="labels">
          {(field) => <LabelsEditor value={field.state.value} onChange={field.handleChange} />}
        </form.AppField>
      </Section>

      <Section title="Tools">
        <form.AppField name="tools">
          {(field) => <ToolsEditor value={field.state.value} onChange={field.handleChange} />}
        </form.AppField>
      </Section>

      <Section title="Default project">
        <div className="grid grid-cols-1 md:grid-cols-2 gap-4">
          <form.AppField name="projectName">
            {(field) => (
              <field.TextField
                label="Project name template"
                helper="Use {cluster} for substitution. Leave empty for auto-generation."
                placeholder="default-{cluster}"
                className={tplInputClassName}
              />
            )}
          </form.AppField>
          <form.AppField name="podSecurityProfile">
            {(field) => (
              <field.SelectField label="Pod Security profile" className={tplInputClassName}>
                {psaOptions.map((opt) => (
                  <option key={opt} value={opt}>
                    {opt}
                  </option>
                ))}
              </field.SelectField>
            )}
          </form.AppField>
        </div>
        <div className="grid grid-cols-1 md:grid-cols-3 gap-4">
          <form.AppField name="resourceQuotaCpu">
            {(field) => (
              <field.TextField
                label="CPU quota"
                helper="Empty = unlimited"
                placeholder="e.g. 4"
                className={tplInputClassName}
              />
            )}
          </form.AppField>
          <form.AppField name="resourceQuotaMemory">
            {(field) => (
              <field.TextField
                label="Memory quota"
                helper="Empty = unlimited"
                placeholder="e.g. 8Gi"
                className={tplInputClassName}
              />
            )}
          </form.AppField>
          <form.AppField name="resourceQuotaPods">
            {(field) => (
              <field.TextField
                label="Pod quota"
                helper="Empty = unlimited"
                placeholder="e.g. 50"
                className={tplInputClassName}
              />
            )}
          </form.AppField>
        </div>
        <form.AppField name="networkPolicyMode">
          {(field) => (
            <field.SelectField label="Network Policy mode" className={tplInputClassName}>
              {netpolOptions.map((opt) => (
                <option key={opt} value={opt}>
                  {opt}
                </option>
              ))}
            </field.SelectField>
          )}
        </form.AppField>
      </Section>

      <Section title="Registration policy">
        <div className="grid grid-cols-1 md:grid-cols-2 gap-4">
          <form.AppField
            name="tokenRotationDays"
            validators={{
              onSubmit: ({ value }) =>
                !(value > 0) ? 'Token rotation days must be > 0' : undefined,
            }}
          >
            {(field) => (
              <field.NumberField
                label="Token rotation (days)"
                required
                min={1}
                className={tplInputClassName}
              />
            )}
          </form.AppField>
          <form.AppField name="requireApproval">
            {(field) => (
              <field.CheckboxField
                label="Require approval"
                helper="Enable to gate auto-registration on operator review."
              />
            )}
          </form.AppField>
        </div>
      </Section>

      {serverError && (
        <div className="rounded-lg border border-status-error/40 bg-status-error/10 p-3 space-y-1">
          <p className="text-xs text-status-error">{serverError}</p>
        </div>
      )}

      <div className="flex justify-end gap-2 pt-2">
        {onCancel && (
          <button
            type="button"
            onClick={onCancel}
            className="h-9 px-4 rounded-lg border border-border text-sm font-medium text-muted-foreground hover:text-foreground hover:bg-accent transition-colors"
          >
            Cancel
          </button>
        )}
        <button
          type="button"
          onClick={() => void form.handleSubmit()}
          disabled={submitting}
          className={cn(
            'inline-flex items-center gap-2 h-9 px-4 rounded-lg bg-primary text-primary-foreground text-sm font-medium hover:opacity-90 transition-opacity',
            submitting && 'opacity-50 cursor-wait',
          )}
        >
          {submitting && <Loader2 className="h-3.5 w-3.5 animate-spin" />}
          {isEdit ? 'Save template' : 'Create template'}
        </button>
      </div>
    </div>
  );
}

// ============================================================
// Helpers — Section shell + sub-editors
// ============================================================

function Section({
  title,
  defaultOpen,
  children,
}: {
  title: string;
  defaultOpen?: boolean;
  children: React.ReactNode;
}) {
  const [open, setOpen] = useState(!!defaultOpen);
  return (
    <div className="rounded-xl border border-border bg-card overflow-hidden">
      <button
        type="button"
        onClick={() => setOpen((v) => !v)}
        className="w-full flex items-center justify-between px-5 py-3 hover:bg-accent/30 transition-colors"
      >
        <span className="text-sm font-medium text-foreground">{title}</span>
        {open ? (
          <ChevronDown className="h-4 w-4 text-muted-foreground" />
        ) : (
          <ChevronRight className="h-4 w-4 text-muted-foreground" />
        )}
      </button>
      {/* Hidden (not unmounted) when collapsed so field validators keep running. */}
      <div className={cn('px-5 pb-5 pt-2 space-y-4', !open && 'hidden')}>{children}</div>
    </div>
  );
}

// ----- Labels (key/value pairs) -----

function LabelsEditor({
  value,
  onChange,
}: {
  value: ClusterTemplateLabel[];
  onChange: (next: ClusterTemplateLabel[]) => void;
}) {
  const updateAt = (i: number, patch: Partial<ClusterTemplateLabel>) =>
    onChange(value.map((l, idx) => (idx === i ? { ...l, ...patch } : l)));
  const remove = (i: number) => onChange(value.filter((_, idx) => idx !== i));
  const add = () => onChange([...value, { key: '', value: '' }]);

  return (
    <div className="space-y-2">
      {value.length === 0 && (
        <p className="text-xs text-muted-foreground">No labels yet.</p>
      )}
      {value.map((label, i) => (
        <div key={i} className="grid grid-cols-[1fr_1fr_auto] gap-2 items-center">
          <input
            type="text"
            value={label.key}
            placeholder="key"
            onChange={(e) => updateAt(i, { key: e.target.value })}
            className="w-full h-9 px-3 rounded-md border border-border bg-background text-sm font-mono placeholder:text-muted-foreground focus:outline-none focus:ring-1 focus:ring-ring"
          />
          <input
            type="text"
            value={label.value}
            placeholder="value"
            onChange={(e) => updateAt(i, { value: e.target.value })}
            className="w-full h-9 px-3 rounded-md border border-border bg-background text-sm font-mono placeholder:text-muted-foreground focus:outline-none focus:ring-1 focus:ring-ring"
          />
          <button
            type="button"
            onClick={() => remove(i)}
            className="p-1.5 rounded text-muted-foreground hover:text-status-error hover:bg-status-error/10 transition-colors"
            title="Remove label"
          >
            <Trash2 className="h-3.5 w-3.5" />
          </button>
        </div>
      ))}
      <button
        type="button"
        onClick={add}
        className="inline-flex items-center gap-1.5 h-8 px-3 rounded-md border border-border text-xs text-muted-foreground hover:text-foreground hover:bg-accent transition-colors"
      >
        <Plus className="h-3 w-3" />
        Add label
      </button>
    </div>
  );
}

// ----- Tools (multi-select from catalog) -----

function ToolsEditor({
  value,
  onChange,
}: {
  value: ClusterTemplateToolBinding[];
  onChange: (next: ClusterTemplateToolBinding[]) => void;
}) {
  const { data: catalog = [], isLoading } = useTools();

  const remainingTools = useMemo(
    () => catalog.filter((t) => !value.some((v) => v.slug === t.slug)),
    [catalog, value],
  );
  const [pending, setPending] = useState('');

  const add = () => {
    if (!pending) return;
    const tool = catalog.find((t) => t.slug === pending);
    const firstPreset = tool ? Object.keys(tool.presets || {})[0] : undefined;
    onChange([...value, { slug: pending, preset: firstPreset, valuesOverride: '' }]);
    setPending('');
  };
  const remove = (slug: string) => onChange(value.filter((v) => v.slug !== slug));
  const updateAt = (slug: string, patch: Partial<ClusterTemplateToolBinding>) =>
    onChange(value.map((v) => (v.slug === slug ? { ...v, ...patch } : v)));

  return (
    <div className="space-y-3">
      {isLoading && (
        <div className="flex items-center gap-2 text-xs text-muted-foreground">
          <Loader2 className="h-3.5 w-3.5 animate-spin" /> Loading tools catalog…
        </div>
      )}
      {value.length === 0 && !isLoading && (
        <p className="text-xs text-muted-foreground">No tools selected.</p>
      )}

      {value.map((binding) => {
        const tool = catalog.find((t) => t.slug === binding.slug);
        const presetNames = tool ? Object.keys(tool.presets || {}) : [];
        return (
          <div key={binding.slug} className="rounded-lg border border-border bg-background p-3 space-y-3">
            <div className="flex items-center justify-between">
              <div className="min-w-0">
                <p className="text-sm font-medium text-foreground">{tool?.name || binding.slug}</p>
                {tool?.description && (
                  <p className="text-xs text-muted-foreground truncate max-w-[400px]">
                    {tool.description}
                  </p>
                )}
              </div>
              <button
                type="button"
                onClick={() => remove(binding.slug)}
                className="p-1.5 rounded text-muted-foreground hover:text-status-error hover:bg-status-error/10 transition-colors"
                title="Remove tool"
              >
                <Trash2 className="h-3.5 w-3.5" />
              </button>
            </div>
            <div className="grid grid-cols-1 md:grid-cols-3 gap-3">
              <div className="space-y-1.5">
                <label className="text-xs font-medium text-foreground">Preset</label>
                <select
                  value={binding.preset || ''}
                  onChange={(e) => updateAt(binding.slug, { preset: e.target.value || undefined })}
                  className="w-full h-9 px-3 rounded-md border border-border bg-background text-sm focus:outline-none focus:ring-1 focus:ring-ring"
                >
                  <option value="">(no preset)</option>
                  {presetNames.map((p) => (
                    <option key={p} value={p}>
                      {p}
                    </option>
                  ))}
                </select>
              </div>
              <div className="space-y-1.5 md:col-span-2">
                <label className="text-xs font-medium text-foreground">Values override (YAML)</label>
                <textarea
                  value={binding.valuesOverride || ''}
                  rows={3}
                  placeholder={'# YAML keys overlay the preset\nresources:\n  requests:\n    cpu: 100m'}
                  onChange={(e) => updateAt(binding.slug, { valuesOverride: e.target.value })}
                  className="w-full px-3 py-2 rounded-md border border-border bg-background text-xs font-mono placeholder:text-muted-foreground focus:outline-none focus:ring-1 focus:ring-ring resize-y"
                />
              </div>
            </div>
          </div>
        );
      })}

      <div className="flex items-center gap-2">
        <select
          value={pending}
          onChange={(e) => setPending(e.target.value)}
          className="flex-1 h-9 px-3 rounded-md border border-border bg-background text-sm focus:outline-none focus:ring-1 focus:ring-ring"
        >
          <option value="">Add a tool…</option>
          {remainingTools.map((t) => (
            <option key={t.slug} value={t.slug}>
              {t.name}
            </option>
          ))}
        </select>
        <button
          type="button"
          onClick={add}
          disabled={!pending}
          className="inline-flex items-center gap-1 h-9 px-3 rounded-md border border-border text-sm text-muted-foreground hover:text-foreground hover:bg-accent transition-colors disabled:opacity-50"
        >
          <Plus className="h-3.5 w-3.5" />
          Add
        </button>
      </div>
    </div>
  );
}
