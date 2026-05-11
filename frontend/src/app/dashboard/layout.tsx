'use client';

import { Sidebar } from '@/components/layout/sidebar';
import { Topbar } from '@/components/layout/topbar';
import { CommandPalette } from '@/components/layout/command-palette';
import { useUIStore } from '@/lib/store';
import { useLiveEvents, useLiveClusterMetricsMerger } from '@/lib/live-events';
import { cn } from '@/lib/utils';

export default function DashboardLayout({
  children,
}: {
  children: React.ReactNode;
}) {
  const { sidebarCollapsed } = useUIStore();
  // Hold a single SSE connection open for the whole dashboard; per-page
  // hooks reuse this connection via refcount inside `lib/live-events.ts`.
  useLiveEvents();
  // Patch React Query caches in place when cluster.metrics / status events
  // arrive so cards / tables tick without paying a refetch on every event.
  useLiveClusterMetricsMerger();

  return (
    <div className="flex h-screen overflow-hidden bg-background">
      <Sidebar />
      <div
        className={cn(
          'flex flex-col flex-1 min-w-0 overflow-hidden',
        )}
      >
        <Topbar />
        <main className="flex-1 min-h-0 overflow-y-auto">
          <div className="p-6 max-w-[1600px] mx-auto animate-fade-in">
            {children}
          </div>
        </main>
        <div id="bottom-panel-root" />
      </div>
      <CommandPalette />
    </div>
  );
}
