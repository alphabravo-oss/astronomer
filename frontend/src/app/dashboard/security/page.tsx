'use client';

import { useEffect, useMemo, useState } from 'react';
import {
  usePodSecurityTemplates,
  useCreatePodSecurityTemplate,
  useUpdatePodSecurityTemplate,
  useDeletePodSecurityTemplate,
  useClusterSecurityPolicies,
  useAssignSecurityPolicy,
  useApplySecurityPolicy,
  useRemoveSecurityPolicy,
  useClusters,
  useCISScans,
} from '@/lib/hooks';
import { DataTable, type Column } from '@/components/ui/data-table';
import { StatusBadge } from '@/components/ui/status-badge';
import { CISScansTab } from '@/components/security/cis-scans-tab';
import { formatRelativeTime, cn } from '@/lib/utils';
import type {
  PodSecurityTemplate,
  PodSecurityLevel,
  ClusterSecurityPolicy,
} from '@/types';
import {
  Shield,
  Plus,
  Trash2,
  X,
  Loader2,
  Pencil,
  Play,
  ScanSearch,
  ShieldCheck,
} from 'lucide-react';

/**
 * Phase B5 — Security overview.
 *
 * Three tabs: CIS Scans (new), Templates (PSA), Policies. The CIS tab is
 * the new primary surface; the existing templates / policies tabs are kept
 * intact since they're owned by Phase A2 and unrelated to cis-operator.
 *
 * The default-active tab depends on data presence: if there are any CIS
 * scans we land on that tab; otherwise we fall back to Policies (the
 * historical default). This keeps the experience stable for existing
 * customers while making the new feature the first thing scan-using
 * customers see.
 */
type TabKey = 'cis' | 'templates' | 'policies';

const tabs: { key: TabKey; label: string; icon: React.ElementType }[] = [
  { key: 'cis', label: 'CIS Scans', icon: ScanSearch },
  { key: 'templates', label: 'PSA Templates', icon: Shield },
  { key: 'policies', label: 'Security Policies', icon: ShieldCheck },
];

const psaLevels: PodSecurityLevel[] = ['privileged', 'baseline', 'restricted'];

const psaLevelColors: Record<PodSecurityLevel, string> = {
  privileged: 'bg-status-error/10 text-status-error',
  baseline: 'bg-status-warning/10 text-status-warning',
  restricted: 'bg-status-success/10 text-status-success',
};

