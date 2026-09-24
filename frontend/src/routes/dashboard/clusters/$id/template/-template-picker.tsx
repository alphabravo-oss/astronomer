import { useState } from "react";
import { PagedSelect } from "@/components/ui/paged-select";
import { ActionButton } from "@/components/ui/action-button";
import { listClusterTemplates } from "@/lib/api/project-detail";
import { queryKeys } from "@/lib/query-keys";

export function TemplatePicker({
  canWrite,
  reason,
  pending,
  onApply,
}: {
  canWrite: boolean;
  reason: string;
  pending: boolean;
  onApply: (id: string) => void;
}) {
  const [selected, setSelected] = useState("");
  const [valid, setValid] = useState(false);
  return (
    <div className="mt-4 flex flex-wrap items-end gap-2">
      <PagedSelect
        label="Cluster template"
        value={selected}
        onChange={setSelected}
        disabled={!canWrite}
        required
        onValidityChange={setValid}
        permission="cluster_templates:read"
        placeholder="Select a template…"
        queryKey={(params) => [...queryKeys.clusterPages.templates, params]}
        fetchPage={(params, signal) =>
          listClusterTemplates({
            offset: params.offset,
            pageSize: params.limit,
            signal,
          })
        }
        optionLabel={(row) => row.displayName || row.name}
      />
      <ActionButton
        intent="primary"
        loading={pending}
        disabled={!valid || !selected || !canWrite}
        disabledReason={!canWrite ? reason : undefined}
        onClick={() => {
          if (valid && selected && canWrite) onApply(selected);
        }}
      >
        Apply Template
      </ActionButton>
    </div>
  );
}
