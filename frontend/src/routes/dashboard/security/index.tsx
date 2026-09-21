import { createFileRoute } from "@tanstack/react-router";
import { pageRowCount } from "@/lib/api/pagination";
import { useMemo, useState } from "react";
import { useAppForm, useStore } from "@/lib/form";
import { useTabParam } from "@/lib/use-tab-param";
import {
  usePodSecurityTemplates,
  useCreatePodSecurityTemplate,
  useUpdatePodSecurityTemplate,
  useDeletePodSecurityTemplate,
  useClusterSecurityPolicies,
  useAssignSecurityPolicy,
  useApplySecurityPolicy,
  useRemoveSecurityPolicy,
} from "@/lib/hooks/security";
import { useClusters } from "@/lib/hooks/clusters";
import { useCISScans } from "@/components/security/hooks";
import { DataTable, type Column } from "@/components/ui/data-table";
import { StatusBadge } from "@/components/ui/status-badge";
import { ActionButton } from "@/components/ui/action-button";
import { Input } from "@/components/ui/input";
import { Select } from "@/components/ui/select";
import { ModalShell } from "@/components/ui/modal-shell";
import { ConfirmDialog } from "@/components/ui/confirm-dialog";
import { PageHeader, PageShell } from "@/components/ui/page";
import { TabStrip } from "@/components/ui/tabs";
import { CISScansTab } from "@/components/security/cis-scans-tab";
import { formatRelativeTime, cn } from "@/lib/utils";
import type {
  PodSecurityTemplate,
  PodSecurityLevel,
  ClusterSecurityPolicy,
} from "@/types";
import {
  Shield,
  Plus,
  Trash2,
  Pencil,
  Play,
  ScanSearch,
  ShieldCheck,
  Info,
  Lock,
} from "lucide-react";

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
type TabKey = "cis" | "templates" | "policies";

type SecurityPolicyRow = ClusterSecurityPolicy & {
  clusterName: string;
  templateName: string;
  enforceLevel: PodSecurityLevel;
  auditLevel: PodSecurityLevel;
  warnLevel: PodSecurityLevel;
};

const TAB_KEYS = ["cis", "templates", "policies"] as const;

const tabs: { key: TabKey; label: string; icon: React.ElementType }[] = [
  { key: "cis", label: "CIS Scans", icon: ScanSearch },
  { key: "templates", label: "PSA Templates", icon: Shield },
  { key: "policies", label: "Security Policies", icon: ShieldCheck },
];

const psaLevels: PodSecurityLevel[] = ["privileged", "baseline", "restricted"];

const psaLevelColors: Record<PodSecurityLevel, string> = {
  privileged: "bg-status-error/10 text-status-error",
  baseline: "bg-status-warning/10 text-status-warning",
  restricted: "bg-status-success/10 text-status-success",
};

// On-page reference copy for the three Pod Security Standards and the three
// admission modes. Kept here next to psaLevelColors so the explainer and the
// table badges stay in lockstep.
const psaLevelDefs: { level: PodSecurityLevel; summary: string }[] = [
  {
    level: "privileged",
    summary:
      "Unrestricted — no policy applied. For trusted/system namespaces or to opt out of PSA.",
  },
  {
    level: "baseline",
    summary:
      "Minimally restrictive — blocks known privilege escalations while staying broadly compatible.",
  },
  {
    level: "restricted",
    summary:
      "Heavily restricted — follows current pod-hardening best practices. Recommended for production.",
  },
];

const psaModeDefs: { mode: string; summary: string }[] = [
  {
    mode: "enforce",
    summary: "Rejects pods that violate the standard at admission time.",
  },
  {
    mode: "audit",
    summary: "Allows the pod but records a violation in the audit log.",
  },
  {
    mode: "warn",
    summary: "Allows the pod but returns a user-facing warning to the client.",
  },
];

