import { createFileRoute } from "@tanstack/react-router";
import { useMemo, useState, type ReactNode } from "react";
import { useNavigate, useLocation } from "@tanstack/react-router";
import { useTabParam } from "@/lib/use-tab-param";
import { useCluster } from "@/lib/hooks/clusters";
import { useProjects } from "@/lib/hooks/projects";
import {
  useHelmRepositories,
  useSyncHelmRepository,
  useDeleteHelmRepository,
  useHelmCharts,
  useInstalledCharts,
  useUninstallChart,
  useRollbackChart,
} from "@/lib/hooks/catalog";
import { ActionButton } from "@/components/ui/action-button";
import { EmptyState } from "@/components/ui/empty-state";
import { PageHeader, PageShell } from "@/components/ui/page";
import { QueryStates } from "@/components/ui/query-states";
import { Select } from "@/components/ui/select";
import { TabStrip, Tabs, TabsContent } from "@/components/ui/tabs";
import type { HelmChart, HelmChartCategory, HelmChartVersion } from "@/types";
import { Package, Plus, SearchX } from "lucide-react";
import { AddRepositoryModal } from "./-add-repository-modal";
import { BrowseTab } from "./-browse-tab";
import { ChartDetailModal } from "./-chart-detail-modal";
import { InstallChartModal } from "./-install-chart-modal";
import { CatalogOperationTimeline } from "@/components/catalog/catalog-operation-timeline";
import { InstalledTab } from "./-installed-tab";
import { RepositoriesTab } from "./-repositories-tab";

type TabKey = "browse" | "installed" | "repositories";

const TAB_KEYS = ["browse", "installed", "repositories"] as const;

