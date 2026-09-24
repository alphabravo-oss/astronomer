import { useState } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { DataTable, type Column } from "@/components/ui/data-table";
import { ActionButton } from "@/components/ui/action-button";
import { ConfirmDialog } from "@/components/ui/confirm-dialog";
import { QueryStates } from "@/components/ui/query-states";
import { PermissionState, StatePanel } from "@/components/ui/empty-state";
import { Shield } from "lucide-react";
import { NativeRuleForm } from "@/components/rbac/native-rule-form";
import {
  createNativeRule,
  deleteNativeRule,
  getNativeRulePage,
  type CreateNativeRuleRequest,
  type NativeRule,
} from "@/lib/api/native-rbac";
import { usePermissionDecision } from "@/lib/permission-hooks";
import { pageTableCount } from "@/lib/api/pagination";
import { queryKeys } from "@/lib/query-keys";
import { toastApiError, toastSuccess } from "@/lib/toast";

export default function NativeRulesTab() {
  const read = usePermissionDecision("rbac", "read");
  const create = usePermissionDecision("rbac", "create");
  const remove = usePermissionDecision("rbac", "delete");
  const [pageIndex, setPageIndex] = useState(0);
  const [showForm, setShowForm] = useState(false);
  const [review, setReview] = useState<CreateNativeRuleRequest | null>(null);
  const [target, setTarget] = useState<NativeRule | null>(null);
  const client = useQueryClient();
  const params = { limit: 25, offset: pageIndex * 25 };
  const query = useQuery({
    queryKey: queryKeys.nativeRbac.page(params),
    queryFn: ({ signal }) => getNativeRulePage(params, signal),
    enabled: read.allowed,
    throwOnError: false,
  });
  const refresh = () =>
    client.invalidateQueries({ queryKey: queryKeys.nativeRbac.all });
  const grant = useMutation({
    mutationFn: (value: CreateNativeRuleRequest) => createNativeRule(value),
    onSuccess: () => {
      void refresh();
      setReview(null);
      setShowForm(false);
      toastSuccess("Native resource grant created");
    },
    onError: (error) => toastApiError("Could not create grant", error),
  });
  const revoke = useMutation({
    mutationFn: (id: string) => deleteNativeRule(id),
    onSuccess: () => {
      void refresh();
      setTarget(null);
      toastSuccess("Native resource grant removed");
    },
    onError: (error) => toastApiError("Could not remove grant", error),
  });
  const columns: Column<NativeRule>[] = [
    { key: "user", header: "User UUID", accessor: (row) => row.userId },
    {
      key: "cluster",
      header: "Cluster scope",
      accessor: (row) => row.clusterId || "All clusters",
    },
    {
      key: "namespace",
      header: "Namespace scope",
      accessor: (row) => row.namespace || "All namespaces",
    },
    {
      key: "resource",
      header: "API group / resource",
      accessor: (row) => `${row.apiGroup || "core"} / ${row.resource}`,
    },
    { key: "verbs", header: "Verbs", accessor: (row) => row.verbs.join(", ") },
    {
      key: "actions",
      header: "Actions",
      sortable: false,
      accessor: (row) => (
        <ActionButton
          size="sm"
          intent="destructive"
          disabled={!remove.allowed}
          disabledReason="Requires rbac:delete"
          onClick={() => setTarget(row)}
        >
          Remove grant
        </ActionButton>
      ),
    },
  ];
  if (!read.allowed) return <PermissionState permission="rbac:read" />;
  return (
    <div className="space-y-4">
      <p className="text-sm text-muted-foreground">
        Per-resource grants are additive. Removing one does not revoke access
        supplied by other roles or grants.
      </p>
      <ActionButton
        intent="primary"
        disabled={!create.allowed || query.isError || !query.data}
        disabledReason="Requires rbac:create and an available native RBAC API"
        onClick={() => setShowForm(true)}
      >
        Create native grant
      </ActionButton>
      <QueryStates
        query={query}
        permission="rbac:read"
        errorTitle="Native grants unavailable"
        notFound={
          <StatePanel
            icon={Shield}
            title="Native RBAC is unavailable"
            description="This installation has not enabled the native RBAC API."
          />
        }
      >
        {(page) => (
          <DataTable
            data={page.data}
            columns={columns.map((column) => ({ ...column, sortable: false }))}
            keyExtractor={(row) => row.id}
            searchable={false}
            serverSide={{
              ...pageTableCount(page),
              pagination: { pageIndex, pageSize: 25 },
              onPaginationChange: (next) => setPageIndex(next.pageIndex),
            }}
            emptyState={{
              title: "No native grants",
              description:
                "Create a grant to authorize an exact resource and scope.",
            }}
          />
        )}
      </QueryStates>
      {showForm && !review && (
        <NativeRuleForm
          onClose={() => setShowForm(false)}
          onReview={setReview}
        />
      )}
      <ConfirmDialog
        open={!!review}
        onClose={() => setReview(null)}
        title="Grant native resource access"
        confirmText="Grant access"
        description={`User ${review?.userId}: ${review?.verbs.join(", ")} on ${review?.apiGroup || "core"}/${review?.resource}; ${review?.clusterId || "ALL CLUSTERS"}; ${review?.namespace || "ALL NAMESPACES"}. This adds access.`}
        confirmValue="grant access"
        loading={grant.isPending}
        onConfirm={() => {
          if (create.allowed && review) grant.mutate(review);
        }}
      />
      <ConfirmDialog
        open={!!target}
        onClose={() => setTarget(null)}
        title="Remove native grant"
        confirmText="Remove grant"
        description={`Remove ${target?.resource} access granted to ${target?.userId} by this rule? Other grants remain effective.`}
        confirmValue="remove grant"
        variant="destructive"
        loading={revoke.isPending}
        onConfirm={() => {
          if (remove.allowed && target) revoke.mutate(target.id);
        }}
      />
    </div>
  );
}
