import { useEffect, useState } from "react";
import { useHelmChartVersions } from "@/lib/hooks/catalog";
import {
  OffsetPagination,
  useOffsetPagination,
} from "@/components/ui/offset-pagination";
import { QueryStates } from "@/components/ui/query-states";
import { Select } from "@/components/ui/select";
import type { HelmChartVersion } from "@/types";

export function useCatalogVersionSelection(
  projectId: string,
  chartId: string,
  value: string,
  onChange: (id: string) => void,
) {
  const scope = `${projectId}:${chartId}`;
  const control = useOffsetPagination(scope);
  const query = useHelmChartVersions(
    projectId,
    chartId,
    "project",
    control.params,
  );
  const [held, setHeld] = useState<{ scope: string; row: HelmChartVersion }>();
  const current = query.data?.data.find((row) => row.id === value);
  // Remember a resolved selection before moving pages, including an existing
  // upgrade version. Conditional render-time adjustment avoids effect cascades.
  if (
    current &&
    !query.isError &&
    (held?.scope !== scope || held.row !== current)
  )
    setHeld({ scope, row: current });
  useEffect(() => {
    const first = query.data?.data[0];
    if (!value && !query.isError && control.page === 1 && first) {
      onChange(first.id);
    }
  }, [query.data, query.isError, control.page, scope, value, onChange]);
  const selected =
    query.isError || query.isLoading
      ? undefined
      : (current ??
        (held?.scope === scope && held.row.id === value
          ? held.row
          : undefined));
  const choose = (id: string) => {
    const row = query.data?.data.find((item) => item.id === id);
    setHeld(row ? { scope, row } : undefined);
    onChange(id);
  };
  return { query, control, selected, value, choose };
}

export function CatalogVersionSelect({
  selection,
  id,
}: {
  selection: ReturnType<typeof useCatalogVersionSelection>;
  id: string;
}) {
  const { query, control, value, choose, selected } = selection;
  const rows = query.isError ? [] : (query.data?.data ?? []);
  return (
    <div className="space-y-2">
      <Select
        id={id}
        aria-label="Version"
        value={value}
        onChange={(event) => choose(event.target.value)}
        required
      >
        <option value="">Select a version</option>
        {value && !rows.some((row) => row.id === value) && (
          <option value={value}>{selected?.version ?? value}</option>
        )}
        {rows.map((row) => (
          <option key={row.id} value={row.id}>
            {row.version}
            {row.appVersion ? ` (app ${row.appVersion})` : ""}
          </option>
        ))}
      </Select>
      <QueryStates
        query={query}
        permission="catalog:read"
        loadingTitle="Loading versions"
        errorTitle="Could not load versions"
      >
        {rows.length === 0 ? (
          <p className="text-xs text-muted-foreground">
            No versions on this page.
          </p>
        ) : null}
      </QueryStates>
      <OffsetPagination control={control} query={query} label="versions" />
    </div>
  );
}
