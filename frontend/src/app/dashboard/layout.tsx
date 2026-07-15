'use client';

import { useEffect, useState } from 'react';
import { usePathname, useRouter } from '@/lib/navigation';
import { Sidebar } from '@/components/layout/sidebar';
import { Topbar } from '@/components/layout/topbar';
import { CommandPalette } from '@/components/layout/command-palette';
import { WindowManager } from '@/components/window-manager/window-manager';
import { ExtensionProvider } from '@/components/extensions/ExtensionProvider';
import { EmptyState } from '@/components/ui/empty-state';
import { useAuthStore } from '@/lib/store';
import { useCurrentUser, useFeatureFlags } from '@/lib/hooks';
import type { FeatureFlags, FeatureFlagKey } from '@/lib/api';
import { useLiveEvents, useLiveClusterMetricsMerger } from '@/lib/live-events';
import { cn } from '@/lib/utils';
import { Lock, WifiOff } from 'lucide-react';

export default function DashboardLayout({
  children,
}: {
  children: React.ReactNode;
}) {
  const router = useRouter();
  const pathname = usePathname();
  const updateUser = useAuthStore((s) => s.updateUser);
  const { data: currentUser, isFetched: currentUserFetched } = useCurrentUser();
  const { data: featureFlags } = useFeatureFlags();
  const mustChangePassword = currentUser
    ? currentUser.mustChangePassword || currentUser.must_change_password
    : false;
  const disabledFeature = disabledFeatureForPath(pathname, featureFlags);
  // UX-05: surface browser offline so hung tables/mutations are explained.
  const [online, setOnline] = useState(true);
  useEffect(() => {
    if (typeof navigator === 'undefined') return;
    const sync = () => setOnline(navigator.onLine);
    sync();
    window.addEventListener('online', sync);
    window.addEventListener('offline', sync);
    return () => {
      window.removeEventListener('online', sync);
      window.removeEventListener('offline', sync);
    };
  }, []);

  useEffect(() => {
    if (currentUser) {
      updateUser(currentUser);
    }
  }, [currentUser, updateUser]);

  // Bootstrap admin / any user flagged must_change_password: kick them out
  // to the forced-rotation screen before any dashboard data starts loading.
  useEffect(() => {
    if (currentUserFetched && mustChangePassword) {
      router.replace('/auth/change-password');
    }
  }, [currentUserFetched, mustChangePassword, router]);

  // Hold a single SSE connection open for the whole dashboard; per-page
  // hooks reuse this connection via refcount inside `lib/live-events.ts`.
  useLiveEvents();
  // Patch React Query caches in place when cluster.metrics / status events
  // arrive so cards / tables tick without paying a refetch on every event.
  useLiveClusterMetricsMerger();

  return (
    // ExtensionProvider wraps the whole dashboard shell once: it fetches
    // GET /extensions/mounts/ a single time and exposes the indexed registry to
    // every <ExtensionSlot> (sidebar nav, dashboard widgets, cluster tabs,
    // settings pages). Render-agnostic, so a broken extension can't reach here.
    <ExtensionProvider>
      <div className="flex h-screen overflow-hidden bg-background">
        <Sidebar />
        <div
          className={cn(
            'flex flex-col flex-1 min-w-0 overflow-hidden',
          )}
        >
          <Topbar />
          {!online && (
            <div
              role="status"
              className="flex items-center gap-2 bg-amber-500/15 text-amber-900 dark:text-amber-100 border-b border-amber-500/30 px-4 py-2 text-sm"
            >
              <WifiOff className="h-4 w-4 shrink-0" />
              You are offline. Live updates and mutations will fail until connectivity returns.
            </div>
          )}
          <main className="flex-1 min-h-0 overflow-y-auto">
            <div className="p-6 max-w-[1600px] mx-auto animate-fade-in">
              {disabledFeature ? <FeatureDisabledState /> : children}
            </div>
          </main>
        </div>
        <CommandPalette />
        {/*
          Mounted once at the dashboard layout level so the bottom drawer
          persists across navigation between cluster / workload / argo pages.
          Renders nothing unless tabs are open.
        */}
        <WindowManager />
      </div>
    </ExtensionProvider>
  );
}

const featurePathPrefixes: Array<{ prefix: string; flag: FeatureFlagKey }> = [
  { prefix: '/dashboard/projects', flag: 'feature.projects' },
  { prefix: '/dashboard/argocd', flag: 'feature.argocd' },
  { prefix: '/dashboard/backups', flag: 'feature.backups' },
  { prefix: '/dashboard/catalog', flag: 'feature.catalog' },
  { prefix: '/dashboard/tools', flag: 'feature.catalog' },
  { prefix: '/dashboard/monitoring', flag: 'feature.monitoring' },
  { prefix: '/dashboard/security', flag: 'feature.security' },
];

function disabledFeatureForPath(pathname: string, flags?: FeatureFlags): FeatureFlagKey | null {
  if (!flags) return null;
  const match = featurePathPrefixes.find(({ prefix }) => pathname === prefix || pathname.startsWith(`${prefix}/`));
  if (!match) return null;
  return flags[match.flag] === false ? match.flag : null;
}

function FeatureDisabledState() {
  return (
    <EmptyState
      icon={Lock}
      title="Section disabled"
      description="This section is disabled by platform settings."
      className="rounded-lg border border-border bg-card p-8"
    />
  );
}
