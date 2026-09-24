import { useState } from "react";
import { usePodSecurityTemplates } from "@/lib/hooks/security";
import { ActionButton } from "@/components/ui/action-button";
import { DataTable } from "@/components/ui/data-table";
import { pageTableCount } from "@/lib/api/pagination";
import type { PodSecurityTemplate } from "@/types";

export function TemplatePicker({
  value,
  onChange,
}: {
  value: string;
  onChange: (template: PodSecurityTemplate) => void;
}) {
  const [pageIndex, setPageIndex] = useState(0);
  const query = usePodSecurityTemplates({ limit: 25, offset: pageIndex * 25 });
  return (
    <DataTable
      data={query.isError ? [] : (query.data?.data ?? [])}
      loading={query.isLoading}
      isError={query.isError}
      error={query.error}
      permission="security:read"
      onRetry={() => void query.refetch()}
      searchable={false}
      keyExtractor={(row) => row.id}
      columns={[
        {
          key: "name",
          header: "Template",
          accessor: (row) => row.name,
          sortable: false,
        },
        {
          key: "select",
          header: "Selection",
          accessor: (row) => (
            <ActionButton
              aria-pressed={value === row.id}
              onClick={() => onChange(row)}
            >
              {value === row.id ? "Selected" : `Select ${row.name}`}
            </ActionButton>
          ),
          sortable: false,
        },
      ]}
      serverSide={{
        ...pageTableCount(query.data),
        pagination: { pageIndex, pageSize: 25 },
        onPaginationChange: (next) => setPageIndex(next.pageIndex),
      }}
      emptyState={{
        title: "No templates",
        description: "Create a security template before assigning one.",
      }}
    />
  );
}
