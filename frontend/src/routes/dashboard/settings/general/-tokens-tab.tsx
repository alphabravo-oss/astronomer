import { Plus, Trash2 } from "lucide-react";
import { useAPITokens, useDeleteAPIToken } from "@/lib/hooks/user-settings";
import { formatDate, formatRelativeTime } from "@/lib/utils";
import type { APIToken } from "@/types";
import { ActionButton } from "@/components/ui/action-button";
import { DataTable, type Column } from "@/components/ui/data-table";
import { ConfirmDialog } from "@/components/ui/confirm-dialog";

export function TokensTab({ onCreate }: { onCreate: () => void }) {
  const { data: tokens, isLoading: tokensLoading } = useAPITokens();
  const deleteToken = useDeleteAPIToken();
  const [deleteTarget, setDeleteTarget] = useState<APIToken | null>(null);

  const tokenColumns: Column<APIToken>[] = [
    {
      key: "name",
      header: "Name",
      accessor: (row) => (
        <div>
          <p className="font-medium text-foreground">{row.name}</p>
        </div>
      ),
    },
    {
      key: "prefix",
      header: "Prefix",
      accessor: (row) => (
        <span className="font-mono text-xs text-muted-foreground">
          {row.prefix}...
        </span>
      ),
    },
    {
      key: "status",
      header: "Status",
      accessor: (row) => (
        <span className="text-sm text-muted-foreground">
          {row.isRevoked ? "Revoked" : "Active"}
        </span>
      ),
    },
    {
      key: "expires",
      header: "Expires",
      accessor: (row) => (
        <span className="text-xs text-muted-foreground">
          {row.expiresAt ? formatDate(row.expiresAt) : "Never"}
        </span>
      ),
    },
    {
      key: "lastUsed",
      header: "Last Used",
      accessor: (row) => (
        <span className="text-xs text-muted-foreground">
          {row.lastUsedAt ? formatRelativeTime(row.lastUsedAt) : "Never"}
        </span>
      ),
    },
    {
      key: "actions",
      header: "",
      sortable: false,
      accessor: (row) => (
        <button
          type="button"
          onClick={(e) => {
            e.stopPropagation();
            setDeleteTarget(row);
          }}
          className="text-muted-foreground hover:text-status-error transition-colors"
          title="Delete token"
        >
          <Trash2 className="h-4 w-4" />
        </button>
      ),
    },
  ];

  return (
    <div className="space-y-4">
      <div className="flex items-center justify-between">
        <p className="text-sm text-muted-foreground">
          API tokens for programmatic access to the Astronomer API.
        </p>
        <ActionButton
          intent="primary"
          icon={<Plus className="h-4 w-4" />}
          onClick={onCreate}
        >
          Create Token
        </ActionButton>
      </div>

      <DataTable
        data={tokens || []}
        columns={tokenColumns}
        keyExtractor={(row) => row.id}
        searchPlaceholder="Search tokens..."
        loading={tokensLoading}
        emptyState={{
          title: "No API tokens created",
          description: "Create the first item to configure this feature.",
        }}
      />
      <ConfirmDialog
        open={deleteTarget !== null}
        onClose={() => setDeleteTarget(null)}
        onConfirm={() => {
          if (!deleteTarget) return;
          deleteToken.mutate(deleteTarget.id, {
            onSuccess: () => setDeleteTarget(null),
          });
        }}
        title="Delete API token"
        description="This immediately invalidates the selected API credential."
        confirmValue={deleteTarget?.name}
        variant="destructive"
        loading={deleteToken.isPending}
        impact={
          deleteTarget
            ? {
                scope: deleteTarget.name,
                consequences: [
                  "Automations using this token will lose API access.",
                  "The token secret cannot be recovered after deletion.",
                ],
                recovery:
                  "Create a replacement token and update its consumers.",
              }
            : undefined
        }
      />
    </div>
  );
}
import { useState } from "react";
