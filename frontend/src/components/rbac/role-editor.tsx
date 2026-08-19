'use client';

import { useState } from 'react';
import { Plus, Trash2 } from 'lucide-react';
import { ModalShell } from '@/components/ui/modal-shell';
import { ActionButton } from '@/components/ui/action-button';
import { Input } from '@/components/ui/input';
import { Select } from '@/components/ui/select';
import { cn } from '@/lib/utils';
import { toastError } from '@/lib/toast';
import { useCreateRole } from '@/lib/hooks';

interface RoleEditorProps {
  onClose: () => void;
  defaultScope?: 'global' | 'cluster' | 'project';
  initialRole?: {
    name: string;
    displayName: string;
    description: string;
    scope: 'global' | 'cluster' | 'project';
    rules: PolicyRuleInput[];
  };
}

interface PolicyRuleInput {
  resource: string;
  verbs: string[];
}

interface CRDGrantInput {
  apiGroup: string;
  resource: string;
  verbs: string[];
}

const PLATFORM_RESOURCES = [
  'clusters',
  'projects',
  'workloads',
  'pods',
  'custom_resources',
  'secrets',
  'configmaps',
  'services',
  'ingresses',
  'storage',
  'nodes',
  'monitoring',
  'alerts',
  'catalog',
  'logging',
  'backups',
  'security',
  'rbac',
  'users',
  'audit_logs',
  'agents',
];

const PLATFORM_VERBS = ['read', 'list', 'watch', 'create', 'update', 'delete'];
const CRD_VERBS = ['read', 'list', 'watch', 'create', 'update', 'delete'];

