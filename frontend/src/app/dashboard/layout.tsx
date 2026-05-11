'use client';

import { useEffect } from 'react';
import { useRouter } from 'next/navigation';
import { Sidebar } from '@/components/layout/sidebar';
import { Topbar } from '@/components/layout/topbar';
import { CommandPalette } from '@/components/layout/command-palette';
import { WindowManager } from '@/components/window-manager/window-manager';
import { useUIStore, useAuthStore } from '@/lib/store';
import { useLiveEvents, useLiveClusterMetricsMerger } from '@/lib/live-events';
import { cn } from '@/lib/utils';

export default function DashboardLayout({
  children,
}: {
  children: React.ReactNode;
}) {
  const { sidebarCollapsed } = useUIStore();
  const router = useRouter();
  const mustChangePassword = useAuthStore((s) => s.user?.must_change_password);

  // Bootstrap admin / any user flagged must_change_password: kick them out
  // to the forced-rotation screen before any dashboard data starts loading.
  useEffect(() => {
    if (mustChangePassword) {
      router.replace('/auth/change-password');
    }
  }, [mustChangePassword, router]);

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
      {/*
        Mounted once at the dashboard layout level so the bottom drawer
        persists across navigation between cluster / workload / argo pages.
        Renders nothing unless tabs are open.
      */}
      <WindowManager />
    </div>
  );
}
