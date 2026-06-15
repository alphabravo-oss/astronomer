'use client';

import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from '@/components/ui/table';
import { useRouter } from 'next/navigation';
import { useClusters, useTools, useClusterToolsStatus } from '@/lib/hooks';
import { cn } from '@/lib/utils';
import { normalizeToolStatus } from '@/lib/tool-status';
import type { Cluster, ClusterTool, ClusterToolStatus, ToolStatus } from '@/types';
import { Wrench, Loader2, Server } from 'lucide-react';

const toolStatusDotColor: Record<ToolStatus, string> = {
  installed: 'bg-status-success',
  installing: 'bg-amber-500',
  upgrading: 'bg-amber-500',
  uninstalling: 'bg-amber-500',
  failed: 'bg-status-error',
  not_installed: 'bg-muted-foreground/30',
  installed_unmanaged: 'bg-blue-500',
  unknown: 'bg-amber-500/50',
};

const toolStatusLabel: Record<ToolStatus, string> = {
  installed: 'Installed',
  installing: 'Installing',
  upgrading: 'Upgrading',
  uninstalling: 'Uninstalling',
  failed: 'Failed',
  not_installed: 'Not Installed',
  installed_unmanaged: 'Unmanaged',
  unknown: 'Unknown',
};

function ClusterToolRow({ cluster, tools }: { cluster: Cluster; tools: ClusterTool[] }) {
  const router = useRouter();
  const { data: statuses } = useClusterToolsStatus(cluster.id);

  const statusMap = new Map<string, ClusterToolStatus>();
  statuses?.forEach((s) => statusMap.set(s.slug, s));

  return (
    <TableRow
      onClick={() => router.push(`/dashboard/clusters/${cluster.id}/tools`)}
      className="border-b border-border hover:bg-muted/30 transition-colors cursor-pointer"
    >
      <TableCell className="px-4 py-3">
        <div className="flex items-center gap-3">
          <Server className="h-4 w-4 text-muted-foreground flex-shrink-0" />
          <div>
            <p className="font-medium text-foreground text-sm">{cluster.displayName}</p>
            <p className="text-xs text-muted-foreground">{cluster.name}</p>
          </div>
        </div>
      </TableCell>
      <TableCell className="px-4 py-3">
        <span className="text-xs text-muted-foreground capitalize">{cluster.environment}</span>
      </TableCell>
      {tools.map((tool) => {
        const toolStatus = statusMap.get(tool.slug);
        const status = normalizeToolStatus(toolStatus?.status);
        return (
          <TableCell key={tool.slug} className="px-4 py-3">
            <div className="flex items-center gap-2">
              <span className="relative flex h-2.5 w-2.5">
                {(status === 'installing' || status === 'upgrading' || status === 'uninstalling') && (
                  <span
                    className={cn(
                      'absolute inline-flex h-full w-full rounded-full opacity-75 animate-ping',
                      toolStatusDotColor[status]
                    )}
                  />
                )}
                <span
                  className={cn('relative inline-flex rounded-full h-2.5 w-2.5', toolStatusDotColor[status])}
                />
              </span>
              <span className="text-xs text-muted-foreground hidden xl:inline">
                {toolStatusLabel[status]}
              </span>
            </div>
          </TableCell>
        );
      })}
    </TableRow>
  );
}

export default function ToolsFleetPage() {
  const { data: clustersData, isLoading: clustersLoading } = useClusters({ pageSize: 100 });
  const { data: tools, isLoading: toolsLoading } = useTools();

  const clusters = clustersData?.data || [];
  const isLoading = clustersLoading || toolsLoading;

  return (
    <div className="space-y-6">
      {/* Header */}
      <div>
        <h1 className="text-2xl font-semibold text-foreground tracking-tight">Cluster Tools</h1>
        <p className="text-sm text-muted-foreground mt-1">
          Manage operational tools across your clusters
        </p>
      </div>

      {/* Table */}
      {isLoading ? (
        <div className="flex items-center justify-center h-64">
          <Loader2 className="h-6 w-6 animate-spin text-muted-foreground" />
        </div>
      ) : clusters.length === 0 ? (
        <div className="flex flex-col items-center justify-center py-16 text-muted-foreground">
          <Server className="h-10 w-10 mb-3" />
          <p className="text-sm">No clusters registered</p>
          <p className="text-xs mt-1">Register a cluster to start managing tools</p>
        </div>
      ) : (
        <div className="rounded-lg border border-border overflow-hidden">
          <div className="overflow-x-auto">
            <Table className="w-full">
              <TableHeader>
                <TableRow className="border-b border-border bg-muted/30">
                  <TableHead className="px-4 py-3 text-left text-xs font-medium text-muted-foreground uppercase tracking-wider">
                    Cluster
                  </TableHead>
                  <TableHead className="px-4 py-3 text-left text-xs font-medium text-muted-foreground uppercase tracking-wider">
                    Environment
                  </TableHead>
                  {(tools || []).map((tool) => (
                    <TableHead
                      key={tool.slug}
                      className="px-4 py-3 text-left text-xs font-medium text-muted-foreground uppercase tracking-wider"
                    >
                      {tool.name}
                    </TableHead>
                  ))}
                </TableRow>
              </TableHeader>
              <TableBody>
                {clusters.map((cluster) => (
                  <ClusterToolRow key={cluster.id} cluster={cluster} tools={tools || []} />
                ))}
              </TableBody>
            </Table>
          </div>
        </div>
      )}
    </div>
  );
}