export function RoleEditor({ onClose, initialRole, defaultScope = 'cluster' }: RoleEditorProps) {
  const createRole = useCreateRole();
  const [form, setForm] = useState(() => splitInitial(initialRole, defaultScope));

  const addPlatform = () => {
    setForm((f) => ({ ...f, platform: [...f.platform, { resource: 'workloads', verbs: ['read', 'list'] }] }));
  };
  const removePlatform = (index: number) => {
    setForm((f) => ({ ...f, platform: f.platform.filter((_, i) => i !== index) }));
  };
  const updatePlatform = (index: number, updates: Partial<PolicyRuleInput>) => {
    setForm((f) => ({
      ...f,
      platform: f.platform.map((rule, i) => (i === index ? { ...rule, ...updates } : rule)),
    }));
  };
  const togglePlatformVerb = (index: number, verb: string) => {
    setForm((f) => ({
      ...f,
      platform: f.platform.map((rule, i) => {
        if (i !== index) return rule;
        const verbs = rule.verbs.includes(verb) ? rule.verbs.filter((v) => v !== verb) : [...rule.verbs, verb];
        return { ...rule, verbs };
      }),
    }));
  };

  const addCRD = () => {
    setForm((f) => ({ ...f, crd: [...f.crd, { apiGroup: '', resource: '', verbs: ['read', 'list'] }] }));
  };
  const removeCRD = (index: number) => {
    setForm((f) => ({ ...f, crd: f.crd.filter((_, i) => i !== index) }));
  };
  const updateCRD = (index: number, updates: Partial<CRDGrantInput>) => {
    setForm((f) => ({
      ...f,
      crd: f.crd.map((rule, i) => (i === index ? { ...rule, ...updates } : rule)),
    }));
  };
  const toggleCRDVerb = (index: number, verb: string) => {
    setForm((f) => ({
      ...f,
      crd: f.crd.map((rule, i) => {
        if (i !== index) return rule;
        const verbs = rule.verbs.includes(verb) ? rule.verbs.filter((v) => v !== verb) : [...rule.verbs, verb];
        return { ...rule, verbs };
      }),
    }));
  };

  const handleSave = async () => {
    if (!form.name || !form.displayName) {
      toastError('Name and display name are required');
      return;
    }
    const platform = form.platform.filter((r) => r.resource && r.verbs.length > 0);
    const crd = form.scope === 'global' ? [] : form.crd.filter((r) => r.resource && r.verbs.length > 0);
    if (platform.length === 0 && crd.length === 0) {
      toastError('Add at least one platform permission or CRD grant');
      return;
    }

    const rules = [
      ...platform.map((r) => ({ resource: r.resource, verbs: r.verbs })),
      ...crd.map((r) => ({
        resource: r.resource,
        verbs: r.verbs,
        api_groups: [r.apiGroup.trim()],
      })),
    ];

    try {
      await createRole.mutateAsync({
        scope: form.scope,
        name: form.name,
        displayName: form.displayName,
        description: form.description || undefined,
        rules,
      });
      onClose();
    } catch {
      // Error is handled by the mutation's onError callback
    }
  };

  return (
    <ModalShell
      title={initialRole ? 'Edit Role' : 'Create Role'}
      onClose={onClose}
      size="lg"
      panelClassName="max-h-[85vh] bg-popover flex flex-col overflow-hidden"
      bodyClassName="flex-1 overflow-y-auto space-y-5"
      footerClassName="flex items-center justify-end gap-2"
      footer={
        <>
          <ActionButton onClick={onClose}>Cancel</ActionButton>
          <ActionButton intent="primary" loading={createRole.isPending} onClick={() => void handleSave()}>
            {initialRole ? 'Update Role' : 'Create Role'}
          </ActionButton>
        </>
      }
    >
      <div className="grid grid-cols-2 gap-4">
        <div className="space-y-1.5">
          <label className="text-sm font-medium text-foreground">Name</label>
          <Input
            type="text"
            value={form.name}
            onChange={(e) => setForm((f) => ({ ...f, name: e.target.value.toLowerCase().replace(/[^a-z0-9-]/g, '-') }))}
            placeholder="role-name"
          />
        </div>
        <div className="space-y-1.5">
          <label className="text-sm font-medium text-foreground">Display Name</label>
          <Input
            type="text"
            value={form.displayName}
            onChange={(e) => setForm((f) => ({ ...f, displayName: e.target.value }))}
            placeholder="My Custom Role"
          />
        </div>
      </div>

      <div className="space-y-1.5">
        <label className="text-sm font-medium text-foreground">Description</label>
        <Input
          type="text"
          value={form.description}
          onChange={(e) => setForm((f) => ({ ...f, description: e.target.value }))}
          placeholder="Describe this role's purpose"
        />
      </div>

      <div className="space-y-1.5">
        <label className="text-sm font-medium text-foreground">Scope</label>
        <div className="flex gap-2">
          {(['global', 'cluster', 'project'] as const).map((scope) => (
            <button
              key={scope}
              onClick={() => setForm((f) => ({ ...f, scope }))}
              className={cn(
                'px-3 py-1.5 rounded-md text-sm font-medium transition-colors capitalize',
                form.scope === scope
                  ? 'bg-primary text-primary-foreground'
                  : 'bg-muted text-muted-foreground hover:text-foreground',
              )}
            >
              {scope}
            </button>
          ))}
        </div>
      </div>

      <RuleSection
        title="Platform permissions"
        hint="Astronomer resources this role can use (workloads, backups, custom resources, …)."
        onAdd={addPlatform}
        addLabel="Add permission"
      >
        {form.platform.map((rule, idx) => (
          <div key={idx} className="rounded-lg border border-border p-4 space-y-3 bg-card">
            <div className="flex items-center justify-between">
              <span className="text-xs font-medium text-muted-foreground">Permission {idx + 1}</span>
              {form.platform.length > 1 && (
                <button onClick={() => removePlatform(idx)} className="text-muted-foreground hover:text-status-error">
                  <Trash2 className="h-3.5 w-3.5" />
                </button>
              )}
            </div>
            <div className="space-y-1.5">
              <label className="text-xs text-muted-foreground">Resource</label>
              <Select
                value={rule.resource}
                onChange={(e) => updatePlatform(idx, { resource: e.target.value })}
                className="h-8 text-xs"
              >
                {PLATFORM_RESOURCES.map((res) => (
                  <option key={res} value={res}>
                    {res}
                  </option>
                ))}
              </Select>
            </div>
            <VerbPills verbs={PLATFORM_VERBS} selected={rule.verbs} onToggle={(v) => togglePlatformVerb(idx, v)} />
          </div>
        ))}
      </RuleSection>

      {form.scope !== 'global' && (
        <RuleSection
          title="CRD grants"
          hint="Extra allow for a single Custom Resource when a platform permission is too coarse. Bind this role to a user to apply the grant."
          onAdd={addCRD}
          addLabel="Add CRD grant"
        >
          {form.crd.length === 0 && (
            <p className="text-xs text-muted-foreground">No CRD grants. Optional — most roles only need platform permissions.</p>
          )}
          {form.crd.map((rule, idx) => (
            <div key={idx} className="rounded-lg border border-border p-4 space-y-3 bg-card">
              <div className="flex items-center justify-between">
                <span className="text-xs font-medium text-muted-foreground">Grant {idx + 1}</span>
                <button onClick={() => removeCRD(idx)} className="text-muted-foreground hover:text-status-error">
                  <Trash2 className="h-3.5 w-3.5" />
                </button>
              </div>
              <div className="grid grid-cols-2 gap-3">
                <div className="space-y-1.5">
                  <label className="text-xs text-muted-foreground">API group</label>
                  <Input
                    value={rule.apiGroup}
                    onChange={(e) => updateCRD(idx, { apiGroup: e.target.value })}
                    placeholder="cert-manager.io (empty = core)"
                    className="h-8 px-2.5 text-xs font-mono"
                  />
                </div>
                <div className="space-y-1.5">
                  <label className="text-xs text-muted-foreground">Resource</label>
                  <Input
                    value={rule.resource}
                    onChange={(e) => updateCRD(idx, { resource: e.target.value })}
                    placeholder="certificates"
                    className="h-8 px-2.5 text-xs font-mono"
                  />
                </div>
              </div>
              <VerbPills verbs={CRD_VERBS} selected={rule.verbs} onToggle={(v) => toggleCRDVerb(idx, v)} />
            </div>
          ))}
        </RuleSection>
      )}
    </ModalShell>
  );
}