/**
 * PSAExplainer renders the on-page definition of Pod Security Admission: a
 * short intro, the three Pod Security Standards (levels), and the three
 * admission modes. Styled as a callout card consistent with the rest of the
 * dashboard.
 */
function PSAExplainer() {
  return (
    <div className="rounded-lg border border-border bg-muted/30 p-4 space-y-4">
      <div className="flex items-start gap-2">
        <Info className="h-4 w-4 text-primary mt-0.5 shrink-0" />
        <div className="space-y-1">
          <p className="text-sm font-medium text-foreground">
            What is Pod Security Admission (PSA)?
          </p>
          <p className="text-xs text-muted-foreground leading-relaxed">
            PSA is the built-in Kubernetes admission controller (the successor
            to PodSecurityPolicy) that enforces the three Pod Security Standards
            on a per-namespace basis. A template defines which standard applies
            in each of three modes; it only takes effect once you assign and
            apply it to a cluster from the Security Policies tab.
          </p>
        </div>
      </div>

      <div className="grid gap-4 sm:grid-cols-2">
        <div className="space-y-2">
          <p className="text-2xs font-semibold uppercase tracking-wide text-muted-foreground">
            Standards (levels)
          </p>
          <ul className="space-y-1.5">
            {psaLevelDefs.map((d) => (
              <li key={d.level} className="flex items-start gap-2">
                <span
                  className={cn(
                    "text-2xs px-1.5 py-0.5 rounded-sm font-medium capitalize shrink-0",
                    psaLevelColors[d.level],
                  )}
                >
                  {d.level}
                </span>
                <span className="text-xs text-muted-foreground leading-relaxed">
                  {d.summary}
                </span>
              </li>
            ))}
          </ul>
        </div>

        <div className="space-y-2">
          <p className="text-2xs font-semibold uppercase tracking-wide text-muted-foreground">
            Modes
          </p>
          <ul className="space-y-1.5">
            {psaModeDefs.map((d) => (
              <li key={d.mode} className="flex items-start gap-2">
                <span className="text-2xs px-1.5 py-0.5 rounded-sm font-medium capitalize shrink-0 bg-accent text-foreground">
                  {d.mode}
                </span>
                <span className="text-xs text-muted-foreground leading-relaxed">
                  {d.summary}
                </span>
              </li>
            ))}
          </ul>
        </div>
      </div>
    </div>
  );
}

