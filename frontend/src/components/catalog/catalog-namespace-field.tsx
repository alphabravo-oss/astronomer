import { useQuery } from "@tanstack/react-query";
import { Input } from "@/components/ui/input";
import { getGenericResources } from "@/lib/api/resource-search";
import { queryKeys } from "@/lib/query-keys";

export function CatalogNamespaceField({
  id,
  clusterId,
  value,
  disabled,
  onChange,
}: {
  id: string;
  clusterId: string;
  value: string;
  disabled: boolean;
  onChange: (value: string) => void;
}) {
  const params = { search: value, limit: 25, offset: 0 };
  const query = useQuery({
    queryKey: queryKeys.generic.resources(clusterId, "namespaces", params),
    queryFn: ({ signal }) =>
      getGenericResources(clusterId, "namespaces", { ...params, signal }),
    enabled: !!clusterId && !disabled,
    throwOnError: false,
  });
  return (
    <div className="space-y-1">
      <Input
        id={id}
        list={`${id}-choices`}
        value={value}
        disabled={disabled}
        onChange={(event) => onChange(event.target.value)}
      />
      <datalist id={`${id}-choices`}>
        {query.data?.data.map((row) => (
          <option key={String(row.name)} value={String(row.name)} />
        ))}
      </datalist>
      {!disabled && (
        <p className="text-xs text-muted-foreground">
          Choose an existing namespace or enter a new one. Access is checked
          before installation.
        </p>
      )}
      {query.data?.pagination.has_more && (
        <p className="text-xs text-muted-foreground">
          Type more to narrow the namespace choices.
        </p>
      )}
      {query.isError && (
        <p className="text-xs text-muted-foreground">
          Namespace suggestions unavailable; your entry is preserved.
        </p>
      )}
    </div>
  );
}
