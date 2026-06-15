'use client';

import { useCallback, useEffect, useState } from 'react';
import type { ReactNode } from 'react';
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
  Folder,
  Rocket,
  BookOpen,
  Box,
} from 'lucide-react';
import type { LucideIcon } from 'lucide-react';
import { useQuery } from '@tanstack/react-query';
import { useUIStore } from '@/lib/store';
import { queryKeys, useClusters, useProjects } from '@/lib/hooks';
import { listArgoCachedApplications, type SearchableResourceType } from '@/lib/api';
import { OverlayShell } from '@/components/ui/overlay-shell';
import type { ArgoCachedApplication } from '@/lib/api';
import type { Cluster, Project } from '@/types';

const pages = [
  { name: 'Dashboard', href: '/dashboard', icon: LayoutDashboard },
  { name: 'Clusters', href: '/dashboard/clusters', icon: Server },
  { name: 'Projects', href: '/dashboard/projects', icon: Folder },
  { name: 'Monitoring', href: '/dashboard/monitoring', icon: BarChart3 },
  { name: 'ArgoCD', href: '/dashboard/argocd', icon: GitBranch },
  { name: 'Catalog', href: '/dashboard/catalog', icon: Box },
  { name: 'RBAC', href: '/dashboard/rbac', icon: Shield },
  { name: 'Settings', href: '/dashboard/settings', icon: Settings },
];

const resourceSearches: Array<{ name: string; type: SearchableResourceType; description: string }> = [
  { name: 'Search pods', type: 'pods', description: 'Across connected clusters' },
  { name: 'Search workloads', type: 'deployments', description: 'Deployments by name' },
  { name: 'Search namespaces', type: 'namespaces', description: 'Namespace inventory' },
  { name: 'Search services', type: 'services', description: 'Service endpoints' },
  { name: 'Search ingresses', type: 'ingresses', description: 'Ingress routing' },
  { name: 'Search nodes', type: 'nodes', description: 'Node inventory' },
];

const runbookLinks = [
  {
    name: 'Argo recovery',
    href: '/dashboard/argocd',
    description: 'Health, orphan checks, sync recovery',
  },
  {
    name: 'Backup recovery',
    href: '/dashboard/backups',
    description: 'Snapshots, restores, restore drills',
  },
  {
    name: 'Operations queues',
    href: '/dashboard/settings/operations',
    description: 'Task outbox, queues, dead letters',
  },
  {
    name: 'Audit investigation',
    href: '/dashboard/audit',
    description: 'Who, what, where, and request IDs',
  },
];

function paletteItemClassName() {
  return 'flex items-center gap-3 px-3 py-2.5 rounded-lg text-sm text-muted-foreground cursor-pointer data-[selected=true]:bg-accent data-[selected=true]:text-foreground';
}

function CommandRow({
  value,
  icon: Icon,
  title,
  description,
  right,
  onSelect,
}: {
  value: string;
  icon: LucideIcon;
  title: string;
  description?: string;
  right?: ReactNode;
  onSelect: () => void;
}) {
  return (
    <Command.Item
      value={value}
      onSelect={onSelect}
      className={paletteItemClassName()}
    >
      <Icon className="h-4 w-4 flex-shrink-0" />
      <div className="min-w-0 flex-1">
        <p className="truncate">{title}</p>
        {description ? (
          <p className="truncate text-xs text-muted-foreground">{description}</p>
        ) : null}
      </div>
      {right ?? <ArrowRight className="h-3.5 w-3.5 opacity-0 data-[selected=true]:opacity-100" />}
    </Command.Item>
  );
}

