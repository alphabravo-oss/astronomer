import { useState } from "react";
import { useQuery } from "@tanstack/react-query";
import { ActionButton } from "@/components/ui/action-button";
import { DataTable } from "@/components/ui/data-table";
import { ModalShell } from "@/components/ui/modal-shell";
import { useProject } from "@/lib/hooks/projects";
import { getProjects, getClusterProjects } from "@/lib/api/projects";
import { pageTableCount } from "@/lib/api/pagination";
import { queryKeys } from "@/lib/query-keys";

/** Remote search and pagination; the selected project is resolved separately. */
export function RemoteProjectPicker({
  value,
  onChange,
  clusterId,
  ariaLabel = "Project",
  id,
}: {
  value: string;
  onChange: (id: string) => void;
  clusterId?: string;
  ariaLabel?: string;
  id?: string;
}) {
  const [open, setOpen] = useState(false);
  const [search, setSearch] = useState("");
  const [pageIndex, setPageIndex] = useState(0);
  const selected = useProject(value, { throwOnError: false });
  const params = { page: pageIndex + 1, pageSize: 25, search };
  const query = useQuery({
    queryKey: queryKeys.projects.picker(clusterId, params),
    queryFn: ({ signal }) =>
      clusterId
        ? getClusterProjects(clusterId, params, { signal })
        : getProjects(params, { signal }),
    enabled: open,
    throwOnError: false,
  });
  const label = selected.isError
    ? value
    : selected.data?.displayName || selected.data?.name || value;
  return (
    <>
      <ActionButton
        id={id}
        aria-label={ariaLabel}
        onClick={() => setOpen(true)}
      >
        {label || "Select a project…"}
      </ActionButton>
      {open && (
        <ModalShell
          title={`Select ${ariaLabel.toLowerCase()}`}
          onClose={() => setOpen(false)}
          size="lg"
        >
          <DataTable
            data={query.isError ? [] : (query.data?.data ?? [])}
            loading={query.isLoading}
            isError={query.isError}
            error={query.error}
            permission="projects:read"
            onRetry={() => void query.refetch()}
            columns={[
              {
                key: "name",
                header: "Project",
                accessor: (row) => row.displayName || row.name,
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
                    Select {row.displayName || row.name}
                  </ActionButton>
                ),
              },
            ]}
            keyExtractor={(row) => row.id}
            pageSize={25}
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
            emptyState={{
              title: "No matching projects",
              description: "Change the search or request project access.",
            }}
          />
        </ModalShell>
      )}
    </>
  );
}
