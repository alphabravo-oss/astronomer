'use client';

import { useEffect } from 'react';
import { usePathname, useRouter } from 'next/navigation';
import { Sidebar } from '@/components/layout/sidebar';
import { Topbar } from '@/components/layout/topbar';
import { CommandPalette } from '@/components/layout/command-palette';
import { WindowManager } from '@/components/window-manager/window-manager';
import { EmptyState } from '@/components/ui/empty-state';
import { useUIStore, useAuthStore } from '@/lib/store';
import { useCurrentUser, useFeatureFlags } from '@/lib/hooks';
import type { FeatureFlags, FeatureFlagKey } from '@/lib/api';
import { useLiveEvents, useLiveClusterMetricsMerger } from '@/lib/live-events';
import { cn } from '@/lib/utils';
import { Lock } from 'lucide-react';

export default function DashboardLayout({
  children,
}: {
  children: React.ReactNode;
}) {
  const { sidebarCollapsed } = useUIStore();
  const router = useRouter();
  const pathname = usePathname();
  const updateUser = useAuthStore((s) => s.updateUser);
  const { data: currentUser, isFetched: currentUserFetched } = useCurrentUser();
  const { data: featureFlags } = useFeatureFlags();
  const mustChangePassword = currentUser
    ? currentUser.mustChangePassword || currentUser.must_change_password
    : false;
  const disabledFeature = disabledFeatureForPath(pathname, featureFlags);

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