export default function SecurityPage() {
  // Default-tab heuristic: the spec says CIS should default-select when
  // scans exist. We need the count *before* committing, so kick off a
  // tiny page-1 query and pick once it lands. Default to `cis` while
  // loading — the tab is always enabled regardless.
  const { data: scansPage } = useCISScans({ pageSize: 1 });
  const [activeTab, setActiveTab] = useState<TabKey>('cis');
  const [tabUserOverridden, setTabUserOverridden] = useState(false);

  useEffect(() => {
    if (tabUserOverridden) return;
    if (!scansPage) return;
    if ((scansPage.total ?? 0) === 0) {
      setActiveTab('templates');
    }
  }, [scansPage, tabUserOverridden]);

  const [showAssignModal, setShowAssignModal] = useState(false);
  const [showTemplateModal, setShowTemplateModal] = useState(false);
  const [editingTemplate, setEditingTemplate] = useState<PodSecurityTemplate | null>(null);

  const { data: policies, isLoading: policiesLoading } = useClusterSecurityPolicies();
  const { data: templates, isLoading: templatesLoading } = usePodSecurityTemplates();

  const applyPolicy = useApplySecurityPolicy();
  const removePolicy = useRemoveSecurityPolicy();
  const deleteTemplate = useDeletePodSecurityTemplate();

  // --- Security Policies Table ---
  const policyColumns: Column<ClusterSecurityPolicy>[] = [
    {
      key: 'cluster',
      header: 'Cluster',
      accessor: (row) => (
        <span className="font-medium text-foreground text-sm">{row.clusterName}</span>
      ),
    },
    {
      key: 'template',
      header: 'Template',
      accessor: (row) => (
        <span className="text-sm text-muted-foreground">{row.templateName}</span>
      ),
    },
    {
      key: 'enforce',
      header: 'Enforce',
      accessor: (row) => (
        <span className={cn('text-xs px-2 py-0.5 rounded font-medium capitalize', psaLevelColors[row.enforceLevel])}>
          {row.enforceLevel}
        </span>
      ),
    },
    {
      key: 'audit',
      header: 'Audit',
      accessor: (row) => (
        <span className={cn('text-xs px-2 py-0.5 rounded font-medium capitalize', psaLevelColors[row.auditLevel])}>
          {row.auditLevel}
        </span>
      ),
    },
    {
      key: 'warn',
      header: 'Warn',
      accessor: (row) => (
        <span className={cn('text-xs px-2 py-0.5 rounded font-medium capitalize', psaLevelColors[row.warnLevel])}>
          {row.warnLevel}
        </span>
      ),
    },
    {
      key: 'syncStatus',
      header: 'Sync Status',
      accessor: (row) => <StatusBadge status={row.syncStatus} />,
    },
    {
      key: 'appliedAt',
      header: 'Applied',
      accessor: (row) => (
        <span className="text-xs text-muted-foreground">
          {row.appliedAt ? formatRelativeTime(row.appliedAt) : 'Not applied'}
        </span>
      ),
    },
    {
      key: 'actions',
      header: '',
      accessor: (row) => (
        <div className="flex items-center gap-1" onClick={(e) => e.stopPropagation()}>
          <button
            onClick={() => applyPolicy.mutate(row.id)}
            disabled={applyPolicy.isPending}
            className="inline-flex items-center gap-1 px-2 py-1 rounded text-xs text-muted-foreground
              hover:text-foreground hover:bg-accent transition-colors disabled:opacity-50"
            title="Apply to cluster"
          >
            <Play className="h-3 w-3" />
            Apply
          </button>
          <button
            onClick={() => {
              if (confirm(`Remove security policy from "${row.clusterName}"?`)) {
                removePolicy.mutate(row.id);
              }
            }}
            className="p-1.5 rounded text-muted-foreground hover:text-status-error hover:bg-status-error/10 transition-colors"
            title="Remove policy"
          >
            <Trash2 className="h-3.5 w-3.5" />
          </button>
        </div>
      ),
      sortable: false,
    },
  ];

  // --- PSA Templates Table ---
  const templateColumns: Column<PodSecurityTemplate>[] = [
    {
      key: 'name',
      header: 'Name',
      accessor: (row) => (
        <div className="flex items-center gap-2">
          <Shield className="h-4 w-4 text-muted-foreground" />
          <span className="font-medium text-foreground">{row.name}</span>
          {row.isDefault && (
            <span className="text-2xs px-1.5 py-0.5 rounded bg-primary/10 text-primary font-medium">Default</span>
          )}
        </div>
      ),
    },
    {
      key: 'enforce',
      header: 'Enforce',
      accessor: (row) => (
        <span className={cn('text-xs px-2 py-0.5 rounded font-medium capitalize', psaLevelColors[row.enforceLevel])}>
          {row.enforceLevel}
        </span>
      ),
    },
    {
      key: 'audit',
      header: 'Audit',
      accessor: (row) => (
        <span className={cn('text-xs px-2 py-0.5 rounded font-medium capitalize', psaLevelColors[row.auditLevel])}>
          {row.auditLevel}
        </span>
      ),
    },
    {
      key: 'warn',
      header: 'Warn',
      accessor: (row) => (
        <span className={cn('text-xs px-2 py-0.5 rounded font-medium capitalize', psaLevelColors[row.warnLevel])}>
          {row.warnLevel}
        </span>
      ),
    },
    {
      key: 'description',
      header: 'Description',
      accessor: (row) => (
        <span className="text-xs text-muted-foreground truncate max-w-[200px] block">
          {row.description || '--'}
        </span>
      ),
      sortable: false,
    },
    {
      key: 'actions',
      header: '',
      accessor: (row) => (
        <div className="flex items-center gap-1" onClick={(e) => e.stopPropagation()}>
          <button
            onClick={() => {
              setEditingTemplate(row);
              setShowTemplateModal(true);
            }}
            className="p-1.5 rounded text-muted-foreground hover:text-foreground hover:bg-accent transition-colors"
            title="Edit template"
          >
            <Pencil className="h-3.5 w-3.5" />
          </button>
          <button
            onClick={() => {
              if (confirm(`Delete template "${row.name}"?`)) {
                deleteTemplate.mutate(row.id);
              }
            }}
            disabled={row.isDefault}
            className="p-1.5 rounded text-muted-foreground hover:text-status-error hover:bg-status-error/10
              transition-colors disabled:opacity-30 disabled:pointer-events-none"
            title="Delete template"
          >
            <Trash2 className="h-3.5 w-3.5" />
          </button>
        </div>
      ),
      sortable: false,
    },
  ];

  return (
    <div className="space-y-6">
      {/* Header */}
      <div className="flex items-center justify-between">
        <div>
          <h1 className="text-2xl font-semibold text-foreground tracking-tight">Security</h1>
          <p className="text-sm text-muted-foreground mt-1">
            CIS benchmarks, Pod Security Admission policies, and compliance.
          </p>
        </div>
        <div className="flex items-center gap-2">
          {activeTab === 'policies' && (
            <button
              onClick={() => setShowAssignModal(true)}
              className="inline-flex items-center gap-2 h-9 px-4 rounded-lg bg-primary text-primary-foreground
                text-sm font-medium hover:opacity-90 transition-opacity"
            >
              <Plus className="h-4 w-4" />
              Assign Template
            </button>
          )}
          {activeTab === 'templates' && (
            <button
              onClick={() => {
                setEditingTemplate(null);
                setShowTemplateModal(true);
              }}
              className="inline-flex items-center gap-2 h-9 px-4 rounded-lg bg-primary text-primary-foreground
                text-sm font-medium hover:opacity-90 transition-opacity"
            >
              <Plus className="h-4 w-4" />
              Create Template
            </button>
          )}
        </div>
      </div>

      {/* Tabs */}
      <div className="border-b border-border">
        <nav className="flex gap-6">
          {tabs.map((tab) => {
            const Icon = tab.icon;
            return (
              <button
                key={tab.key}
                onClick={() => {
                  setActiveTab(tab.key);
                  setTabUserOverridden(true);
                }}
                className={cn(
                  'flex items-center gap-2 pb-3 text-sm font-medium border-b-2 transition-colors',
                  activeTab === tab.key
                    ? 'border-foreground text-foreground'
                    : 'border-transparent text-muted-foreground hover:text-foreground',
                )}
              >
                <Icon className="h-4 w-4" />
                {tab.label}
              </button>
            );
          })}
        </nav>
      </div>

      {/* Content */}
      <div className="animate-fade-in">
        {activeTab === 'cis' && <CISScansTab />}

        {activeTab === 'policies' && (
          <DataTable
            data={policies || []}
            columns={policyColumns}
            keyExtractor={(row) => row.id}
            searchPlaceholder="Search cluster policies..."
            loading={policiesLoading}
            emptyMessage="No security policies assigned"
          />
        )}

        {activeTab === 'templates' && (
          <DataTable
            data={templates || []}
            columns={templateColumns}
            keyExtractor={(row) => row.id}
            searchPlaceholder="Search templates..."
            loading={templatesLoading}
            emptyMessage="No PSA templates defined"
          />
        )}
      </div>

      {showAssignModal && (
        <AssignTemplateModal
          templates={templates || []}
          onClose={() => setShowAssignModal(false)}
        />
      )}

      {showTemplateModal && (
        <PSATemplateModal
          template={editingTemplate}
          onClose={() => {
            setShowTemplateModal(false);
            setEditingTemplate(null);
          }}
        />
      )}
    </div>
  );
}

