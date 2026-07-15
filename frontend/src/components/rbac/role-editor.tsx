'use client';

import { useState } from 'react';
import { Plus, Trash2, Loader2 } from 'lucide-react';
import { ModalShell } from '@/components/ui/modal-shell';
import { cn } from '@/lib/utils';
import { toastError } from '@/lib/toast';
import { useCreateRole } from '@/lib/hooks';

interface RoleEditorProps {
  onClose: () => void;
  initialRole?: {
    name: string;
    displayName: string;
    description: string;
    scope: 'global' | 'cluster' | 'project';
    rules: PolicyRuleInput[];
  };
}

interface PolicyRuleInput {
  apiGroups: string;
  resources: string;
  verbs: string[];
}

const VERBS = ['get', 'list', 'watch', 'create', 'update', 'patch', 'delete'];

const emptyRule: PolicyRuleInput = {
  apiGroups: '',
  resources: '',
  verbs: [],
};

export function RoleEditor({ onClose, initialRole }: RoleEditorProps) {
  const createRole = useCreateRole();
  const [form, setForm] = useState({
    name: initialRole?.name || '',
    displayName: initialRole?.displayName || '',
    description: initialRole?.description || '',
    scope: initialRole?.scope || ('global' as 'global' | 'cluster' | 'project'),
    rules: initialRole?.rules || [{ ...emptyRule }],
  });

  const addRule = () => {
    setForm((f) => ({
      ...f,
      rules: [...f.rules, { ...emptyRule }],
    }));
  };

  const removeRule = (index: number) => {
    setForm((f) => ({
      ...f,
      rules: f.rules.filter((_, i) => i !== index),
    }));
  };

  const updateRule = (index: number, updates: Partial<PolicyRuleInput>) => {
    setForm((f) => ({
      ...f,
      rules: f.rules.map((rule, i) => (i === index ? { ...rule, ...updates } : rule)),
    }));
  };

  const toggleVerb = (index: number, verb: string) => {
    setForm((f) => ({
      ...f,
      rules: f.rules.map((rule, i) => {
        if (i !== index) return rule;
        const verbs = rule.verbs.includes(verb)
          ? rule.verbs.filter((v) => v !== verb)
          : [...rule.verbs, verb];
        return { ...rule, verbs };
      }),
    }));
  };

  const selectAllVerbs = (index: number) => {
    setForm((f) => ({
      ...f,
      rules: f.rules.map((rule, i) => {
        if (i !== index) return rule;
        const allSelected = VERBS.every((v) => rule.verbs.includes(v));
        return { ...rule, verbs: allSelected ? [] : [...VERBS] };
      }),
    }));
  };

  const handleSave = async () => {
    if (!form.name || !form.displayName) {
      toastError('Name and display name are required');
      return;
    }
    if (form.rules.some((r) => !r.resources || r.verbs.length === 0)) {
      toastError('Each rule must have resources and at least one verb');
      return;
    }

    const rules = form.rules.map((r) => ({
      apiGroups: r.apiGroups
        ? r.apiGroups.split(',').map((s) => s.trim())
        : [''],
      resources: r.resources.split(',').map((s) => s.trim()),
      verbs: r.verbs,
    }));

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
      panelClassName="max-w-2xl max-h-[85vh] bg-popover flex flex-col overflow-hidden"
      bodyClassName="flex-1 overflow-y-auto space-y-5"
      footerClassName="bg-muted/30"
      footer={(
        <div className="flex items-center justify-end gap-2">
          <button
            onClick={onClose}
            className="h-9 px-4 rounded-lg border border-border text-sm font-medium
              text-muted-foreground hover:text-foreground hover:bg-accent transition-colors"
          >
            Cancel
          </button>
          <button
            onClick={handleSave}
            disabled={createRole.isPending}
            className="inline-flex items-center gap-2 h-9 px-4 rounded-lg bg-primary text-primary-foreground
              text-sm font-medium hover:opacity-90 transition-opacity disabled:opacity-50"
          >
            {createRole.isPending && <Loader2 className="h-3.5 w-3.5 animate-spin" />}
            {initialRole ? 'Update Role' : 'Create Role'}
          </button>
        </div>
      )}
    >
          {/* Basic Info */}
          <div className="grid grid-cols-2 gap-4">
            <div className="space-y-1.5">
              <label className="text-sm font-medium text-foreground">Name</label>
              <input
                type="text"
                value={form.name}
                onChange={(e) => setForm((f) => ({ ...f, name: e.target.value.toLowerCase().replace(/[^a-z0-9-]/g, '-') }))}
                placeholder="role-name"
                className="w-full h-9 px-3 rounded-md border border-border bg-background text-sm
                  placeholder:text-muted-foreground focus:outline-none focus:ring-1 focus:ring-ring"
              />
            </div>
            <div className="space-y-1.5">
              <label className="text-sm font-medium text-foreground">Display Name</label>
              <input
                type="text"
                value={form.displayName}
                onChange={(e) => setForm((f) => ({ ...f, displayName: e.target.value }))}
                placeholder="My Custom Role"
                className="w-full h-9 px-3 rounded-md border border-border bg-background text-sm
                  placeholder:text-muted-foreground focus:outline-none focus:ring-1 focus:ring-ring"
              />
            </div>
          </div>

          <div className="space-y-1.5">
            <label className="text-sm font-medium text-foreground">Description</label>
            <input
              type="text"
              value={form.description}
              onChange={(e) => setForm((f) => ({ ...f, description: e.target.value }))}
              placeholder="Describe this role's purpose"
              className="w-full h-9 px-3 rounded-md border border-border bg-background text-sm
                placeholder:text-muted-foreground focus:outline-none focus:ring-1 focus:ring-ring"
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
                      : 'bg-muted text-muted-foreground hover:text-foreground'
                  )}
                >
                  {scope}
                </button>
              ))}
            </div>
          </div>

          {/* Rules */}
          <div className="space-y-3">
            <div className="flex items-center justify-between">
              <label className="text-sm font-medium text-foreground">Permission Rules</label>
              <button
                onClick={addRule}
                className="inline-flex items-center gap-1 text-xs text-muted-foreground hover:text-foreground transition-colors"
              >
                <Plus className="h-3 w-3" />
                Add Rule
              </button>
            </div>

            {form.rules.map((rule, idx) => (
              <div
                key={idx}
                className="rounded-lg border border-border p-4 space-y-3 bg-card"
              >
                <div className="flex items-center justify-between">
                  <span className="text-xs font-medium text-muted-foreground">Rule {idx + 1}</span>
                  {form.rules.length > 1 && (
                    <button
                      onClick={() => removeRule(idx)}
                      className="text-muted-foreground hover:text-status-error transition-colors"
                    >
                      <Trash2 className="h-3.5 w-3.5" />
                    </button>
                  )}
                </div>

                <div className="grid grid-cols-2 gap-3">
                  <div className="space-y-1.5">
                    <label className="text-xs text-muted-foreground">API Groups</label>
                    <input
                      type="text"
                      value={rule.apiGroups}
                      onChange={(e) => updateRule(idx, { apiGroups: e.target.value })}
                      placeholder='e.g., "", apps, batch'
                      className="w-full h-8 px-2.5 rounded border border-border bg-background text-xs
                        placeholder:text-muted-foreground focus:outline-none focus:ring-1 focus:ring-ring font-mono"
                    />
                  </div>
                  <div className="space-y-1.5">
                    <label className="text-xs text-muted-foreground">Resources</label>
                    <input
                      type="text"
                      value={rule.resources}
                      onChange={(e) => updateRule(idx, { resources: e.target.value })}
                      placeholder="e.g., pods, deployments"
                      className="w-full h-8 px-2.5 rounded border border-border bg-background text-xs
                        placeholder:text-muted-foreground focus:outline-none focus:ring-1 focus:ring-ring font-mono"
                    />
                  </div>
                </div>

                {/* Verbs matrix */}
                <div className="space-y-1.5">
                  <div className="flex items-center justify-between">
                    <label className="text-xs text-muted-foreground">Verbs</label>
                    <button
                      onClick={() => selectAllVerbs(idx)}
                      className="text-2xs text-muted-foreground hover:text-foreground transition-colors"
                    >
                      {VERBS.every((v) => rule.verbs.includes(v)) ? 'Deselect all' : 'Select all'}
                    </button>
                  </div>
                  <div className="flex flex-wrap gap-1.5">
                    {VERBS.map((verb) => (
                      <button
                        key={verb}
                        onClick={() => toggleVerb(idx, verb)}
                        className={cn(
                          'px-2.5 py-1 rounded text-xs font-medium transition-colors',
                          rule.verbs.includes(verb)
                            ? 'bg-primary text-primary-foreground'
                            : 'bg-muted text-muted-foreground hover:text-foreground'
                        )}
                      >
                        {verb}
                      </button>
                    ))}
                  </div>
                </div>
              </div>
            ))}
          </div>
    </ModalShell>
  );
}
