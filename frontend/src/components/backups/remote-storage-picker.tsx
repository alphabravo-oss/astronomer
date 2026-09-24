import { useState } from "react";
import { useQuery } from "@tanstack/react-query";
import { b2ListStorageLocations } from "@/lib/api/backups";
import { usePermissionDecision } from "@/lib/permission-hooks";
import { pageTableCount } from "@/lib/api/pagination";
import { Input } from "@/components/ui/input";
import { ActionButton } from "@/components/ui/action-button";
import { ModalShell } from "@/components/ui/modal-shell";
import { DataTable } from "@/components/ui/data-table";
import { b2Keys } from "./hooks";
import { PermissionState } from "@/components/ui/empty-state";

/** Known IDs remain usable without granting permission to enumerate backups. */
export function RemoteStoragePicker({
  value,
  onChange,
  label,
}: {
  value: string;
  onChange: (id: string) => void;
  label: string;
}) {
  const [open, setOpen] = useState(false);
  const [pageIndex, setPageIndex] = useState(0);
  const read = usePermissionDecision("backups", "read");
  const params = { page: pageIndex + 1, page_size: 25 };
  const query = useQuery({
    queryKey: b2Keys.storage(params),
    queryFn: () => b2ListStorageLocations(params),
    enabled: open && read.allowed,
    throwOnError: false,
  });
  return (
    <div className="space-y-2 mt-1">
      <Input
        aria-label={label}
        value={value}
        onChange={(event) => onChange(event.target.value)}
        placeholder="Storage configuration ID"
      />
      <ActionButton
        disabled={!read.allowed}
        disabledReason="Searching storage requires backups:read; you can enter a known ID."
        onClick={() => setOpen(true)}
      >
        Find storage configuration
      </ActionButton>
      {open && (
        <ModalShell
          title="Select storage configuration"
          onClose={() => setOpen(false)}
        >
          {!read.allowed ? (
            <PermissionState permission="backups:read" />
          ) : (
            <DataTable
              data={
                !read.allowed || query.isError ? [] : (query.data?.data ?? [])
              }
              loading={query.isLoading}
              isError={query.isError}
              error={query.error}
              permission="backups:read"
              onRetry={() => void query.refetch()}
              searchable={false}
              keyExtractor={(row) => row.id}
              columns={[
                {
                  key: "name",
                  header: "Name",
                  accessor: (row) => row.name,
                  sortable: false,
                },
                {
                  key: "bucket",
                  header: "Bucket",
                  accessor: (row) => row.bucket,
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
                      Select {row.name}
                    </ActionButton>
                  ),
                },
              ]}
              serverSide={{
                ...pageTableCount(
                  query.isError || !read.allowed ? undefined : query.data,
                ),
                pagination: { pageIndex, pageSize: 25 },
                onPaginationChange: (next) => setPageIndex(next.pageIndex),
              }}
              emptyState={{
                title: "No storage configurations",
                description:
                  "Create a backup storage configuration or enter a known ID.",
              }}
            />
          )}
        </ModalShell>
      )}
    </div>
  );
}