// ============================================================
// Assign Template Modal — unchanged from Phase A2
// ============================================================

function AssignTemplateModal({
  templates,
  onClose,
}: {
  templates: PodSecurityTemplate[];
  onClose: () => void;
}) {
  const assignPolicy = useAssignSecurityPolicy();
  const { data: clustersData } = useClusters({ pageSize: 100 });
  const clusters = clustersData?.data || [];

  const [form, setForm] = useState({
    clusterId: '',
    templateId: templates.find((t) => t.isDefault)?.id || templates[0]?.id || '',
  });

  const selectedTemplate = useMemo(
    () => templates.find((t) => t.id === form.templateId),
    [templates, form.templateId],
  );

  const handleSave = async () => {
    try {
      await assignPolicy.mutateAsync({
        cluster_id: form.clusterId,
        template_id: form.templateId,
      });
      onClose();
    } catch {
      // Error surfaced by mutation toast.
    }
  };

  return (
    <div className="fixed inset-0 z-50 flex items-center justify-center">
      <div className="fixed inset-0 bg-black/60 backdrop-blur-sm" onClick={onClose} />
      <div className="relative w-full max-w-lg rounded-xl border border-border bg-popover shadow-2xl flex flex-col">
        <div className="flex items-center justify-between px-6 py-4 border-b border-border flex-shrink-0">
          <h3 className="text-lg font-semibold text-foreground">Assign Security Template</h3>
          <button onClick={onClose} className="text-muted-foreground hover:text-foreground transition-colors">
            <X className="h-5 w-5" />
          </button>
        </div>

        <div className="p-6 space-y-4">
          <div className="space-y-1.5">
            <label className="text-sm font-medium text-foreground">Cluster</label>
            <select
              value={form.clusterId}
              onChange={(e) => setForm((f) => ({ ...f, clusterId: e.target.value }))}
              className="w-full h-9 px-3 rounded-md border border-border bg-background text-sm
                focus:outline-none focus:ring-1 focus:ring-ring"
            >
              <option value="">Select a cluster...</option>
              {clusters.map((c) => (
                <option key={c.id} value={c.id}>
                  {c.displayName} ({c.name})
                </option>
              ))}
            </select>
          </div>

          <div className="space-y-1.5">
            <label className="text-sm font-medium text-foreground">Template</label>
            <select
              value={form.templateId}
              onChange={(e) => setForm((f) => ({ ...f, templateId: e.target.value }))}
              className="w-full h-9 px-3 rounded-md border border-border bg-background text-sm
                focus:outline-none focus:ring-1 focus:ring-ring"
            >
              {templates.map((t) => (
                <option key={t.id} value={t.id}>
                  {t.name} {t.isDefault ? '(Default)' : ''}
                </option>
              ))}
            </select>
          </div>

          {selectedTemplate && (
            <div className="rounded-lg border border-border bg-muted/30 p-4 space-y-2">
              <p className="text-xs font-medium text-muted-foreground">Template Preview</p>
              <div className="grid grid-cols-3 gap-3">
                <div>
                  <p className="text-2xs text-muted-foreground">Enforce</p>
                  <span className={cn('text-xs px-2 py-0.5 rounded font-medium capitalize', psaLevelColors[selectedTemplate.enforceLevel])}>
                    {selectedTemplate.enforceLevel}
                  </span>
                </div>
                <div>
                  <p className="text-2xs text-muted-foreground">Audit</p>
                  <span className={cn('text-xs px-2 py-0.5 rounded font-medium capitalize', psaLevelColors[selectedTemplate.auditLevel])}>
                    {selectedTemplate.auditLevel}
                  </span>
                </div>
                <div>
                  <p className="text-2xs text-muted-foreground">Warn</p>
                  <span className={cn('text-xs px-2 py-0.5 rounded font-medium capitalize', psaLevelColors[selectedTemplate.warnLevel])}>
                    {selectedTemplate.warnLevel}
                  </span>
                </div>
              </div>
              {selectedTemplate.description && (
                <p className="text-xs text-muted-foreground">{selectedTemplate.description}</p>
              )}
            </div>
          )}
        </div>

        <div className="flex items-center justify-end gap-2 px-6 py-4 border-t border-border bg-muted/30">
          <button
            onClick={onClose}
            className="h-9 px-4 rounded-lg border border-border text-sm font-medium
              text-muted-foreground hover:text-foreground hover:bg-accent transition-colors"
          >
            Cancel
          </button>
          <button
            onClick={handleSave}
            disabled={assignPolicy.isPending || !form.clusterId || !form.templateId}
            className="inline-flex items-center gap-2 h-9 px-4 rounded-lg bg-primary text-primary-foreground
              text-sm font-medium hover:opacity-90 transition-opacity disabled:opacity-50"
          >
            {assignPolicy.isPending && <Loader2 className="h-3.5 w-3.5 animate-spin" />}
            Assign Template
          </button>
        </div>
      </div>
    </div>
  );
}

