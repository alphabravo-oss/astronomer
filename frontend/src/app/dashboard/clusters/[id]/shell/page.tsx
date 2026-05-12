'use client';

// Migration 065 / sprint 17 — in-browser kubectl shell page.
//
// Full-page xterm.js terminal wired to a kubectl_sessions row. The page
// itself just hosts the ClusterShell component; lifecycle (open / close /
// stream) lives there.

import { useParams } from 'next/navigation';
import { useCluster } from '@/lib/hooks';
import { ClusterShell } from '@/components/clusters/cluster-shell';
import { Loader2, Server, TerminalSquare } from 'lucide-react';

export default function ClusterShellPage() {
  const params = useParams();
  const clusterId = params?.id as string;
  const { data: cluster, isLoading } = useCluster(clusterId);

  if (isLoading) {
    return (
      <div className="flex items-center justify-center h-64">
        <Loader2 className="h-6 w-6 animate-spin text-muted-foreground" />
      </div>
    );
  }

  if (!cluster) {
    return (
      <div className="flex flex-col items-center justify-center h-64 text-muted-foreground">
        <Server className="h-8 w-8 mb-3" />
        <p>Cluster not found</p>
      </div>
    );
  }

  if (cluster.isLocal) {
    return (
      <div className="flex flex-col items-center justify-center h-64 text-muted-foreground gap-2 max-w-md mx-auto text-center">
        <TerminalSquare className="h-8 w-8 mb-2" />
        <p className="text-sm font-medium text-foreground">
          Shell isn&apos;t available on the management plane&apos;s own cluster.
        </p>
        <p className="text-xs">
          The kubectl shell flow needs a real remote agent and tunnel. Use
          <code className="mx-1 px-1.5 py-0.5 rounded bg-muted font-mono">kubectl exec</code>
          directly against this cluster, or register a managed cluster and open
          a shell there.
        </p>
      </div>
    );
  }

  return (
    <div className="h-[calc(100vh-8rem)]">
      <ClusterShell clusterId={clusterId} />
    </div>
  );
}
