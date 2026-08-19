import { createFileRoute } from '@tanstack/react-router';
import { useMemo, useState, type ReactNode } from 'react';
import { useRouter, useSearchParams } from '@/lib/navigation';
import { useTabParam } from '@/lib/use-tab-param';
import {
  useHelmRepositories,
  useSyncHelmRepository,
  useDeleteHelmRepository,
  useHelmCharts,
  useInstalledCharts,
  useUninstallChart,
  useRollbackChart,
  useClusters,
  useProjects,
} from '@/lib/hooks';
import { ActionButton } from '@/components/ui/action-button';
import { PageHeader, PageShell } from '@/components/ui/page';
import { Select } from '@/components/ui/select';
import { TabStrip, Tabs, TabsContent } from '@/components/ui/tabs';
import type { HelmChart, HelmChartCategory, HelmChartVersion } from '@/types';
import { Package, Plus } from 'lucide-react';
import { AddRepositoryModal } from './-add-repository-modal';
import { BrowseTab } from './-browse-tab';
import { ChartDetailModal } from './-chart-detail-modal';
import { InstallChartModal } from './-install-chart-modal';
import { InstalledTab } from './-installed-tab';
import { RepositoriesTab } from './-repositories-tab';

type TabKey = 'browse' | 'installed' | 'repositories';

const TAB_KEYS = ['browse', 'installed', 'repositories'] as const;