function SecurityPage() {
  // Default-tab heuristic: the spec says CIS should default-select when
  // scans exist. We need the count *before* committing, so kick off a
  // tiny page-1 query and use it to pick the fallback tab. Default to `cis`
  // while loading (the tab is always enabled), fall back to `templates`
  // once we know there are no scans. An explicit `?tab=` in the URL always
  // wins over this heuristic.
  const { data: scansPage } = useCISScans({ pageSize: 1 });
  const defaultTab: TabKey =
    scansPage && pageRowCount(scansPage) === 0 ? "templates" : "cis";
  const [activeTab, setActiveTab] = useTabParam(TAB_KEYS, defaultTab);

  const [showAssignModal, setShowAssignModal] = useState(false);
  const [showTemplateModal, setShowTemplateModal] = useState(false);
  const [editingTemplate, setEditingTemplate] =
    useState<PodSecurityTemplate | null>(null);
  const [removePolicyTarget, setRemovePolicyTarget] =
    useState<SecurityPolicyRow | null>(null);
  const [deleteTemplateTarget, setDeleteTemplateTarget] =
    useState<PodSecurityTemplate | null>(null);

  const { data: policies, isLoading: policiesLoading } =
    useClusterSecurityPolicies();
  const { data: templates, isLoading: templatesLoading } =
    usePodSecurityTemplates();
  const { data: clustersData } = useClusters({ pageSize: 200 });

  const policyRows = useMemo<SecurityPolicyRow[]>(() => {
    const clusters = new Map(
      (clustersData?.data ?? []).map((cluster) => [
        cluster.id,
        cluster.displayName || cluster.name,
      ]),
    );
    const templateById = new Map(
      (templates ?? []).map((template) => [template.id, template]),
    );
    return (policies ?? []).map((policy) => {
      const template = templateById.get(policy.templateId);
      return {
        ...policy,
        clusterName:
          clusters.get(policy.clusterId) || `Unknown (${policy.clusterId})`,
        templateName: template?.name || `Unknown (${policy.templateId})`,
        enforceLevel: template?.enforceLevel || "privileged",
        auditLevel: template?.auditLevel || "privileged",
        warnLevel: template?.warnLevel || "privileged",
      };
    });
  }, [clustersData?.data, policies, templates]);

  const applyPolicy = useApplySecurityPolicy();
  const removePolicy = useRemoveSecurityPolicy();
  const deleteTemplate = useDeletePodSecurityTemplate();

  // --- Security Policies Table ---
  const policyColumns: Column<SecurityPolicyRow>[] = [
    {
      key: "cluster",
      header: "Cluster",
      accessor: (row) => (
        <span className="font-medium text-foreground text-sm">
          {row.clusterName}
        </span>
      ),
    },
    {
      key: "template",
      header: "Template",
      accessor: (row) => (
        <span className="text-sm text-muted-foreground">
          {row.templateName}
        </span>
      ),
    },
    {
      key: "enforce",
      header: "Enforce",
      accessor: (row) => (
        <span
          className={cn(
            "text-xs px-2 py-0.5 rounded-sm font-medium capitalize",
            psaLevelColors[row.enforceLevel],
          )}
        >
          {row.enforceLevel}
        </span>
      ),
    },
    {
      key: "audit",
      header: "Audit",
      accessor: (row) => (
        <span
          className={cn(
            "text-xs px-2 py-0.5 rounded-sm font-medium capitalize",
            psaLevelColors[row.auditLevel],
          )}
        >
          {row.auditLevel}
        </span>
      ),
    },
    {
      key: "warn",
      header: "Warn",
      accessor: (row) => (
        <span
          className={cn(
            "text-xs px-2 py-0.5 rounded-sm font-medium capitalize",
            psaLevelColors[row.warnLevel],
          )}
        >
          {row.warnLevel}
        </span>
      ),
    },
    {
      key: "syncStatus",
      header: "Sync Status",
      accessor: (row) => <StatusBadge status={row.syncStatus} />,
    },
    {
      key: "appliedAt",
      header: "Applied",
      accessor: (row) => (
        <span className="text-xs text-muted-foreground">
          {row.appliedAt ? formatRelativeTime(row.appliedAt) : "Not applied"}
        </span>
      ),
    },
    {
      key: "actions",
      header: "",
      accessor: (row) => (
        <div className="flex items-center gap-1">
          <button
            onClick={() => applyPolicy.mutate(row.id)}
            disabled={applyPolicy.isPending}
            className="inline-flex items-center gap-1 px-2 py-1 rounded-sm text-xs text-muted-foreground
              hover:text-foreground hover:bg-accent transition-colors disabled:opacity-50"
            title="Apply to cluster"
          >
            <Play className="h-3 w-3" />
            Apply
          </button>
          <button
            onClick={() => setRemovePolicyTarget(row)}
            className="p-1.5 rounded-sm text-muted-foreground hover:text-status-error hover:bg-status-error/10 transition-colors"
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
      key: "name",
      header: "Name",
      accessor: (row) => (
        <div className="flex items-center gap-2">
          <Shield className="h-4 w-4 text-muted-foreground" />
          <span className="font-medium text-foreground">{row.name}</span>
          {row.isDefault && (
            <span className="text-2xs px-1.5 py-0.5 rounded-sm bg-primary/10 text-primary font-medium">
              Default
            </span>
          )}
          {row.isBuiltin && (
            <span className="inline-flex items-center gap-1 text-2xs px-1.5 py-0.5 rounded-sm bg-accent text-muted-foreground font-medium">
              <Lock className="h-2.5 w-2.5" />
              Built-in
            </span>
          )}
        </div>
      ),
    },
    {
      key: "enforce",
      header: "Enforce",
      accessor: (row) => (
        <span
          className={cn(
            "text-xs px-2 py-0.5 rounded-sm font-medium capitalize",
            psaLevelColors[row.enforceLevel],
          )}
        >
          {row.enforceLevel}
        </span>
      ),
    },
    {
      key: "audit",
      header: "Audit",
      accessor: (row) => (
        <span
          className={cn(
            "text-xs px-2 py-0.5 rounded-sm font-medium capitalize",
            psaLevelColors[row.auditLevel],
          )}
        >
          {row.auditLevel}
        </span>
      ),
    },
    {
      key: "warn",
      header: "Warn",
      accessor: (row) => (
        <span
          className={cn(
            "text-xs px-2 py-0.5 rounded-sm font-medium capitalize",
            psaLevelColors[row.warnLevel],
          )}
        >
          {row.warnLevel}
        </span>
      ),
    },
    {
      key: "description",
      header: "Description",
      accessor: (row) => (
        <span className="text-xs text-muted-foreground truncate max-w-[200px] block">
          {row.description || "--"}
        </span>
      ),
      sortable: false,
    },
    {
      key: "actions",
      header: "",
      accessor: (row) => (
        <div className="flex items-center gap-1">
          <button
            onClick={() => {
              setEditingTemplate(row);
              setShowTemplateModal(true);
            }}
            disabled={row.isBuiltin}
            className="p-1.5 rounded-sm text-muted-foreground hover:text-foreground hover:bg-accent
              transition-colors disabled:opacity-30 disabled:pointer-events-none"
            title={
              row.isBuiltin
                ? "Built-in templates cannot be edited"
                : "Edit template"
            }
          >
            <Pencil className="h-3.5 w-3.5" />
          </button>
          <button
            onClick={() => setDeleteTemplateTarget(row)}
            disabled={row.isDefault || row.isBuiltin}
            className="p-1.5 rounded-sm text-muted-foreground hover:text-status-error hover:bg-status-error/10
              transition-colors disabled:opacity-30 disabled:pointer-events-none"
            title={
              row.isBuiltin
                ? "Built-in templates cannot be deleted"
                : "Delete template"
            }
          >
            <Trash2 className="h-3.5 w-3.5" />
          </button>
        </div>
      ),
      sortable: false,
    },
  ];

  return (
    <PageShell>
      <PageHeader
        title="Security"
        description="CIS benchmarks, Pod Security Admission policies, and compliance."
        actions={
          <>
            {activeTab === "policies" && (
              <ActionButton
                intent="primary"
                icon={<Plus className="h-4 w-4" />}
                onClick={() => setShowAssignModal(true)}
              >
                Assign Template
              </ActionButton>
            )}
            {activeTab === "templates" && (
              <ActionButton
                intent="primary"
                icon={<Plus className="h-4 w-4" />}
                onClick={() => {
                  setEditingTemplate(null);
                  setShowTemplateModal(true);
                }}
              >
                Create Template
              </ActionButton>
            )}
          </>
        }
      />

      {/* Tabs */}
      <TabStrip tabs={tabs} value={activeTab} onChange={setActiveTab} />

      {/* Content */}
      <div className="animate-fade-in">
        {activeTab === "cis" && <CISScansTab />}

        {activeTab === "policies" && (
          <div className="space-y-4">
            <div className="rounded-lg border border-border bg-muted/30 p-4 flex items-start gap-2">
              <Info className="h-4 w-4 text-primary mt-0.5 shrink-0" />
              <p className="text-xs text-muted-foreground leading-relaxed">
                A security policy binds a PSA template to a cluster. Until you
                assign and apply a template here, Pod Security Admission is not
                enforced — defining or seeding a template alone changes nothing
                on your clusters.
              </p>
            </div>
            <DataTable
              data={policyRows}
              columns={policyColumns}
              keyExtractor={(row) => row.id}
              searchPlaceholder="Search cluster policies..."
              loading={policiesLoading}
              emptyState={{
                title: "No security policies assigned",
                description:
                  "Resources will appear here when they are available in this scope.",
              }}
            />
          </div>
        )}

        {activeTab === "templates" && (
          <div className="space-y-4">
            <PSAExplainer />
            <DataTable
              data={templates || []}
              columns={templateColumns}
              keyExtractor={(row) => row.id}
              searchPlaceholder="Search templates..."
              loading={templatesLoading}
              emptyState={{
                title: "No PSA templates defined",
                description: "Create the first item to configure this feature.",
              }}
            />
          </div>
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
      <ConfirmDialog
        open={removePolicyTarget !== null}
        onClose={() => setRemovePolicyTarget(null)}
        onConfirm={() => {
          if (!removePolicyTarget) return;
          removePolicy.mutate(removePolicyTarget.id, {
            onSuccess: () => setRemovePolicyTarget(null),
          });
        }}
        title="Remove security policy"
        description="This unassigns the Pod Security Admission policy from the cluster."
        confirmText="Remove policy"
        variant="destructive"
        loading={removePolicy.isPending}
        impact={
          removePolicyTarget
            ? {
                scope: `${removePolicyTarget.templateName} on ${removePolicyTarget.clusterName}`,
                consequences: [
                  "The platform will stop managing this PSA assignment.",
                  "Namespaces may no longer receive the selected enforce, audit, and warn levels.",
                ],
                recovery: "Assign and apply the template to the cluster again.",
              }
            : undefined
        }
      />
      <ConfirmDialog
        open={deleteTemplateTarget !== null}
        onClose={() => setDeleteTemplateTarget(null)}
        onConfirm={() => {
          if (!deleteTemplateTarget) return;
          deleteTemplate.mutate(deleteTemplateTarget.id, {
            onSuccess: () => setDeleteTemplateTarget(null),
          });
        }}
        title="Delete PSA template"
        description="This permanently removes the custom Pod Security Admission template."
        confirmValue={deleteTemplateTarget?.name}
        variant="destructive"
        loading={deleteTemplate.isPending}
        impact={
          deleteTemplateTarget
            ? {
                scope: deleteTemplateTarget.name,
                consequences: [
                  "The template can no longer be assigned to clusters.",
                  "Existing policy assignments may need to be replaced.",
                ],
                recovery: "Recreate the template and its assignments manually.",
              }
            : undefined
        }
      />
    </PageShell>
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

  const form = useAppForm({
    defaultValues: {
      clusterId: "",
      templateId:
        templates.find((t) => t.isDefault)?.id || templates[0]?.id || "",
    },
    validators: {
      onSubmit: ({ value }) =>
        !value.clusterId || !value.templateId
          ? "Select a cluster and security template."
          : undefined,
    },
    onSubmit: async ({ value }) => {
      try {
        await assignPolicy.mutateAsync({
          cluster_id: value.clusterId,
          template_id: value.templateId,
        });
        onClose();
      } catch {
        // Persistent failure is rendered in the form summary.
      }
    },
  });
  const values = useStore(form.store, (state) => state.values);
  const selectedTemplate = templates.find(
    (template) => template.id === values.templateId,
  );

  return (
    <ModalShell
      title="Assign Security Template"
      onClose={onClose}
      size="md"
      footerClassName="flex items-center justify-end gap-2"
      footer={
        <>
          <ActionButton onClick={onClose}>Cancel</ActionButton>
          <ActionButton
            intent="primary"
            onClick={() => void form.handleSubmit()}
            disabled={
              assignPolicy.isPending || !values.clusterId || !values.templateId
            }
            loading={assignPolicy.isPending}
          >
            Assign Template
          </ActionButton>
        </>
      }
    >
      <form.AppForm>
        <form.FormErrorSummary serverError={assignPolicy.error?.message} />
      </form.AppForm>
      <form.AppField name="clusterId">
        {(field) => (
          <field.SelectField label="Cluster">
            <option value="">Select a cluster…</option>
            {clusters.map((cluster) => (
              <option key={cluster.id} value={cluster.id}>
                {cluster.displayName} ({cluster.name})
              </option>
            ))}
          </field.SelectField>
        )}
      </form.AppField>
      <form.AppField name="templateId">
        {(field) => (
          <field.SelectField label="Template">
            {templates.map((template) => (
              <option key={template.id} value={template.id}>
                {template.name}
                {template.isDefault ? " (Default)" : ""}
              </option>
            ))}
          </field.SelectField>
        )}
      </form.AppField>

      {selectedTemplate && (
        <div className="rounded-lg border border-border bg-muted/30 p-4 space-y-2">
          <p className="text-xs font-medium text-muted-foreground">
            Template Preview
          </p>
          <div className="grid grid-cols-3 gap-3">
            <div>
              <p className="text-2xs text-muted-foreground">Enforce</p>
              <span
                className={cn(
                  "text-xs px-2 py-0.5 rounded-sm font-medium capitalize",
                  psaLevelColors[selectedTemplate.enforceLevel],
                )}
              >
                {selectedTemplate.enforceLevel}
              </span>
            </div>
            <div>
              <p className="text-2xs text-muted-foreground">Audit</p>
              <span
                className={cn(
                  "text-xs px-2 py-0.5 rounded-sm font-medium capitalize",
                  psaLevelColors[selectedTemplate.auditLevel],
                )}
              >
                {selectedTemplate.auditLevel}
              </span>
            </div>
            <div>
              <p className="text-2xs text-muted-foreground">Warn</p>
              <span
                className={cn(
                  "text-xs px-2 py-0.5 rounded-sm font-medium capitalize",
                  psaLevelColors[selectedTemplate.warnLevel],
                )}
              >
                {selectedTemplate.warnLevel}
              </span>
            </div>
          </div>
          {selectedTemplate.description && (
            <p className="text-xs text-muted-foreground">
              {selectedTemplate.description}
            </p>
          )}
        </div>
      )}
    </ModalShell>
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

  const parseCSV = (val: string): string[] =>
    val
      .split(",")
      .map((s) => s.trim())
      .filter(Boolean);

  const form = useAppForm({
    defaultValues: {
      name: template?.name || "",
      description: template?.description || "",
      enforceLevel: template?.enforceLevel || ("baseline" as PodSecurityLevel),
      enforceVersion: template?.enforceVersion || "latest",
      auditLevel: template?.auditLevel || ("restricted" as PodSecurityLevel),
      auditVersion: template?.auditVersion || "latest",
      warnLevel: template?.warnLevel || ("restricted" as PodSecurityLevel),
      warnVersion: template?.warnVersion || "latest",
      exemptNamespaces: template?.exemptNamespaces?.join(", ") || "",
      exemptRuntimeClasses: template?.exemptRuntimeClasses?.join(", ") || "",
      exemptUsernames: template?.exemptUsernames?.join(", ") || "",
    },
    onSubmit: async ({ value }) => {
      const data = {
        name: value.name,
        description: value.description || undefined,
        enforceLevel: value.enforceLevel,
        enforceVersion: value.enforceVersion || undefined,
        auditLevel: value.auditLevel,
        auditVersion: value.auditVersion || undefined,
        warnLevel: value.warnLevel,
        warnVersion: value.warnVersion || undefined,
        exemptNamespaces: parseCSV(value.exemptNamespaces),
        exemptRuntimeClasses: parseCSV(value.exemptRuntimeClasses),
        exemptUsernames: parseCSV(value.exemptUsernames),
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
    },
  });

  const templateName = useStore(form.store, (s) => s.values.name);

  const isPending = createTemplate.isPending || updateTemplate.isPending;

  return (
    <ModalShell
      title={template ? "Edit PSA Template" : "Create PSA Template"}
      onClose={onClose}
      size="md"
      footerClassName="flex items-center justify-end gap-2"
      footer={
        <>
          <ActionButton onClick={onClose}>Cancel</ActionButton>
          <ActionButton
            intent="primary"
            onClick={() => void form.handleSubmit()}
            disabled={isPending || !templateName}
            loading={isPending}
          >
            {template ? "Update Template" : "Create Template"}
          </ActionButton>
        </>
      }
    >
      <form.AppForm>
        <form.FormErrorSummary
          serverError={
            createTemplate.error?.message ?? updateTemplate.error?.message
          }
        />
      </form.AppForm>
      <div className="space-y-1.5">
        <label
          className="text-sm font-medium text-foreground"
          htmlFor="field-7db9bce2-682"
        >
          Name
        </label>
        <form.Field name="name">
          {(field) => (
            <Input
              name={field.name}
              id="field-7db9bce2-682"
              type="text"
              value={field.state.value}
              onChange={(e) => field.handleChange(e.target.value)}
              onBlur={field.handleBlur}
              placeholder="restricted-production"
            />
          )}
        </form.Field>
      </div>

      <div className="space-y-1.5">
        <label
          className="text-sm font-medium text-foreground"
          htmlFor="field-7db9bce2-698"
        >
          Description
        </label>
        <form.Field name="description">
          {(field) => (
            <Input
              name={field.name}
              id="field-7db9bce2-698"
              type="text"
              value={field.state.value}
              onChange={(e) => field.handleChange(e.target.value)}
              onBlur={field.handleBlur}
              placeholder="Restricted policy for production clusters"
            />
          )}
        </form.Field>
      </div>

      <div className="grid grid-cols-3 gap-4">
        <div className="space-y-1.5">
          <label
            className="text-sm font-medium text-foreground"
            htmlFor="field-7db9bce2-715"
          >
            Enforce
          </label>
          <form.Field name="enforceLevel">
            {(field) => (
              <Select
                name={field.name}
                id="field-7db9bce2-715"
                value={field.state.value}
                onChange={(e) =>
                  field.handleChange(e.target.value as PodSecurityLevel)
                }
                onBlur={field.handleBlur}
                className="capitalize"
              >
                {psaLevels.map((level) => (
                  <option key={level} value={level}>
                    {level}
                  </option>
                ))}
              </Select>
            )}
          </form.Field>
        </div>
        <div className="space-y-1.5">
          <label
            className="text-sm font-medium text-foreground"
            htmlFor="field-7db9bce2-734"
          >
            Audit
          </label>
          <form.Field name="auditLevel">
            {(field) => (
              <Select
                name={field.name}
                id="field-7db9bce2-734"
                value={field.state.value}
                onChange={(e) =>
                  field.handleChange(e.target.value as PodSecurityLevel)
                }
                onBlur={field.handleBlur}
                className="capitalize"
              >
                {psaLevels.map((level) => (
                  <option key={level} value={level}>
                    {level}
                  </option>
                ))}
              </Select>
            )}
          </form.Field>
        </div>
        <div className="space-y-1.5">
          <label
            className="text-sm font-medium text-foreground"
            htmlFor="field-7db9bce2-753"
          >
            Warn
          </label>
          <form.Field name="warnLevel">
            {(field) => (
              <Select
                name={field.name}
                id="field-7db9bce2-753"
                value={field.state.value}
                onChange={(e) =>
                  field.handleChange(e.target.value as PodSecurityLevel)
                }
                onBlur={field.handleBlur}
                className="capitalize"
              >
                {psaLevels.map((level) => (
                  <option key={level} value={level}>
                    {level}
                  </option>
                ))}
              </Select>
            )}
          </form.Field>
        </div>
      </div>

      <div className="grid grid-cols-3 gap-4">
        <div className="space-y-1.5">
          <label
            className="text-xs text-muted-foreground"
            htmlFor="field-7db9bce2-775"
          >
            Enforce Version
          </label>
          <form.Field name="enforceVersion">
            {(field) => (
              <Input
                name={field.name}
                id="field-7db9bce2-775"
                type="text"
                value={field.state.value}
                onChange={(e) => field.handleChange(e.target.value)}
                onBlur={field.handleBlur}
                placeholder="latest"
                className="w-full h-8 px-2.5 rounded-sm border border-border bg-background text-xs
                      placeholder:text-muted-foreground focus:outline-hidden focus:ring-1 focus:ring-ring"
              />
            )}
          </form.Field>
        </div>
        <div className="space-y-1.5">
          <label
            className="text-xs text-muted-foreground"
            htmlFor="field-7db9bce2-791"
          >
            Audit Version
          </label>
          <form.Field name="auditVersion">
            {(field) => (
              <Input
                name={field.name}
                id="field-7db9bce2-791"
                type="text"
                value={field.state.value}
                onChange={(e) => field.handleChange(e.target.value)}
                onBlur={field.handleBlur}
                placeholder="latest"
                className="h-8 text-xs"
              />
            )}
          </form.Field>
        </div>
        <div className="space-y-1.5">
          <label
            className="text-xs text-muted-foreground"
            htmlFor="field-7db9bce2-806"
          >
            Warn Version
          </label>
          <form.Field name="warnVersion">
            {(field) => (
              <Input
                name={field.name}
                id="field-7db9bce2-806"
                type="text"
                value={field.state.value}
                onChange={(e) => field.handleChange(e.target.value)}
                onBlur={field.handleBlur}
                placeholder="latest"
                className="h-8 text-xs"
              />
            )}
          </form.Field>
        </div>
      </div>

      <div className="space-y-3 pt-2">
        <p className="text-sm font-medium text-foreground">Exemptions</p>

        <div className="space-y-1.5">
          <label
            className="text-xs text-muted-foreground"
            htmlFor="field-7db9bce2-826"
          >
            Namespaces (comma-separated)
          </label>
          <form.Field name="exemptNamespaces">
            {(field) => (
              <Input
                name={field.name}
                id="field-7db9bce2-826"
                type="text"
                value={field.state.value}
                onChange={(e) => field.handleChange(e.target.value)}
                onBlur={field.handleBlur}
                placeholder="kube-system, kube-public, kube-node-lease"
              />
            )}
          </form.Field>
        </div>

        <div className="space-y-1.5">
          <label
            className="text-xs text-muted-foreground"
            htmlFor="field-7db9bce2-842"
          >
            Runtime Classes (comma-separated)
          </label>
          <form.Field name="exemptRuntimeClasses">
            {(field) => (
              <Input
                name={field.name}
                id="field-7db9bce2-842"
                type="text"
                value={field.state.value}
                onChange={(e) => field.handleChange(e.target.value)}
                onBlur={field.handleBlur}
                placeholder="gvisor, kata"
              />
            )}
          </form.Field>
        </div>

        <div className="space-y-1.5">
          <label
            className="text-xs text-muted-foreground"
            htmlFor="field-7db9bce2-858"
          >
            Usernames (comma-separated)
          </label>
          <form.Field name="exemptUsernames">
            {(field) => (
              <Input
                name={field.name}
                id="field-7db9bce2-858"
                type="text"
                value={field.state.value}
                onChange={(e) => field.handleChange(e.target.value)}
                onBlur={field.handleBlur}
                placeholder="system:serviceaccount:kube-system:default"
              />
            )}
          </form.Field>
        </div>
      </div>
    </ModalShell>
  );
}

export const Route = createFileRoute("/dashboard/security/")({
  // ?tab= deep-link (P2.4): typed passthrough — useTabParam's allowlist stays the real validator.
  validateSearch: (search: Record<string, unknown>) =>
    search as { tab?: string } & Record<string, unknown>,
  component: SecurityPage,
});