export function CommandPalette() {
  const router = useRouter();
  const { commandPaletteOpen, setCommandPaletteOpen } = useUIStore();
  const { data: clustersData } = useClusters({ pageSize: 50 });
  const { data: projectsData } = useProjects({ pageSize: 25 });
  const { data: argoApps = [] } = useQuery({
    queryKey: queryKeys.argocd.cachedApplications({ limit: 25 }),
    queryFn: () => listArgoCachedApplications({ limit: 25 }),
    enabled: commandPaletteOpen,
    staleTime: 30_000,
  });
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
    (cluster: Cluster) => {
      // The cluster context is encoded in the URL slug, not in any global
      // store — navigating is sufficient.
      router.push(`/dashboard/clusters/${cluster.id}`);
      setCommandPaletteOpen(false);
      setSearch('');
    },
    [router, setCommandPaletteOpen]
  );

  const selectProject = useCallback(
    (project: Project) => {
      router.push(`/dashboard/projects/${project.id}`);
      setCommandPaletteOpen(false);
      setSearch('');
    },
    [router, setCommandPaletteOpen],
  );

  const selectArgoApp = useCallback(
    (app: ArgoCachedApplication) => {
      router.push(`/dashboard/argocd/${app.argocdInstanceId}/applications/${app.id}`);
      setCommandPaletteOpen(false);
      setSearch('');
    },
    [router, setCommandPaletteOpen],
  );

  const resourceSearchHref = useCallback(
    (type: SearchableResourceType) => {
      const params = new URLSearchParams({ type });
      const q = search.trim();
      if (q) params.set('name', q);
      return `/dashboard/search?${params.toString()}`;
    },
    [search],
  );

  if (!commandPaletteOpen) return null;

  return (
    <OverlayShell onClose={() => setCommandPaletteOpen(false)}>
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
                return (
                  <CommandRow
                    key={page.href}
                    value={page.name}
                    icon={page.icon}
                    title={page.name}
                    onSelect={() => navigate(page.href)}
                  />
                );
              })}
            </Command.Group>

            <Command.Group heading="Resource Search" className="text-xs text-muted-foreground/60 font-semibold uppercase tracking-wider px-2 py-1.5 mt-2">
              {resourceSearches.map((item) => (
                <CommandRow
                  key={item.type}
                  value={`${item.name} ${item.type} kubernetes resources ${search}`}
                  icon={Search}
                  title={item.name}
                  description={item.description}
                  onSelect={() => navigate(resourceSearchHref(item.type))}
                />
              ))}
            </Command.Group>

            {/* Clusters */}
            {clustersData?.data && clustersData.data.length > 0 && (
              <Command.Group heading="Clusters" className="text-xs text-muted-foreground/60 font-semibold uppercase tracking-wider px-2 py-1.5 mt-2">
                {clustersData.data.map((cluster) => (
                  <CommandRow
                    key={cluster.id}
                    value={`${cluster.name} ${cluster.displayName} ${cluster.provider} ${cluster.region}`}
                    icon={Server}
                    title={cluster.displayName}
                    description={`${cluster.provider} / ${cluster.region}`}
                    right={
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
                    }
                    onSelect={() => selectCluster(cluster)}
                  />
                ))}
              </Command.Group>
            )}

            {projectsData?.data && projectsData.data.length > 0 && (
              <Command.Group heading="Projects" className="text-xs text-muted-foreground/60 font-semibold uppercase tracking-wider px-2 py-1.5 mt-2">
                {projectsData.data.map((project) => (
                  <CommandRow
                    key={project.id}
                    value={`${project.name} ${project.displayName} project namespaces ${project.namespaces?.join(' ') ?? ''}`}
                    icon={Folder}
                    title={project.displayName || project.name}
                    description={`${project.namespaces?.length ?? 0} namespaces`}
                    onSelect={() => selectProject(project)}
                  />
                ))}
              </Command.Group>
            )}

            {argoApps.length > 0 && (
              <Command.Group heading="GitOps Apps" className="text-xs text-muted-foreground/60 font-semibold uppercase tracking-wider px-2 py-1.5 mt-2">
                {argoApps.map((app) => (
                  <CommandRow
                    key={app.id}
                    value={`${app.name} ${app.project} ${app.destinationNamespace} ${app.syncStatus} ${app.healthStatus} argocd gitops app application`}
                    icon={Rocket}
                    title={app.name}
                    description={`${app.project || 'default'} / ${app.destinationNamespace || 'cluster'}`}
                    right={
                      <span className="shrink-0 rounded border border-border px-1.5 py-0.5 text-[11px] text-muted-foreground">
                        {app.syncStatus || 'unknown'}
                      </span>
                    }
                    onSelect={() => selectArgoApp(app)}
                  />
                ))}
              </Command.Group>
            )}

            <Command.Group heading="Runbooks" className="text-xs text-muted-foreground/60 font-semibold uppercase tracking-wider px-2 py-1.5 mt-2">
              {runbookLinks.map((item) => (
                <CommandRow
                  key={item.href}
                  value={`${item.name} ${item.description} runbook docs operations recovery`}
                  icon={BookOpen}
                  title={item.name}
                  description={item.description}
                  onSelect={() => navigate(item.href)}
                />
              ))}
            </Command.Group>

            {/* Quick Actions */}
            <Command.Group heading="Actions" className="text-xs text-muted-foreground/60 font-semibold uppercase tracking-wider px-2 py-1.5 mt-2">
              <CommandRow
                value="Register new cluster"
                icon={Server}
                title="Register New Cluster"
                onSelect={() => navigate('/dashboard/clusters/register')}
              />
            </Command.Group>
          </Command.List>
        </Command>
      </div>
    </OverlayShell>
  );
}