// ============================================================
// PSA Template Modal — unchanged from Phase A2
// ============================================================

function PSATemplateModal({
  template,
  onClose,
}: {
  template: PodSecurityTemplate | null;
  onClose: () => void;
}) {
  const createTemplate = useCreatePodSecurityTemplate();
  const updateTemplate = useUpdatePodSecurityTemplate();

  const [form, setForm] = useState({
    name: template?.name || '',
    description: template?.description || '',
    enforceLevel: template?.enforceLevel || ('baseline' as PodSecurityLevel),
    enforceVersion: template?.enforceVersion || 'latest',
    auditLevel: template?.auditLevel || ('restricted' as PodSecurityLevel),
    auditVersion: template?.auditVersion || 'latest',
    warnLevel: template?.warnLevel || ('restricted' as PodSecurityLevel),
    warnVersion: template?.warnVersion || 'latest',
    exemptNamespaces: template?.exemptNamespaces?.join(', ') || '',
    exemptRuntimeClasses: template?.exemptRuntimeClasses?.join(', ') || '',
    exemptUsernames: template?.exemptUsernames?.join(', ') || '',
  });

  const parseCSV = (val: string): string[] =>
    val
      .split(',')
      .map((s) => s.trim())
      .filter(Boolean);

  const handleSave = async () => {
    const data = {
      name: form.name,
      description: form.description || undefined,
      enforceLevel: form.enforceLevel,
      enforceVersion: form.enforceVersion || undefined,
      auditLevel: form.auditLevel,
      auditVersion: form.auditVersion || undefined,
      warnLevel: form.warnLevel,
      warnVersion: form.warnVersion || undefined,
      exemptNamespaces: parseCSV(form.exemptNamespaces),
      exemptRuntimeClasses: parseCSV(form.exemptRuntimeClasses),
      exemptUsernames: parseCSV(form.exemptUsernames),
    };

    try {
      if (template) {
        await updateTemplate.mutateAsync({ id: template.id, data });
      } else {
        await createTemplate.mutateAsync(data);
      }
      onClose();
    } catch {
      // Error surfaced by mutation toast.
    }
  };

  const isPending = createTemplate.isPending || updateTemplate.isPending;

  return (
    <div className="fixed inset-0 z-50 flex items-center justify-center">
      <div className="fixed inset-0 bg-black/60 backdrop-blur-sm" onClick={onClose} />
      <div className="relative w-full max-w-lg max-h-[85vh] rounded-xl border border-border bg-popover shadow-2xl flex flex-col">
        <div className="flex items-center justify-between px-6 py-4 border-b border-border flex-shrink-0">
          <h3 className="text-lg font-semibold text-foreground">
            {template ? 'Edit PSA Template' : 'Create PSA Template'}
          </h3>
          <button onClick={onClose} className="text-muted-foreground hover:text-foreground transition-colors">
            <X className="h-5 w-5" />
          </button>
        </div>

        <div className="flex-1 overflow-y-auto p-6 space-y-4">
          <div className="space-y-1.5">
            <label className="text-sm font-medium text-foreground">Name</label>
            <input
              type="text"
              value={form.name}
              onChange={(e) => setForm((f) => ({ ...f, name: e.target.value }))}
              placeholder="restricted-production"
              className="w-full h-9 px-3 rounded-md border border-border bg-background text-sm
                placeholder:text-muted-foreground focus:outline-none focus:ring-1 focus:ring-ring"
            />
          </div>

          <div className="space-y-1.5">
            <label className="text-sm font-medium text-foreground">Description</label>
            <input
              type="text"
              value={form.description}
              onChange={(e) => setForm((f) => ({ ...f, description: e.target.value }))}
              placeholder="Restricted policy for production clusters"
              className="w-full h-9 px-3 rounded-md border border-border bg-background text-sm
                placeholder:text-muted-foreground focus:outline-none focus:ring-1 focus:ring-ring"
            />
          </div>

          <div className="grid grid-cols-3 gap-4">
            <div className="space-y-1.5">
              <label className="text-sm font-medium text-foreground">Enforce</label>
              <select
                value={form.enforceLevel}
                onChange={(e) =>
                  setForm((f) => ({ ...f, enforceLevel: e.target.value as PodSecurityLevel }))
                }
                className="w-full h-9 px-3 rounded-md border border-border bg-background text-sm
                  focus:outline-none focus:ring-1 focus:ring-ring capitalize"
              >
                {psaLevels.map((level) => (
                  <option key={level} value={level}>
                    {level}
                  </option>
                ))}
              </select>
            </div>
            <div className="space-y-1.5">
              <label className="text-sm font-medium text-foreground">Audit</label>
              <select
                value={form.auditLevel}
                onChange={(e) =>
                  setForm((f) => ({ ...f, auditLevel: e.target.value as PodSecurityLevel }))
                }
                className="w-full h-9 px-3 rounded-md border border-border bg-background text-sm
                  focus:outline-none focus:ring-1 focus:ring-ring capitalize"
              >
                {psaLevels.map((level) => (
                  <option key={level} value={level}>
                    {level}
                  </option>
                ))}
              </select>
            </div>
            <div className="space-y-1.5">
              <label className="text-sm font-medium text-foreground">Warn</label>
              <select
                value={form.warnLevel}
                onChange={(e) =>
                  setForm((f) => ({ ...f, warnLevel: e.target.value as PodSecurityLevel }))
                }
                className="w-full h-9 px-3 rounded-md border border-border bg-background text-sm
                  focus:outline-none focus:ring-1 focus:ring-ring capitalize"
              >
                {psaLevels.map((level) => (
                  <option key={level} value={level}>
                    {level}
                  </option>
                ))}
              </select>
            </div>
          </div>

          <div className="grid grid-cols-3 gap-4">
            <div className="space-y-1.5">
              <label className="text-xs text-muted-foreground">Enforce Version</label>
              <input
                type="text"
                value={form.enforceVersion}
                onChange={(e) => setForm((f) => ({ ...f, enforceVersion: e.target.value }))}
                placeholder="latest"
                className="w-full h-8 px-2.5 rounded border border-border bg-background text-xs
                  placeholder:text-muted-foreground focus:outline-none focus:ring-1 focus:ring-ring"
              />
            </div>
            <div className="space-y-1.5">
              <label className="text-xs text-muted-foreground">Audit Version</label>
              <input
                type="text"
                value={form.auditVersion}
                onChange={(e) => setForm((f) => ({ ...f, auditVersion: e.target.value }))}
                placeholder="latest"
                className="w-full h-8 px-2.5 rounded border border-border bg-background text-xs
                  placeholder:text-muted-foreground focus:outline-none focus:ring-1 focus:ring-ring"
              />
            </div>
            <div className="space-y-1.5">
              <label className="text-xs text-muted-foreground">Warn Version</label>
              <input
                type="text"
                value={form.warnVersion}
                onChange={(e) => setForm((f) => ({ ...f, warnVersion: e.target.value }))}
                placeholder="latest"
                className="w-full h-8 px-2.5 rounded border border-border bg-background text-xs
                  placeholder:text-muted-foreground focus:outline-none focus:ring-1 focus:ring-ring"
              />
            </div>
          </div>

          <div className="space-y-3 pt-2">
            <p className="text-sm font-medium text-foreground">Exemptions</p>

            <div className="space-y-1.5">
              <label className="text-xs text-muted-foreground">Namespaces (comma-separated)</label>
              <input
                type="text"
                value={form.exemptNamespaces}
                onChange={(e) => setForm((f) => ({ ...f, exemptNamespaces: e.target.value }))}
                placeholder="kube-system, kube-public, kube-node-lease"
                className="w-full h-9 px-3 rounded-md border border-border bg-background text-sm
                  placeholder:text-muted-foreground focus:outline-none focus:ring-1 focus:ring-ring"
              />
            </div>

            <div className="space-y-1.5">
              <label className="text-xs text-muted-foreground">Runtime Classes (comma-separated)</label>
              <input
                type="text"
                value={form.exemptRuntimeClasses}
                onChange={(e) => setForm((f) => ({ ...f, exemptRuntimeClasses: e.target.value }))}
                placeholder="gvisor, kata"
                className="w-full h-9 px-3 rounded-md border border-border bg-background text-sm
                  placeholder:text-muted-foreground focus:outline-none focus:ring-1 focus:ring-ring"
              />
            </div>

            <div className="space-y-1.5">
              <label className="text-xs text-muted-foreground">Usernames (comma-separated)</label>
              <input
                type="text"
                value={form.exemptUsernames}
                onChange={(e) => setForm((f) => ({ ...f, exemptUsernames: e.target.value }))}
                placeholder="system:serviceaccount:kube-system:default"
                className="w-full h-9 px-3 rounded-md border border-border bg-background text-sm
                  placeholder:text-muted-foreground focus:outline-none focus:ring-1 focus:ring-ring"
              />
            </div>
          </div>
        </div>

        <div className="flex items-center justify-end gap-2 px-6 py-4 border-t border-border flex-shrink-0 bg-muted/30">
          <button
            onClick={onClose}
            className="h-9 px-4 rounded-lg border border-border text-sm font-medium
              text-muted-foreground hover:text-foreground hover:bg-accent transition-colors"
          >
            Cancel
          </button>
          <button
            onClick={handleSave}
            disabled={isPending || !form.name}
            className="inline-flex items-center gap-2 h-9 px-4 rounded-lg bg-primary text-primary-foreground
              text-sm font-medium hover:opacity-90 transition-opacity disabled:opacity-50"
          >
            {isPending && <Loader2 className="h-3.5 w-3.5 animate-spin" />}
            {template ? 'Update Template' : 'Create Template'}
          </button>
        </div>
      </div>
    </div>
  );
}