function CatalogPage() {
  const [activeTab, setActiveTab] = useTabParam(TAB_KEYS, 'browse');
  const [selectedCategory, setSelectedCategory] = useState<HelmChartCategory | 'all'>('all');
  const initialSearchParams = useSearchParams();
  const router = useRouter();
  const projectsQuery = useProjects({ pageSize: 200 });
  const projects = projectsQuery.data?.data ?? [];
  const requestedProjectId = initialSearchParams?.get('project') ?? '';
  const projectId = projects.some((project) => project.id === requestedProjectId)
    ? requestedProjectId
    : projects.length === 1
      ? projects[0].id
      : '';
  const selectedProject = projects.find((project) => project.id === projectId);
  const allowedClusterIds = [
    selectedProject?.clusterId,
    ...(selectedProject?.clusterIds ?? []),
  ].filter((id): id is string => Boolean(id));
  const setProjectId = (nextProjectId: string) => {
    const next = new URLSearchParams(initialSearchParams);
    if (nextProjectId) next.set('project', nextProjectId);
    else next.delete('project');
    router.replace(`/dashboard/catalog${next.size ? `?${next.toString()}` : ''}`);
    setSelectedChart(null);
    setShowInstallModal(false);
    setInstallChart(null);
  };
  const [searchQuery, setSearchQuery] = useState(initialSearchParams?.get('search') ?? '');
  const presetClusterIdPage = initialSearchParams?.get('cluster_id') ?? '';
  const [selectedChart, setSelectedChart] = useState<HelmChart | null>(null);
  const [showRepoModal, setShowRepoModal] = useState(false);
  const [showInstallModal, setShowInstallModal] = useState(false);
  const [installChart, setInstallChart] = useState<{ chart: HelmChart; version: HelmChartVersion } | null>(null);

  const { data: charts, isLoading: chartsLoading } = useHelmCharts({
    projectId,
    category: selectedCategory !== 'all' ? selectedCategory : undefined,
    search: searchQuery || undefined,
  });
  const { data: installed, isLoading: installedLoading } = useInstalledCharts();
  const { data: repos, isLoading: reposLoading } = useHelmRepositories();
  const { data: presetClusterData } = useClusters({ pageSize: 100 });
  const presetCluster = useMemo(
    () => (presetClusterIdPage ? (presetClusterData?.data || []).find((c) => c.id === presetClusterIdPage) : undefined),
    [presetClusterIdPage, presetClusterData],
  );

  const syncRepo = useSyncHelmRepository();
  const deleteRepo = useDeleteHelmRepository();
  const uninstall = useUninstallChart();
  const rollback = useRollbackChart();

  const tabs: { key: TabKey; label: ReactNode }[] = [
    { key: 'browse', label: 'Browse Charts' },
    {
      key: 'installed',
      label: (
        <>
          Installed
          {installed && (
            <span className="text-xs px-1.5 py-0.5 rounded-full bg-muted text-muted-foreground tabular-nums">
              {installed.length}
            </span>
          )}
        </>
      ),
    },
    {
      key: 'repositories',
      label: (
        <>
          Repositories
          {repos && (
            <span className="text-xs px-1.5 py-0.5 rounded-full bg-muted text-muted-foreground tabular-nums">
              {repos.length}
            </span>
          )}
        </>
      ),
    },
  ];

  return (
    <PageShell>
      <div>
        <PageHeader
          title="Catalog"
          description="Browse, install, and manage Helm charts across your clusters"
          actions={
            <>
              <label className="flex items-center gap-2 text-xs text-muted-foreground">
                Project visibility
                <Select
                  aria-label="Catalog project"
                  value={projectId}
                  onChange={(event) => setProjectId(event.target.value)}
                  className="min-w-56"
                  disabled={projectsQuery.isLoading}
                >
                  <option value="">Select a project</option>
                  {projects.map((project) => (
                    <option key={project.id} value={project.id}>
                      {project.displayName || project.name}
                    </option>
                  ))}
                </Select>
              </label>
              {activeTab === 'repositories' && (
                <ActionButton
                  intent="primary"
                  icon={<Plus className="h-4 w-4" />}
                  onClick={() => setShowRepoModal(true)}
                >
                  Add Repository
                </ActionButton>
              )}
            </>
          }
        />
        {presetClusterIdPage && (
          <div className="mt-2 inline-flex items-center gap-2 text-xs px-2 py-1 rounded bg-accent/40 text-foreground">
            <Package className="h-3.5 w-3.5" />
            Installing onto{' '}
            <span className="font-medium">
              {presetCluster?.displayName || presetCluster?.name || presetClusterIdPage}
            </span>
          </div>
        )}
      </div>

      <Tabs>
        <TabStrip
          tabs={tabs}
          value={activeTab}
          onChange={setActiveTab}
          className="overflow-x-auto"
        />

        <TabsContent>
          {activeTab === 'browse' && (
            <BrowseTab
              projectId={projectId}
              searchQuery={searchQuery}
              onSearchQueryChange={setSearchQuery}
              selectedCategory={selectedCategory}
              onSelectedCategoryChange={setSelectedCategory}
              charts={charts}
              chartsLoading={chartsLoading}
              onSelectChart={setSelectedChart}
            />
          )}

          {activeTab === 'installed' && (
            <InstalledTab
              installed={installed}
              loading={installedLoading}
              onRollback={(id, revision) => rollback.mutate({ id, revision })}
              onUninstall={(id) => uninstall.mutate(id)}
            />
          )}

          {activeTab === 'repositories' && (
            <RepositoriesTab
              repos={repos}
              loading={reposLoading}
              onSync={(id) => syncRepo.mutate(id)}
              onDelete={(id) => deleteRepo.mutate(id)}
              syncPending={syncRepo.isPending}
            />
          )}
        </TabsContent>
      </Tabs>

      {selectedChart && (
        <ChartDetailModal
          projectId={projectId}
          chart={selectedChart}
          onClose={() => setSelectedChart(null)}
          onInstall={(chart, version) => {
            setInstallChart({ chart, version });
            setShowInstallModal(true);
            setSelectedChart(null);
          }}
        />
      )}

      {showInstallModal && installChart && (
        <InstallChartModal
          projectId={projectId}
          allowedClusterIds={allowedClusterIds}
          chart={installChart.chart}
          version={installChart.version}
          onClose={() => {
            setShowInstallModal(false);
            setInstallChart(null);
          }}
        />
      )}

      {showRepoModal && (
        <AddRepositoryModal onClose={() => setShowRepoModal(false)} />
      )}
    </PageShell>
  );
}

export const Route = createFileRoute('/dashboard/catalog/')({
  // Deep-link contract (P2.4): typed passthrough — unrelated params survive.
  validateSearch: (search: Record<string, unknown>) =>
    search as { tab?: string; search?: string; cluster_id?: string } & Record<string, unknown>,
  component: CatalogPage,
});
