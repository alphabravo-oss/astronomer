import { createContext, useContext, useState } from "react";
import { useQuery } from "@tanstack/react-query";
import { Input } from "@/components/ui/input";
import { Select } from "@/components/ui/select";
import { ActionButton } from "@/components/ui/action-button";
import { getGenericResources } from "@/lib/api/resource-search";
import { queryKeys } from "@/lib/query-keys";

export const CatalogResourceContext = createContext({
  clusterId: "",
  namespace: "",
});
export type CatalogResourceKind =
  "storageclasses" | "ingressclasses" | "secrets";

/** Uses the authorized metadata list API; never fetches a Secret's contents. */
export function CatalogResourceField({
  id,
  kind,
  value,
  required,
  onChange,
}: {
  id: string;
  kind: CatalogResourceKind;
  value: string;
  required: boolean;
  onChange: (value: string | undefined) => void;
}) {
  const { clusterId, namespace } = useContext(CatalogResourceContext);
  const [search, setSearch] = useState("");
  const [offset, setOffset] = useState(0);
  const params = {
    namespace: kind === "secrets" ? namespace : undefined,
    search,
    limit: 25,
    offset,
  };
  const query = useQuery({
    queryKey: queryKeys.generic.resources(clusterId, kind, params),
    queryFn: ({ signal }) =>
      getGenericResources(clusterId, kind, { ...params, signal }),
    enabled: !!clusterId && (kind !== "secrets" || !!namespace),
    throwOnError: false,
  });
  const names = (query.data?.data ?? [])
    .map((row) => String(row.name ?? ""))
    .filter(Boolean);
  return (
    <div className="space-y-2">
      <Input
        aria-label={`Search ${kind}`}
        value={search}
        onChange={(event) => {
          setSearch(event.target.value);
          setOffset(0);
        }}
        placeholder="Search available resources"
      />
      <Select
        id={id}
        value={value}
        required={required}
        onChange={(event) => onChange(event.target.value)}
        disabled={!clusterId || query.isError}
      >
        <option value="">
          {required ? "Select a resource" : "Empty / chart-specific value"}
        </option>
        {value && !names.includes(value) && (
          <option value={value}>{value} (current value)</option>
        )}
        {names.map((name) => (
          <option key={name} value={name}>
            {name}
          </option>
        ))}
      </Select>
      {query.isError && (
        <p role="alert" className="text-xs text-status-error">
          Resources could not be loaded. Check your access to this cluster and
          namespace. Your current value is preserved.
        </p>
      )}
      {query.isFetching && (
        <p role="status" className="text-xs text-muted-foreground">
          Loading resources…
        </p>
      )}
      <ActionButton size="sm" onClick={() => onChange(undefined)}>
        Use chart default
      </ActionButton>
      <div className="flex gap-2">
        {offset > 0 && (
          <ActionButton
            size="sm"
            onClick={() => setOffset(Math.max(0, offset - 25))}
          >
            Previous resources
          </ActionButton>
        )}
        {query.data?.pagination.has_more && (
          <ActionButton size="sm" onClick={() => setOffset(offset + 25)}>
            More resources
          </ActionButton>
        )}
      </div>
    </div>
  );
}
