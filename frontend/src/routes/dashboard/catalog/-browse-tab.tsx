import { EmptyState } from "@/components/ui/empty-state";
import { CatalogIcon } from "@/components/catalog/catalog-icon";
import { CatalogSourceBadge } from "@/components/catalog/catalog-source-badge";
import { DataTable, type Column } from "@/components/ui/data-table";
import { Input } from "@/components/ui/input";
import { cn } from "@/lib/utils";
import type { CuratedHelmChart } from "@/lib/catalogs/astronomer";
import {
  catalogSourceSortWeight,
  type CatalogSourceFamily,
} from "@/lib/catalogs/source";
import type { HelmChartCategory } from "@/types";
import {
  Box,
  LayoutGrid,
  List,
  Package,
  Search,
  ShieldCheck,
  Star,
  X,
} from "lucide-react";
import { useMemo, useState } from "react";
import { categories, CategoryChip } from "./-category";

type CatalogView = "cards" | "table";
type SupportFilter = "all" | "Astronomer" | "Upstream" | "Experimental";
type AccessFilter = "all" | "standard" | "privileged";
type SourceFilter = "all" | CatalogSourceFamily;

export function BrowseTab({
  projectId,
  searchQuery,
  onSearchQueryChange,
  selectedCategory,
  onSelectedCategoryChange,
  charts,
  chartsLoading,
  onSelectChart,
}: {
  projectId: string;
  searchQuery: string;
  onSearchQueryChange: (value: string) => void;
  selectedCategory: HelmChartCategory | "all";
  onSelectedCategoryChange: (value: HelmChartCategory | "all") => void;
  charts: CuratedHelmChart[] | undefined;
  chartsLoading: boolean;
  onSelectChart: (chart: CuratedHelmChart) => void;
}) {
  const [view, setView] = useState<CatalogView>("cards");
  const [support, setSupport] = useState<SupportFilter>("all");
  const [access, setAccess] = useState<AccessFilter>("all");
  const [storageOnly, setStorageOnly] = useState(false);
  const [source, setSource] = useState<SourceFilter>("all");
  const [repository, setRepository] = useState("all");

  const sourceCounts = useMemo(() => {
    const counts = { all: 0, "first-party": 0, curated: 0, community: 0, custom: 0 };
    for (const chart of charts || []) {
      counts.all += 1;
      counts[chart.catalogSource.family] += 1;
    }
    return counts;
  }, [charts]);

  const repositoryOptions = useMemo(
    () =>
      Array.from(
        new Map(
          (charts || []).map((chart) => [
            chart.catalogSource.repositoryId,
            chart.catalogSource,
          ]),
        ).values(),
      ).sort(
        (left, right) =>
          catalogSourceSortWeight(left.family) -
            catalogSourceSortWeight(right.family) ||
          left.repositoryName.localeCompare(right.repositoryName),
      ),
    [charts],
  );

  const filteredCharts = useMemo(
    () =>
      (charts || []).filter((chart) => {
        const presentation = chart.catalogPresentation;
        if (source !== "all" && chart.catalogSource.family !== source) {
          return false;
        }
        if (
          repository !== "all" &&
          chart.catalogSource.repositoryId !== repository
        ) {
          return false;
        }
        if (support !== "all" && presentation?.supportTier !== support) {
          return false;
        }
        if (
          access !== "all" &&
          (presentation?.privileged ? "privileged" : "standard") !== access
        ) {
          return false;
        }
        if (storageOnly && (!presentation || presentation.storage === "None")) {
          return false;
        }
        return true;
      }),
    [access, charts, repository, source, storageOnly, support],
  );

  const featured = Array.from(
    new Map(
      filteredCharts
        .filter((chart) => chart.catalogPresentation?.featured)
        .map((chart) => [chart.name, chart]),
    ).values(),
  );
  const remaining = filteredCharts.filter(
    (chart) => !chart.catalogPresentation?.featured,
  );

  const tableCharts = [...featured, ...remaining].sort(
    (left, right) =>
      catalogSourceSortWeight(left.catalogSource.family) -
        catalogSourceSortWeight(right.catalogSource.family) ||
      Number(Boolean(right.catalogPresentation?.featured)) -
        Number(Boolean(left.catalogPresentation?.featured)) ||
      (left.displayName || left.name).localeCompare(
        right.displayName || right.name,
      ),
  );
  const columns: Column<CuratedHelmChart>[] = [
    {
      key: "name",
      header: "Application",
      accessor: (chart) => (
        <div className="flex min-w-0 items-center gap-3">
          <CatalogIcon
            src={chart.iconUrl}
            label={chart.displayName || chart.name}
            className="h-9 w-9 rounded-md"
            imageClassName="h-7 w-7"
          />
          <div className="min-w-0">
            <p className="truncate font-medium text-foreground">
              {chart.displayName || chart.name}
            </p>
            <p className="truncate text-xs text-table-secondary">
              {chart.description || "No description available"}
            </p>
          </div>
        </div>
      ),
      sortAccessor: (chart) => chart.displayName || chart.name,
    },
    {
      key: "source",
      header: "Source",
      accessor: (chart) => (
        <CatalogSourceBadge source={chart.catalogSource} compact />
      ),
      sortAccessor: (chart) =>
        `${catalogSourceSortWeight(chart.catalogSource.family)}-${chart.catalogSource.repositoryName}`,
      filter: { label: "Sources" },
    },
    {
      key: "category",
      header: "Category",
      accessor: (chart) => <CategoryChip category={chart.category} />,
      sortAccessor: (chart) => chart.category,
      filter: { label: "Categories" },
    },
    {
      key: "support",
      header: "Support",
      accessor: (chart) => (
        <span className="inline-flex items-center gap-1 text-xs font-medium text-foreground">
          <ShieldCheck className="h-3.5 w-3.5 text-primary" />
          {chart.catalogPresentation?.supportTier || "Repository"}
        </span>
      ),
      sortAccessor: (chart) =>
        chart.catalogPresentation?.supportTier || "Repository",
      filter: { label: "Support" },
    },
    {
      key: "resources",
      header: "Resources",
      accessor: (chart) =>
        chart.catalogPresentation?.resourceProfile || "Not specified",
      sortAccessor: (chart) =>
        chart.catalogPresentation?.resourceProfile || "Not specified",
      filter: { label: "Profiles" },
    },
    {
      key: "access",
      header: "Cluster access",
      accessor: (chart) =>
        chart.catalogPresentation?.privileged ? "Required" : "Standard",
      sortAccessor: (chart) =>
        chart.catalogPresentation?.privileged ? "Required" : "Standard",
      filter: { label: "Access" },
    },
    {
      key: "version",
      header: "Latest",
      accessor: (chart) => chart.latestVersion || "—",
      sortAccessor: (chart) => chart.latestVersion || "",
      code: true,
    },
  ];

  const renderChart = (chart: CuratedHelmChart) => (
    <button
      key={chart.id}
      onClick={() => onSelectChart(chart)}
      className="rounded-lg border border-border p-4 text-left hover:border-primary/40 hover:bg-muted/30 transition-colors group"
    >
      <div className="flex items-start gap-3">
        <CatalogIcon
          src={chart.iconUrl}
          label={chart.displayName || chart.name}
          className="h-11 w-11"
          imageClassName="h-8 w-8"
        />
        <div className="flex-1 min-w-0">
          <p className="font-medium text-foreground text-sm truncate group-hover:text-primary transition-colors">
            {chart.displayName || chart.name}
          </p>
          <p className="text-xs text-muted-foreground truncate">
            {chart.repositoryName ||
              `Repository ${chart.repositoryId.slice(0, 8)}`}
          </p>
        </div>
      </div>
      <p className="text-xs text-table-secondary mt-3 line-clamp-2 min-h-[2rem]">
        {chart.description || "No description available"}
      </p>
      {chart.catalogPresentation && (
        <div className="mt-3 flex flex-wrap gap-1.5">
          <span className="inline-flex items-center gap-1 rounded bg-primary/10 px-1.5 py-0.5 text-2xs font-medium text-primary">
            <ShieldCheck className="h-3 w-3" />
            {chart.catalogPresentation.supportTier}
          </span>
          <span className="inline-flex items-center gap-1 rounded bg-muted px-1.5 py-0.5 text-2xs text-table-secondary">
            <Box className="h-3 w-3" />
            {chart.catalogPresentation.resourceProfile}
          </span>
          {chart.catalogPresentation.privileged && (
            <span className="rounded bg-status-warning/10 px-1.5 py-0.5 text-2xs text-status-warning">
              Cluster access
            </span>
          )}
        </div>
      )}
      <div className="mt-3">
        <CatalogSourceBadge source={chart.catalogSource} />
      </div>
      <div className="flex items-center justify-between mt-3">
        <CategoryChip
          category={chart.category}
          className="text-2xs px-1.5 py-0.5"
        />
        {chart.latestVersion && (
          <span className="text-xs font-mono text-table-secondary">
            v{chart.latestVersion}
          </span>
        )}
      </div>
    </button>
  );

  return (
    <div className="space-y-4">
      <div className="flex flex-wrap items-center gap-3">
        <div className="relative max-w-sm flex-1">
          <Search className="absolute left-3 top-1/2 -translate-y-1/2 h-4 w-4 text-muted-foreground" />
          <Input
            type="text"
            placeholder="Search charts..."
            value={searchQuery}
            onChange={(e) => onSearchQueryChange(e.target.value)}
            className="pl-9 pr-8"
          />
          {searchQuery && (
            <button
              onClick={() => onSearchQueryChange("")}
              className="absolute right-2.5 top-1/2 -translate-y-1/2 text-muted-foreground hover:text-foreground"
            >
              <X className="h-3.5 w-3.5" />
            </button>
          )}
        </div>
        <div className="inline-flex flex-wrap rounded-md border border-input bg-background p-0.5">
          {(
            ["first-party", "curated", "community", "custom", "all"] as SourceFilter[]
          ).map((value) => (
            <button
              key={value}
              type="button"
              aria-pressed={source === value}
              onClick={() => {
                setSource(value);
                setRepository("all");
              }}
              className={cn(
                "rounded px-2.5 py-1.5 text-xs font-medium transition-colors",
                source === value
                  ? "bg-muted text-foreground"
                  : "text-muted-foreground hover:text-foreground",
              )}
            >
              {value === "first-party"
                ? "First Party"
                : value === "curated"
                ? "Curated"
                : value === "community"
                  ? "Community"
                  : value === "custom"
                    ? "Custom"
                    : "All"}{" "}
              <span className="tabular-nums">({sourceCounts[value]})</span>
            </button>
          ))}
        </div>
        <select
          aria-label="Filter by catalog source"
          value={repository}
          onChange={(event) => setRepository(event.target.value)}
          className="h-9 max-w-56 rounded-md border border-input bg-background px-3 text-xs text-foreground"
        >
          <option value="all">All catalog sources</option>
          {(["first-party", "curated", "community", "custom"] as const).map((family) => {
            const options = repositoryOptions.filter(
              (item) => item.family === family,
            );
            return options.length > 0 ? (
              <optgroup key={family} label={options[0].familyLabel}>
                {options.map((item) => (
                  <option
                    key={`${family}-${item.repositoryId}`}
                    value={item.repositoryId}
                  >
                    {item.repositoryName}
                  </option>
                ))}
              </optgroup>
            ) : null;
          })}
        </select>
        <select
          aria-label="Filter by support tier"
          value={support}
          onChange={(event) => setSupport(event.target.value as SupportFilter)}
          className="h-9 rounded-md border border-input bg-background px-3 text-xs text-foreground"
        >
          <option value="all">All support tiers</option>
          <option value="Astronomer">Astronomer</option>
          <option value="Upstream">Upstream</option>
          <option value="Experimental">Experimental</option>
        </select>
        <select
          aria-label="Filter by cluster access"
          value={access}
          onChange={(event) => setAccess(event.target.value as AccessFilter)}
          className="h-9 rounded-md border border-input bg-background px-3 text-xs text-foreground"
        >
          <option value="all">All access levels</option>
          <option value="standard">Standard access</option>
          <option value="privileged">Cluster access</option>
        </select>
        <button
          type="button"
          onClick={() => setStorageOnly((current) => !current)}
          className={cn(
            "h-9 rounded-md border px-3 text-xs font-medium transition-colors",
            storageOnly
              ? "border-primary bg-primary/10 text-primary"
              : "border-input bg-background text-muted-foreground hover:text-foreground",
          )}
        >
          Storage required
        </button>
        <div className="ml-auto inline-flex rounded-md border border-input bg-background p-0.5">
          <button
            type="button"
            aria-label="Card view"
            aria-pressed={view === "cards"}
            onClick={() => setView("cards")}
            className={cn(
              "rounded p-1.5 transition-colors",
              view === "cards"
                ? "bg-muted text-foreground"
                : "text-muted-foreground hover:text-foreground",
            )}
          >
            <LayoutGrid className="h-4 w-4" />
          </button>
          <button
            type="button"
            aria-label="Table view"
            aria-pressed={view === "table"}
            onClick={() => setView("table")}
            className={cn(
              "rounded p-1.5 transition-colors",
              view === "table"
                ? "bg-muted text-foreground"
                : "text-muted-foreground hover:text-foreground",
            )}
          >
            <List className="h-4 w-4" />
          </button>
        </div>
      </div>

      <div className="flex flex-wrap gap-1.5">
        {categories.map((cat) => (
          <button
            key={cat.key}
            onClick={() => onSelectedCategoryChange(cat.key)}
            className={cn(
              "px-3 py-1.5 rounded-md text-xs font-medium transition-colors",
              selectedCategory === cat.key
                ? "bg-primary text-primary-foreground"
                : "bg-muted text-muted-foreground hover:text-foreground",
            )}
          >
            {cat.label}
          </button>
        ))}
      </div>

      {!projectId ? (
        <EmptyState
          icon={Package}
          title="Select a project"
          description="Chart visibility and install authorization are isolated by project."
        />
      ) : chartsLoading ? (
        <div className="grid grid-cols-1 sm:grid-cols-2 lg:grid-cols-3 xl:grid-cols-4 gap-4">
          {Array.from({ length: 8 }).map((_, i) => (
            <div
              key={i}
              className="rounded-lg border border-border p-4 space-y-3"
            >
              <div className="flex items-center gap-3">
                <div className="h-10 w-10 rounded-lg bg-muted animate-pulse" />
                <div className="flex-1 space-y-1.5">
                  <div className="h-4 w-24 rounded bg-muted animate-pulse" />
                  <div className="h-3 w-16 rounded bg-muted animate-pulse" />
                </div>
              </div>
              <div className="h-3 w-full rounded bg-muted animate-pulse" />
              <div className="h-3 w-3/4 rounded bg-muted animate-pulse" />
            </div>
          ))}
        </div>
      ) : filteredCharts.length === 0 ? (
        <EmptyState
          icon={Package}
          title="No charts found"
          description="Try adjusting your search or category filter."
        />
      ) : view === "table" ? (
        <DataTable
          data={tableCharts}
          columns={columns}
          keyExtractor={(chart) => chart.id}
          onRowClick={onSelectChart}
          searchable={false}
          pageSize={25}
          persistKey="catalog-applications"
          resizable
          emptyMessage="No applications match these filters"
        />
      ) : (
        <div className="space-y-7">
          {featured.length > 0 && (
            <section className="space-y-3">
              <div className="flex items-center gap-2">
                <Star className="h-4 w-4 text-primary" />
                <h2 className="text-sm font-semibold text-foreground">
                  Curated applications
                </h2>
                <span className="text-xs text-table-secondary">
                  Reviewed metadata and guided configuration
                </span>
              </div>
              <div className="grid grid-cols-1 sm:grid-cols-2 lg:grid-cols-3 xl:grid-cols-4 gap-4">
                {featured.map(renderChart)}
              </div>
            </section>
          )}
          {remaining.length > 0 && (
            <section className="space-y-3">
              <h2 className="text-sm font-semibold text-foreground">
                All repository applications
              </h2>
              <div className="grid grid-cols-1 sm:grid-cols-2 lg:grid-cols-3 xl:grid-cols-4 gap-4">
                {remaining.map(renderChart)}
              </div>
            </section>
          )}
        </div>
      )}
    </div>
  );
}
