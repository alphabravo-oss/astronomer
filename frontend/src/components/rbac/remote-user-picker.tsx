import { useState } from "react";
import { useUsers } from "@/lib/hooks/user-settings";
import { usePermissionDecision } from "@/lib/permission-hooks";
import { pageTableCount } from "@/lib/api/pagination";
import { ActionButton } from "@/components/ui/action-button";
import { Input } from "@/components/ui/input";
import { ModalShell } from "@/components/ui/modal-shell";
import { DataTable } from "@/components/ui/data-table";

export function RemoteUserPicker({
  value,
  onChange,
}: {
  value: string;
  onChange: (id: string) => void;
}) {
  const [open, setOpen] = useState(false);
  const [pageIndex, setPageIndex] = useState(0);
  const [search, setSearch] = useState("");
  const permission = usePermissionDecision("users", "read");
  const query = useUsers(
    { page: pageIndex + 1, pageSize: 25, search },
    { enabled: open && permission.allowed },
  );
  return (
    <div className="space-y-2">
      <Input
        aria-label="User UUID (blank for me)"
        value={value}
        onChange={(event) => onChange(event.target.value)}
        placeholder="Me (or enter user UUID)"
      />
      <ActionButton
        disabled={!permission.allowed}
        disabledReason="Searching users requires users:read; a known UUID does not require enumeration."
        onClick={() => setOpen(true)}
      >
        Find user
      </ActionButton>
      {open && (
        <ModalShell title="Select user" onClose={() => setOpen(false)}>
          <DataTable
            data={query.isError ? [] : (query.data?.data ?? [])}
            loading={query.isLoading}
            isError={query.isError}
            error={query.error}
            permission="users:read"
            onRetry={() => void query.refetch()}
            keyExtractor={(row) => row.id}
            columns={[
              {
                key: "user",
                header: "User",
                accessor: (row) => row.displayName || row.username,
                sortable: false,
              },
              {
                key: "select",
                header: "Select",
                accessor: (row) => (
                  <ActionButton
                    onClick={() => {
                      onChange(row.id);
                      setOpen(false);
                    }}
                  >
                    Select {row.username}
                  </ActionButton>
                ),
                sortable: false,
              },
            ]}
            serverSide={{
              ...pageTableCount(query.data),
              pagination: { pageIndex, pageSize: 25 },
              onPaginationChange: (next) => setPageIndex(next.pageIndex),
              search: {
                value: search,
                onChange: (term) => {
                  setSearch(term);
                  setPageIndex(0);
                },
              },
            }}
          />
        </ModalShell>
      )}
    </div>
  );
}
