import { useState } from "react";
import { useQuery } from "@tanstack/react-query";
import { ActionButton } from "@/components/ui/action-button";
import { ModalShell } from "@/components/ui/modal-shell";
import { DataTable } from "@/components/ui/data-table";
import { getRolePage } from "@/lib/api/rbac-role-page";
import { type RoleScope } from "@/lib/api/rbac";
import { pageTableCount } from "@/lib/api/pagination";
import { queryKeys } from "@/lib/query-keys";
import { usePermissionDecision } from "@/lib/permission-hooks";
import { roleTitle } from "./binding-utils";
import { PermissionState } from "@/components/ui/empty-state";

export function RemoteRolePicker({
  scope,
  value,
  onChange,
  id,
}: {
  scope: RoleScope;
  value: string;
  onChange: (id: string) => void;
  id?: string;
}) {
  const [open, setOpen] = useState(false);
  const [pageIndex, setPageIndex] = useState(0);
  const read = usePermissionDecision("rbac", "read");
  const query = useQuery({
    queryKey: queryKeys.rbac.rolePage(scope, pageIndex),
    queryFn: ({ signal }) => getRolePage(scope, pageIndex * 25, signal),
    enabled: open && read.allowed,
    throwOnError: false,
  });
  const rows = read.allowed && !query.isError ? (query.data?.data ?? []) : [];
  const selected = rows.find((role) => role.id === value);
  return (
    <>
      <ActionButton
        id={id}
        aria-label="Role"
        disabled={!read.allowed}
        disabledReason="Selecting roles requires rbac:read."
        onClick={() => setOpen(true)}
      >
        {selected ? roleTitle(selected) : value || `Select a ${scope} role…`}
      </ActionButton>
      {open && (
        <ModalShell
          title={`Select ${scope} role`}
          onClose={() => setOpen(false)}
        >
          {!read.allowed ? (
            <PermissionState permission="rbac:read" />
          ) : (
            <DataTable
              data={rows}
              loading={query.isLoading}
              isError={query.isError}
              error={query.error}
              permission="rbac:read"
              onRetry={() => void query.refetch()}
              keyExtractor={(row) => row.id}
              searchable={false}
              columns={[
                {
                  key: "name",
                  header: "Role",
                  accessor: roleTitle,
                  sortable: false,
                },
                {
                  key: "select",
                  header: "Select",
                  sortable: false,
                  accessor: (row) => (
                    <ActionButton
                      onClick={() => {
                        onChange(row.id);
                        setOpen(false);
                      }}
                    >
                      Select {roleTitle(row)}
                    </ActionButton>
                  ),
                },
              ]}
              serverSide={{
                ...pageTableCount(query.isError ? undefined : query.data),
                pagination: { pageIndex, pageSize: 25 },
                onPaginationChange: (next) => setPageIndex(next.pageIndex),
              }}
              emptyState={{
                title: "No roles available",
                description: "Request role access or create a role.",
              }}
            />
          )}
        </ModalShell>
      )}
    </>
  );
}
