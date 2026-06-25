'use client';

// §HostMounts — DeclarativeWidget: the Tier-1 entry point ExtensionSlot mounts.
//
// switch(kind){ table->ExtTable, chart->ExtChart, stat->ExtStat, form->ExtForm }.
// table/chart/stat fetch their rows through the data proxy (React Query, keyed by
// (name, dataSource, context) so the same widget on different clusters caches
// independently); the server re-derives the upstream + re-runs RBAC on every
// call. form does not read — it renders inputs and POSTs on submit. Loading,
// empty, and error are handled here so every renderer gets a populated/known
// state. Zero third-party JS: values are painted as text nodes downstream.

import { useQuery } from '@tanstack/react-query';
import { fetchExtensionData } from '@/lib/api/extensions';
import { queryKeys } from '@/lib/query-keys';
import { LoadingState, ErrorState } from '@/components/ui/empty-state';
import { extractRows, extractObject, isEmptyResponse } from './declarative';
import { ExtTable } from './ExtTable';
import { ExtChart } from './ExtChart';
import { ExtStat } from './ExtStat';
import { ExtForm } from './ExtForm';
import type {
  DeclarativeWidget as DeclarativeWidgetSpec,
  ExtensionContext,
} from '@/lib/api/extensions';

export interface DeclarativeWidgetProps {
  extensionName: string;
  spec: DeclarativeWidgetSpec;
  context?: ExtensionContext;
}

export function DeclarativeWidget({ extensionName, spec, context }: DeclarativeWidgetProps) {
  // A form is a write surface: it never reads through the proxy, so render it
  // directly without a data fetch.
  if (spec.kind === 'form') {
    if (!spec.form) {
      return <ErrorState title="Invalid widget" description="form spec missing" />;
    }
    return <ExtForm extensionName={extensionName} spec={spec.form} context={context} />;
  }

  return (
    <FetchingWidget extensionName={extensionName} spec={spec} context={context} />
  );
}

// table/chart/stat: fetch then dispatch. Split out so the hooks below only run
// for the read kinds (a form returns before reaching here).
function FetchingWidget({ extensionName, spec, context }: DeclarativeWidgetProps) {
  const { data, isLoading, isError, error, refetch } = useQuery({
    queryKey: queryKeys.extensions.data(
      extensionName,
      spec.dataSource,
      context as Record<string, unknown> | undefined,
    ),
    queryFn: () => fetchExtensionData(extensionName, spec.dataSource, { context }),
    staleTime: 30_000,
  });

  if (isLoading) {
    return <LoadingState title="Loading" className="py-8" />;
  }
  if (isError) {
    return (
      <ErrorState
        title="Failed to load"
        description={error instanceof Error ? error.message : undefined}
        onRetry={() => refetch()}
        className="py-8"
      />
    );
  }

  // Shape the response per kind. The empty branch uses the manifest's emptyText.
  switch (spec.kind) {
    case 'table': {
      const rows = extractRows(data);
      return <ExtTable rows={rows} fields={spec.fields} emptyText={spec.emptyText} />;
    }
    case 'chart': {
      if (!spec.chart) {
        return <ErrorState title="Invalid widget" description="chart spec missing" />;
      }
      const rows = extractRows(data);
      return <ExtChart rows={rows} spec={spec.chart} emptyText={spec.emptyText} />;
    }
    case 'stat': {
      if (!spec.stat) {
        return <ErrorState title="Invalid widget" description="stat spec missing" />;
      }
      if (isEmptyResponse(data, 'object')) {
        return (
          <div className="rounded-lg border border-border bg-card p-5 text-sm text-muted-foreground">
            {spec.emptyText || 'No data'}
          </div>
        );
      }
      return <ExtStat row={extractObject(data)} spec={spec.stat} emptyText={spec.emptyText} />;
    }
    default:
      // Unknown kind from a malformed manifest -> render nothing rather than throw.
      return null;
  }
}
