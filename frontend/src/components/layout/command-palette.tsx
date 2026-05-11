'use client';

import { useCallback, useEffect, useState } from 'react';
import { useRouter } from 'next/navigation';
import { Command } from 'cmdk';
import {
  LayoutDashboard,
  Server,
  BarChart3,
  GitBranch,
  Shield,
  Settings,
  Search,
  ArrowRight,
} from 'lucide-react';
import { useUIStore } from '@/lib/store';
import { useClusters } from '@/lib/hooks';

const pages = [
  { name: 'Dashboard', href: '/dashboard', icon: LayoutDashboard },
  { name: 'Clusters', href: '/dashboard/clusters', icon: Server },
  { name: 'Monitoring', href: '/dashboard/monitoring', icon: BarChart3 },
  { name: 'ArgoCD', href: '/dashboard/argocd', icon: GitBranch },
  { name: 'RBAC', href: '/dashboard/rbac', icon: Shield },
  { name: 'Settings', href: '/dashboard/settings', icon: Settings },
];

export function CommandPalette() {
  const router = useRouter();
  const { commandPaletteOpen, setCommandPaletteOpen } = useUIStore();
  const { data: clustersData } = useClusters({ pageSize: 50 });
  const [search, setSearch] = useState('');

  // Keyboard shortcut
  useEffect(() => {
    function handleKeyDown(e: KeyboardEvent) {
      if ((e.metaKey || e.ctrlKey) && e.key === 'k') {
        e.preventDefault();
        setCommandPaletteOpen(!commandPaletteOpen);
      }
      if (e.key === 'Escape') {
        setCommandPaletteOpen(false);
      }
    }
    document.addEventListener('keydown', handleKeyDown);
    return () => document.removeEventListener('keydown', handleKeyDown);
  }, [commandPaletteOpen, setCommandPaletteOpen]);

  const navigate = useCallback(
    (href: string) => {
      router.push(href);
      setCommandPaletteOpen(false);
      setSearch('');
    },
    [router, setCommandPaletteOpen]
  );

  const selectCluster = useCallback(
    (cluster: import('@/types').Cluster) => {
      // The cluster context is encoded in the URL slug, not in any global
      // store — navigating is sufficient.
      router.push(`/dashboard/clusters/${cluster.id}`);
      setCommandPaletteOpen(false);
      setSearch('');
    },
    [router, setCommandPaletteOpen]
  );

  if (!commandPaletteOpen) return null;

  return (
    <div className="fixed inset-0 z-50">
      {/* Backdrop */}
      <div
        className="fixed inset-0 bg-black/60 backdrop-blur-sm"
        onClick={() => setCommandPaletteOpen(false)}
      />

      {/* Palette */}
      <div className="fixed top-[20%] left-1/2 -translate-x-1/2 w-full max-w-lg">
        <Command
          className="rounded-xl border border-border bg-popover shadow-2xl overflow-hidden"
          shouldFilter={true}
        >
          <div className="flex items-center border-b border-border px-4">
            <Search className="h-4 w-4 text-muted-foreground flex-shrink-0" />
            <Command.Input
              value={search}
              onValueChange={setSearch}
              placeholder="Search clusters, pages, actions..."
              className="flex-1 h-12 px-3 bg-transparent text-sm text-foreground placeholder:text-muted-foreground
                focus:outline-none"
            />
            <kbd className="hidden sm:inline-flex items-center gap-0.5 px-1.5 py-0.5 rounded border
              border-border bg-muted text-[10px] font-mono text-muted-foreground">
              ESC
            </kbd>
          </div>

          <Command.List className="max-h-80 overflow-y-auto p-2">
            <Command.Empty className="py-8 text-center text-sm text-muted-foreground">
              No results found.
            </Command.Empty>

            {/* Navigation */}
            <Command.Group heading="Pages" className="text-xs text-muted-foreground/60 font-semibold uppercase tracking-wider px-2 py-1.5">
              {pages.map((page) => {
                const Icon = page.icon;
                return (
                  <Command.Item
                    key={page.href}
                    value={page.name}
                    onSelect={() => navigate(page.href)}
                    className="flex items-center gap-3 px-3 py-2.5 rounded-lg text-sm text-muted-foreground
                      cursor-pointer data-[selected=true]:bg-accent data-[selected=true]:text-foreground"
                  >
                    <Icon className="h-4 w-4 flex-shrink-0" />
                    <span className="flex-1">{page.name}</span>
                    <ArrowRight className="h-3.5 w-3.5 opacity-0 data-[selected=true]:opacity-100" />
                  </Command.Item>
                );
              })}
            </Command.Group>

            {/* Clusters */}
            {clustersData?.data && clustersData.data.length > 0 && (
              <Command.Group heading="Clusters" className="text-xs text-muted-foreground/60 font-semibold uppercase tracking-wider px-2 py-1.5 mt-2">
                {clustersData.data.map((cluster) => (
                  <Command.Item
                    key={cluster.id}
                    value={`${cluster.name} ${cluster.displayName} ${cluster.provider} ${cluster.region}`}
                    onSelect={() => selectCluster(cluster)}
                    className="flex items-center gap-3 px-3 py-2.5 rounded-lg text-sm text-muted-foreground
                      cursor-pointer data-[selected=true]:bg-accent data-[selected=true]:text-foreground"
                  >
                    <Server className="h-4 w-4 flex-shrink-0" />
                    <div className="flex-1 min-w-0">
                      <p className="truncate">{cluster.displayName}</p>
                      <p className="text-xs text-muted-foreground truncate">
                        {cluster.provider} / {cluster.region}
                      </p>
                    </div>
                    <span
                      className={`inline-flex h-2 w-2 rounded-full ${
                        cluster.status === 'active'
                          ? 'bg-status-success'
                          : cluster.status === 'warning'
                            ? 'bg-status-warning'
                            : cluster.status === 'error'
                              ? 'bg-status-error'
                              : 'bg-status-neutral'
                      }`}
                    />
                  </Command.Item>
                ))}
              </Command.Group>
            )}

            {/* Quick Actions */}
            <Command.Group heading="Actions" className="text-xs text-muted-foreground/60 font-semibold uppercase tracking-wider px-2 py-1.5 mt-2">
              <Command.Item
                value="Register new cluster"
                onSelect={() => navigate('/dashboard/clusters?register=true')}
                className="flex items-center gap-3 px-3 py-2.5 rounded-lg text-sm text-muted-foreground
                  cursor-pointer data-[selected=true]:bg-accent data-[selected=true]:text-foreground"
              >
                <Server className="h-4 w-4 flex-shrink-0" />
                <span>Register New Cluster</span>
              </Command.Item>
            </Command.Group>
          </Command.List>
        </Command>
      </div>
    </div>
  );
}