function RuleSection({
  title,
  hint,
  onAdd,
  addLabel,
  children,
}: {
  title: string;
  hint: string;
  onAdd: () => void;
  addLabel: string;
  children: React.ReactNode;
}) {
  return (
    <div className="space-y-3">
      <div className="flex items-start justify-between gap-3">
        <div>
          <label className="text-sm font-medium text-foreground">{title}</label>
          <p className="mt-0.5 text-xs text-muted-foreground">{hint}</p>
        </div>
        <ActionButton size="sm" intent="ghost" icon={<Plus className="h-3 w-3" />} onClick={onAdd}>
          {addLabel}
        </ActionButton>
      </div>
      {children}
    </div>
  );
}

function VerbPills({
  verbs,
  selected,
  onToggle,
}: {
  verbs: string[];
  selected: string[];
  onToggle: (verb: string) => void;
}) {
  return (
    <div className="space-y-1.5">
      <label className="text-xs text-muted-foreground">Verbs</label>
      <div className="flex flex-wrap gap-1.5">
        {verbs.map((verb) => (
          <button
            key={verb}
            type="button"
            onClick={() => onToggle(verb)}
            className={cn(
              'px-2.5 py-1 rounded text-xs font-medium transition-colors',
              selected.includes(verb)
                ? 'bg-primary text-primary-foreground'
                : 'bg-muted text-muted-foreground hover:text-foreground',
            )}
          >
            {verb}
          </button>
        ))}
      </div>
    </div>
  );
}

function splitInitial(
  initial?: RoleEditorProps['initialRole'],
  defaultScope: 'global' | 'cluster' | 'project' = 'cluster',
) {
  const platform: PolicyRuleInput[] = [];
  const crd: CRDGrantInput[] = [];
  for (const rule of initial?.rules ?? []) {
    const rec = rule as PolicyRuleInput & { api_groups?: string[]; apiGroups?: string[]; resources?: string[] };
    const groups = rec.api_groups ?? rec.apiGroups ?? [];
    if (groups.length > 0) {
      crd.push({
        apiGroup: groups[0] ?? '',
        resource: rec.resource || rec.resources?.[0] || '',
        verbs: rec.verbs ?? [],
      });
    } else if (rec.resource || rec.resources?.[0]) {
      platform.push({ resource: rec.resource || rec.resources?.[0] || 'workloads', verbs: rec.verbs ?? [] });
    }
  }
  return {
    name: initial?.name || '',
    displayName: initial?.displayName || '',
    description: initial?.description || '',
    scope: initial?.scope || defaultScope,
    platform: platform.length ? platform : [{ resource: 'workloads', verbs: ['read', 'list'] }],
    crd,
  };
}