function CatalogPage() {
  const [activeTab, setActiveTab] = useTabParam(TAB_KEYS, "browse");
  const [selectedCategory, setSelectedCategory] = useState<
    HelmChartCategory | "all"
  >("all");
  const initialSearchParams = new URLSearchParams(
    useLocation({ select: (location) => location.searchStr }),
  );
  const navigate = useNavigate();
  const projectsQuery = useProjects({ pageSize: 200 });
  const projects = projectsQuery.data?.data ?? [];
  const requestedProjectId = initialSearchParams?.get("project") ?? "";
  const projectId = projects.some(
    (project) => project.id === requestedProjectId,
  )
    ? requestedProjectId
    : projects.length === 1
      ? projects[0].id
      : "";
  const selectedProject = projects.find((project) => project.id === projectId);
  const allowedClusterIds = [
    selectedProject?.clusterId,
    ...(selectedProject?.clusterIds ?? []),
  ].filter((id): id is string => Boolean(id));
  const setProjectId = (nextProjectId: string) => {
    const next = new URLSearchParams(initialSearchParams);
    if (nextProjectId) next.set("project", nextProjectId);
    else next.delete("project");
    void navigate({
      to: `/dashboard/catalog${next.size ? `?${next.toString()}` : ""}`,
      replace: true,
    });
    setSelectedChart(null);
    setShowInstallModal(false);
    setInstallChart(null);
  };
  const [searchQuery, setSearchQuery] = useState(
    initialSearchParams?.get("search") ?? "",
  );
  const presetClusterIdPage = initialSearchParams?.get("cluster_id") ?? "";
  const [selectedChart, setSelectedChart] = useState<HelmChart | null>(null);
  const [showRepoModal, setShowRepoModal] = useState(false);
  const [showInstallModal, setShowInstallModal] = useState(false);
  const [installChart, setInstallChart] =
    useState<{ chart: HelmChart; version: HelmChartVersion } | null>(null);
  const [operationId, setOperationId] = useState<string | null>(null);

  const chartsQuery = useHelmCharts({
    projectId,
    category: selectedCategory !== "all" ? selectedCategory : undefined,
    search: searchQuery || undefined,
  });
  const installedQuery = useInstalledCharts();
  const reposQuery = useHelmRepositories();
  const presetClusterQuery = useCluster(presetClusterIdPage);
  const charts = chartsQuery.data;
  const installed = installedQuery.data;
  const repos = reposQuery.data;
  const repositoryNames = useMemo(
    () => new Map((repos || []).map((repo) => [repo.id, repo.name])),
    [repos],
  );
  const catalogCharts = useMemo(
    () =>
      charts?.map((chart) => ({
        ...chart,
        repositoryName: repositoryNames.get(chart.repositoryId),
      })),
    [charts, repositoryNames],
  );
  const presetCluster = presetClusterQuery.data;

  const syncRepo = useSyncHelmRepository();
  const deleteRepo = useDeleteHelmRepository();
  const uninstall = useUninstallChart();
  const rollback = useRollbackChart();

  const tabs: { key: TabKey; label: ReactNode }[] = [
    { key: "browse", label: "Browse Charts" },
    {
      key: "installed",
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
      key: "repositories",
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

  if (
    projectsQuery.isLoading ||
    projectsQuery.isError ||
    projectsQuery.data === undefined
  ) {
    return (
      <PageShell>
        <PageHeader
          title="Catalog"
          description="Shared Helm repositories and charts."
        />
        <QueryStates
          query={projectsQuery}
          loadingTitle="Loading catalog projects"
          permission="projects:read"
          errorTitle="Failed to load catalog projects"
        >
          {null}
        </QueryStates>
      </PageShell>
    );
  }

  return (
    <PageShell>
      <div>
        <PageHeader
          title="Catalog"
          description="Shared Helm repositories. Browse and install charts from a cluster's Apps page."
          actions={
            <>
              <label className="flex items-center gap-2 text-xs text-muted-foreground">
                Project visibility
                <Select
                  aria-label="Catalog project"
                  value={projectId}
                  onChange={(event) => setProjectId(event.target.value)}
                  containerClassName="min-w-56"
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
              {activeTab === "repositories" && (
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
          <div className="mt-2 inline-flex items-center gap-2 text-xs px-2 py-1 rounded-sm bg-accent/40 text-foreground">
            <Package className="h-3.5 w-3.5" />
            Installing onto{" "}
            <span className="font-medium">
              {presetCluster?.displayName ||
                presetCluster?.name ||
                presetClusterIdPage}
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
          {activeTab === "browse" &&
            (!projectId ? (
              <BrowseTab
                projectId={projectId}
                searchQuery={searchQuery}
                onSearchQueryChange={setSearchQuery}
                selectedCategory={selectedCategory}
                onSelectedCategoryChange={setSelectedCategory}
                charts={catalogCharts}
                chartsLoading={false}
                onSelectChart={setSelectedChart}
              />
            ) : (
              <QueryStates
                query={chartsQuery}
                loadingTitle="Loading charts"
                permission="catalog:read"
                errorTitle="Failed to load charts"
                isEmpty={(rows) => rows.length === 0}
                empty={
                  <EmptyState
                    icon={
                      searchQuery || selectedCategory !== "all"
                        ? SearchX
                        : Package
                    }
                    title={
                      searchQuery || selectedCategory !== "all"
                        ? "No charts match these filters"
                        : "No charts available"
                    }
                    description={
                      searchQuery || selectedCategory !== "all"
                        ? "Clear the search and category filter to browse the complete catalog."
                        : "Add and sync a Helm repository before browsing charts."
                    }
                    actionLabel={
                      searchQuery || selectedCategory !== "all"
                        ? "Clear filters"
                        : "Manage repositories"
                    }
                    onAction={() => {
                      if (searchQuery || selectedCategory !== "all") {
                        setSearchQuery("");
                        setSelectedCategory("all");
                      } else {
                        setActiveTab("repositories");
                      }
                    }}
                  />
                }
              >
                <BrowseTab
                  projectId={projectId}
                  searchQuery={searchQuery}
                  onSearchQueryChange={setSearchQuery}
                  selectedCategory={selectedCategory}
                  onSelectedCategoryChange={setSelectedCategory}
                  charts={catalogCharts}
                  chartsLoading={false}
                  onSelectChart={setSelectedChart}
                />
              </QueryStates>
            ))}

          {activeTab === "installed" && (
            <QueryStates
              query={installedQuery}
              loadingTitle="Loading installed charts"
              permission="catalog:read"
              errorTitle="Failed to load installed charts"
              isEmpty={(rows) => rows.length === 0}
              empty={
                <EmptyState
                  icon={Package}
                  title="No charts installed"
                  description="Browse the catalog to install a chart on an adopted cluster."
                  actionLabel="Browse charts"
                  onAction={() => setActiveTab("browse")}
                />
              }
            >
              <InstalledTab
                installed={installed}
                loading={false}
                onRollback={(id, revision) => rollback.mutate({ id, revision })}
                onUninstall={(id) => uninstall.mutateAsync(id)}
                uninstallPending={uninstall.isPending}
              />
            </QueryStates>
          )}

          {activeTab === "repositories" && (
            <QueryStates
              query={reposQuery}
              loadingTitle="Loading repositories"
              permission="catalog:read"
              errorTitle="Failed to load repositories"
              isEmpty={(rows) => rows.length === 0}
              empty={
                <EmptyState
                  icon={Package}
                  title="No repositories configured"
                  description="Add a Helm repository, then sync it to make charts available to projects."
                  actionLabel="Add repository"
                  actionIcon={Plus}
                  onAction={() => setShowRepoModal(true)}
                />
              }
            >
              <RepositoriesTab
                repos={repos}
                loading={false}
                onSync={(id) => syncRepo.mutate(id)}
                onDelete={(id) => deleteRepo.mutateAsync(id)}
                syncPending={syncRepo.isPending}
                deletePending={deleteRepo.isPending}
              />
            </QueryStates>
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
          onOperationStarted={setOperationId}
          onClose={() => {
            setShowInstallModal(false);
            setInstallChart(null);
          }}
        />
      )}

      {operationId && (
        <div className="fixed bottom-4 right-4 z-40 w-full max-w-xl shadow-lg">
          <CatalogOperationTimeline operationId={operationId} />
        </div>
      )}

      {showRepoModal && (
        <AddRepositoryModal onClose={() => setShowRepoModal(false)} />
      )}
    </PageShell>
  );
}

export const Route = createFileRoute("/dashboard/catalog/")({
  // Deep-link contract (P2.4): typed passthrough — unrelated params survive.
  validateSearch: (search: Record<string, unknown>) =>
    search as { tab?: string; search?: string; cluster_id?: string } & Record<
      string,
      unknown
    >,
  component: CatalogPage,
});
