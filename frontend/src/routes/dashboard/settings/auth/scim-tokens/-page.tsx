/**
 * /dashboard/settings/auth/scim-tokens — SCIM provisioning tokens (F-05).
 *
 * Mint, list, and revoke the static bearer tokens that authenticate the
 * /scim/v2/* provisioning chain. The plaintext token is shown exactly once,
 * immediately after creation; list rows only ever carry metadata.
 */
import { useState } from "react";
import { Link as RouterLink, useNavigate } from "@tanstack/react-router";
import { ArrowLeft, Plus, Trash2, KeyRound } from "lucide-react";
import { DataTable, type Column } from "@/components/ui/data-table";
import { ConfirmDialog } from "@/components/ui/confirm-dialog";
import { ActionButton } from "@/components/ui/action-button";
import { PageHeader, PageShell } from "@/components/ui/page";
import { SettingsAuthGate } from "@/components/settings/auth-gate";
import { formatRelativeTime } from "@/lib/utils";
import type { SCIMToken } from "@/types";
import { useSCIMTokens, useRevokeSCIMToken } from "./-hooks";

function SCIMTokensList() {
  const navigate = useNavigate();
  const { data, isLoading, isError, refetch } = useSCIMTokens();
  const revoke = useRevokeSCIMToken();

  const [revokeTarget, setRevokeTarget] = useState<SCIMToken | null>(null);

  const columns: Column<SCIMToken>[] = [
    {
      key: "name",
      header: "Name",
      accessor: (row) => (
        <div className="flex items-center gap-2">
          <KeyRound className="h-4 w-4 text-muted-foreground" />
          <span className="font-medium text-foreground">{row.name}</span>
        </div>
      ),
    },
    {
      key: "prefix",
      header: "Token",
      accessor: (row) => (
        <span className="text-xs font-mono text-muted-foreground">
          {row.prefix}…
        </span>
      ),
      sortable: false,
    },
    {
      key: "lastUsedAt",
      header: "Last used",
      accessor: (row) => (
        <span className="text-xs text-muted-foreground">
          {row.lastUsedAt ? formatRelativeTime(row.lastUsedAt) : "Never"}
        </span>
      ),
    },
    {
      key: "createdAt",
      header: "Created",
      accessor: (row) => (
        <span className="text-xs text-muted-foreground">
          {formatRelativeTime(row.createdAt)}
        </span>
      ),
    },
    {
      key: "actions",
      header: "",
      sortable: false,
      accessor: (row) => (
        <button
          onClick={(e) => {
            e.stopPropagation();
            setRevokeTarget(row);
          }}
          className="p-1.5 rounded-sm text-muted-foreground hover:text-status-error hover:bg-status-error/10 transition-colors"
          title="Revoke token"
        >
          <Trash2 className="h-3.5 w-3.5" />
        </button>
      ),
    },
  ];

  return (
    <>
      <div className="flex items-center justify-end">
        <ActionButton
          intent="primary"
          icon={<Plus className="h-4 w-4" />}
          onClick={() =>
            void navigate({ to: "/dashboard/settings/auth/scim-tokens/new" })
          }
        >
          Mint Token
        </ActionButton>
      </div>

      <DataTable
        data={data ?? []}
        columns={columns}
        keyExtractor={(row) => row.id}
        loading={isLoading}
        isError={isError}
        onRetry={() => refetch()}
        searchPlaceholder="Search tokens..."
        emptyState={{
          title: "No SCIM tokens minted",
          description: "Create the first item to configure this feature.",
        }}
      />

      <ConfirmDialog
        open={!!revokeTarget}
        onClose={() => setRevokeTarget(null)}
        onConfirm={async () => {
          if (!revokeTarget) return;
          await revoke.mutateAsync(revokeTarget.id);
          setRevokeTarget(null);
        }}
        title="Revoke SCIM token?"
        description={`Any IdP using "${revokeTarget?.name}" will immediately fail to provision. This cannot be undone.`}
        confirmText="Revoke"
        confirmValue={revokeTarget?.name}
        variant="destructive"
        loading={revoke.isPending}
      />
    </>
  );
}

export default function SCIMTokensPage() {
  return (
    <SettingsAuthGate>
      <PageShell>
        <RouterLink
          to="/dashboard/settings/auth"
          className="inline-flex items-center gap-1.5 text-xs text-muted-foreground hover:text-foreground transition-colors"
        >
          <ArrowLeft className="h-3.5 w-3.5" />
          Back to Auth
        </RouterLink>
        <PageHeader
          eyebrow="Settings · Auth · SCIM"
          title="SCIM Provisioning Tokens"
          description={
            <>
              Bearer tokens that authenticate your IdP&apos;s SCIM 2.0
              provisioning requests to
              <code className="mx-1 text-2xs font-mono">/scim/v2</code>. Mint
              one per IdP; revoke to cut off provisioning.
            </>
          }
        />
        <SCIMTokensList />
      </PageShell>
    </SettingsAuthGate>
  );
}
