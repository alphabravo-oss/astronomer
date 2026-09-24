import { pageRowCount, pageTableCount } from "@/lib/api/pagination";
import { useState } from "react";
import { useTabParam } from "@/lib/use-tab-param";
import {
  usePodSecurityTemplates,
  useDeletePodSecurityTemplate,
  useClusterSecurityPolicies,
  useApplySecurityPolicy,
  useRemoveSecurityPolicy,
} from "@/lib/hooks/security";
import { usePolicyRows } from "./-policy-rows";
import { useCISScans } from "@/components/security/hooks";
import { ActionButton } from "@/components/ui/action-button";
import { ConfirmDialog } from "@/components/ui/confirm-dialog";
import { PageHeader, PageShell } from "@/components/ui/page";
import { TabStrip } from "@/components/ui/tabs";
import { CISScansTab } from "@/components/security/cis-scans-tab";
import type { PodSecurityTemplate } from "@/types";
import { Shield, Plus, ScanSearch, ShieldCheck } from "lucide-react";
import { PoliciesTab, type SecurityPolicyRow } from "./-policies-tab";
import { TemplatesTab } from "./-templates-tab";
import { AssignTemplateModal } from "./-assign-template-modal";
import { PSATemplateModal } from "./-psa-template-modal";
import { QueryStates } from "@/components/ui/query-states";
import { usePermissionDecision } from "@/lib/permission-hooks";

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

const TAB_KEYS = ["cis", "templates", "policies"] as const;

const tabs: { key: TabKey; label: string; icon: React.ElementType }[] = [
  { key: "cis", label: "CIS Scans", icon: ScanSearch },
  { key: "templates", label: "PSA Templates", icon: Shield },
  { key: "policies", label: "Security Policies", icon: ShieldCheck },
];

export function SecurityPage() {
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

  const [policyPage, setPolicyPage] = useState(0);
  const [templatePage, setTemplatePage] = useState(0);
  const policiesQuery = useClusterSecurityPolicies({
    limit: 25,
    offset: policyPage * 25,
  });
  const templatesQuery = usePodSecurityTemplates({
    limit: 25,
    offset: templatePage * 25,
  });
  const policies = policiesQuery.isError
    ? []
    : (policiesQuery.data?.data ?? []);
  const templates = templatesQuery.isError
    ? undefined
    : templatesQuery.data?.data;
  const create = usePermissionDecision("security", "create");
  const policyRows = usePolicyRows(policies);

  const applyPolicy = useApplySecurityPolicy();
  const removePolicy = useRemoveSecurityPolicy();
  const deleteTemplate = useDeletePodSecurityTemplate();

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
                disabled={
                  !create.allowed || !templates || templatesQuery.isError
                }
                disabledReason={
                  !create.allowed ? "Requires security:create" : undefined
                }
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
                disabled={!create.allowed}
                disabledReason={
                  !create.allowed ? "Requires security:create" : undefined
                }
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
          <QueryStates
            query={policiesQuery}
            permission="security:read"
            errorTitle="Policies unavailable"
          >
            <PoliciesTab
              rows={policyRows}
              serverSide={{
                ...pageTableCount(policiesQuery.data),
                pagination: { pageIndex: policyPage, pageSize: 25 },
                onPaginationChange: (next) => setPolicyPage(next.pageIndex),
              }}
              loading={policiesQuery.isLoading}
              onApply={(row) => applyPolicy.mutate(row.id)}
              applyPending={applyPolicy.isPending}
              onRemove={setRemovePolicyTarget}
            />
          </QueryStates>
        )}

        {activeTab === "templates" && (
          <QueryStates
            query={templatesQuery}
            permission="security:read"
            errorTitle="Templates unavailable"
          >
            <TemplatesTab
              templates={templates || []}
              serverSide={{
                ...pageTableCount(templatesQuery.data),
                pagination: { pageIndex: templatePage, pageSize: 25 },
                onPaginationChange: (next) => setTemplatePage(next.pageIndex),
              }}
              loading={templatesQuery.isLoading}
              onEdit={(row) => {
                setEditingTemplate(row);
                setShowTemplateModal(true);
              }}
              onDelete={setDeleteTemplateTarget}
            />
          </QueryStates>
        )}
      </div>

      {showAssignModal && (
        <AssignTemplateModal onClose={() => setShowAssignModal(false)} />
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
