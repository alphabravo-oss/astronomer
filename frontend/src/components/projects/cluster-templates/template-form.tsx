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
 * Inputs are plain controlled `useState` per the project's no-react-hook-form
 * constraint. The same component handles edit by accepting an `initial`
 * snapshot and a `submitLabel`.
 */
import { useMemo, useState } from 'react';
import { ChevronDown, ChevronRight, Loader2, Plus, Trash2 } from 'lucide-react';
import { useTools } from '@/lib/hooks';
import { cn } from '@/lib/utils';
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

const defaultSpec: ClusterTemplateSpec = {
  environment: 'development',
  labels: [],
  tools: [],
  defaultProject: {
    name: '',
    podSecurityProfile: 'baseline',
    resourceQuotaCpu: null,
    resourceQuotaMemory: null,
    resourceQuotaPods: null,
    networkPolicyMode: 'isolated',
  },
  registrationPolicy: {
    tokenRotationDays: 90,
    requireApproval: false,
  },
};

export function TemplateForm({
  initial,
  isEdit,
  submitting,
  serverError,
  onSubmit,
  onCancel,
}: TemplateFormProps) {
  const [name, setName] = useState(initial?.name ?? '');
  const [displayName, setDisplayName] = useState(initial?.displayName ?? '');
  const [description, setDescription] = useState(initial?.description ?? '');
  const [spec, setSpec] = useState<ClusterTemplateSpec>(initial?.spec ?? defaultSpec);
  const [clientErrors, setClientErrors] = useState<string[]>([]);

  const update = <K extends keyof ClusterTemplateSpec>(key: K, value: ClusterTemplateSpec[K]) =>
    setSpec((prev) => ({ ...prev, [key]: value }));

  const updateDefaultProject = <K extends keyof ClusterTemplateSpec['defaultProject']>(
    key: K,
    value: ClusterTemplateSpec['defaultProject'][K],
  ) => setSpec((prev) => ({ ...prev, defaultProject: { ...prev.defaultProject, [key]: value } }));

  const updateRegistration = <K extends keyof ClusterTemplateSpec['registrationPolicy']>(
    key: K,
    value: ClusterTemplateSpec['registrationPolicy'][K],
  ) =>
    setSpec((prev) => ({
      ...prev,
      registrationPolicy: { ...prev.registrationPolicy, [key]: value },
    }));

  const handleSubmit = () => {
    const errors: string[] = [];
    if (!name.trim()) errors.push('Name is required');
    if (!displayName.trim()) errors.push('Display name is required');
    if (!isEdit && !/^[a-z0-9-]+$/.test(name.trim())) {
      errors.push('Name must be lowercase letters, digits, and dashes');
    }
    if (spec.registrationPolicy.tokenRotationDays <= 0) {
      errors.push('Token rotation days must be > 0');
    }
    if (errors.length) {
      setClientErrors(errors);
      return;
    }
    setClientErrors([]);
    onSubmit({
      name: name.trim(),
      displayName: displayName.trim(),
      description: description.trim() || undefined,
      spec,
    });
  };

  const allErrors = useMemo(() => {
    const out = [...clientErrors];
    if (serverError) out.unshift(serverError);
    return out;
  }, [clientErrors, serverError]);

  return (
    <div className="space-y-4">
      <Section title="Identity" defaultOpen>
        <div className="grid grid-cols-1 md:grid-cols-2 gap-4">
          <Field label="Name" required>
            <input
              type="text"
              value={name}
              disabled={isEdit}
              placeholder="prod-template"
              onChange={(e) =>
                setName(e.target.value.toLowerCase().replace(/[^a-z0-9-]/g, '-'))
              }
              className="text-input"
            />
          </Field>
          <Field label="Display name" required>
            <input
              type="text"
              value={displayName}
              placeholder="Production Template"
              onChange={(e) => setDisplayName(e.target.value)}
              className="text-input"
            />
          </Field>
        </div>
        <Field label="Description">
          <input
            type="text"
            value={description}
            placeholder="What does this template represent?"
            onChange={(e) => setDescription(e.target.value)}
            className="text-input"
          />
        </Field>
        <Field label="Environment">
          <select
            value={spec.environment}
            onChange={(e) => update('environment', e.target.value as ClusterTemplateSpec['environment'])}
            className="text-input"
          >
            {envOptions.map((opt) => (
              <option key={opt} value={opt}>
                {opt}
              </option>
            ))}
          </select>
        </Field>
      </Section>

      <Section title="Labels">
        <LabelsEditor value={spec.labels} onChange={(labels) => update('labels', labels)} />
      </Section>

      <Section title="Tools">
        <ToolsEditor value={spec.tools} onChange={(tools) => update('tools', tools)} />
      </Section>

      <Section title="Default project">
        <div className="grid grid-cols-1 md:grid-cols-2 gap-4">
          <Field
            label="Project name template"
            hint="Use {cluster} for substitution. Leave empty for auto-generation."
          >
            <input
              type="text"
              value={spec.defaultProject.name ?? ''}
              placeholder="default-{cluster}"
              onChange={(e) => updateDefaultProject('name', e.target.value)}
              className="text-input"
            />
          </Field>
          <Field label="Pod Security profile">
            <select
              value={spec.defaultProject.podSecurityProfile}
              onChange={(e) =>
                updateDefaultProject('podSecurityProfile', e.target.value as PodSecurityProfile)
              }
              className="text-input"
            >
              {psaOptions.map((opt) => (
                <option key={opt} value={opt}>
                  {opt}
                </option>
              ))}
            </select>
          </Field>
        </div>
        <div className="grid grid-cols-1 md:grid-cols-3 gap-4">
          <Field label="CPU quota" hint="Empty = unlimited">
            <input
              type="text"
              value={spec.defaultProject.resourceQuotaCpu ?? ''}
              placeholder="e.g. 4"
              onChange={(e) =>
                updateDefaultProject('resourceQuotaCpu', e.target.value.trim() || null)
              }
              className="text-input"
            />
          </Field>
          <Field label="Memory quota" hint="Empty = unlimited">
            <input
              type="text"
              value={spec.defaultProject.resourceQuotaMemory ?? ''}
              placeholder="e.g. 8Gi"
              onChange={(e) =>
                updateDefaultProject('resourceQuotaMemory', e.target.value.trim() || null)
              }
              className="text-input"
            />
          </Field>
          <Field label="Pod quota" hint="Empty = unlimited">
            <input
              type="text"
              value={spec.defaultProject.resourceQuotaPods != null
                ? String(spec.defaultProject.resourceQuotaPods)
                : ''}
              placeholder="e.g. 50"
              onChange={(e) =>
                updateDefaultProject(
                  'resourceQuotaPods',
                  e.target.value.trim() ? Number(e.target.value.trim()) : null,
                )
              }
              className="text-input"
            />
          </Field>
        </div>
        <Field label="Network Policy mode">
          <select
            value={spec.defaultProject.networkPolicyMode}
            onChange={(e) =>
              updateDefaultProject('networkPolicyMode', e.target.value as NetworkPolicyMode)
            }
            className="text-input"
          >
            {netpolOptions.map((opt) => (
              <option key={opt} value={opt}>
                {opt}
              </option>
            ))}
          </select>
        </Field>
      </Section>

      <Section title="Registration policy">
        <div className="grid grid-cols-1 md:grid-cols-2 gap-4">
          <Field label="Token rotation (days)" required>
            <input
              type="number"
              min={1}
              value={spec.registrationPolicy.tokenRotationDays}
              onChange={(e) =>
                updateRegistration('tokenRotationDays', Math.max(1, Number(e.target.value || 1)))
              }
              className="text-input"
            />
          </Field>
          <Field label="Require approval" hint="Enable to gate auto-registration on operator review.">
            <label className="inline-flex items-center gap-2 mt-1.5 text-sm">
              <input
                type="checkbox"
                checked={!!spec.registrationPolicy.requireApproval}
                onChange={(e) => updateRegistration('requireApproval', e.target.checked)}
                className="rounded border-border"
              />
              <span className="text-muted-foreground">Manual approval required</span>
            </label>
          </Field>
        </div>
      </Section>

      {allErrors.length > 0 && (
        <div className="rounded-lg border border-status-error/40 bg-status-error/10 p-3 space-y-1">
          {allErrors.map((err, i) => (
            <p key={i} className="text-xs text-status-error">
              {err}
            </p>
          ))}
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
          onClick={handleSubmit}
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
// Helpers — Section / Field shell + sub-editors
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
      {open && <div className="px-5 pb-5 pt-2 space-y-4">{children}</div>}
    </div>
  );
}

function Field({
  label,
  hint,
  required,
  children,
}: {
  label: string;
  hint?: string;
  required?: boolean;
  children: React.ReactNode;
}) {
  return (
    <div className="space-y-1.5">
      <label className="text-sm font-medium text-foreground">
        {label}
        {required && <span className="text-status-error"> *</span>}
      </label>
      {/* Tailwind utility passthrough: child input components reference the
          shared `text-input` class declared in globals.css OR fall back to
          locally-scoped classes. We re-apply the canonical input styling
          here so children stay simple. */}
      <div className="[&_.text-input]:w-full [&_.text-input]:h-9 [&_.text-input]:px-3 [&_.text-input]:rounded-md [&_.text-input]:border [&_.text-input]:border-border [&_.text-input]:bg-background [&_.text-input]:text-sm [&_.text-input]:placeholder:text-muted-foreground [&_.text-input]:focus:outline-none [&_.text-input]:focus:ring-1 [&_.text-input]:focus:ring-ring [&_.text-input]:disabled:opacity-60">
        {children}
      </div>
      {hint && <p className="text-xs text-muted-foreground">{hint}</p>}
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
