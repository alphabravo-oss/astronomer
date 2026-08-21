import { EmptyState } from '@/components/ui/empty-state';
import { Input } from '@/components/ui/input';
import { cn } from '@/lib/utils';
import type { HelmChart, HelmChartCategory } from '@/types';
import { Package, Search, X } from 'lucide-react';
import { categories, CategoryChip } from './-category';

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
  selectedCategory: HelmChartCategory | 'all';
  onSelectedCategoryChange: (value: HelmChartCategory | 'all') => void;
  charts: HelmChart[] | undefined;
  chartsLoading: boolean;
  onSelectChart: (chart: HelmChart) => void;
}) {
  return (
    <div className="space-y-4">
      <div className="flex items-center gap-3">
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
              onClick={() => onSearchQueryChange('')}
              className="absolute right-2.5 top-1/2 -translate-y-1/2 text-muted-foreground hover:text-foreground"
            >
              <X className="h-3.5 w-3.5" />
            </button>
          )}
        </div>
      </div>

      <div className="flex flex-wrap gap-1.5">
        {categories.map((cat) => (
          <button
            key={cat.key}
            onClick={() => onSelectedCategoryChange(cat.key)}
            className={cn(
              'px-3 py-1.5 rounded-md text-xs font-medium transition-colors',
              selectedCategory === cat.key
                ? 'bg-primary text-primary-foreground'
                : 'bg-muted text-muted-foreground hover:text-foreground',
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
            <div key={i} className="rounded-lg border border-border p-4 space-y-3">
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
      ) : (charts || []).length === 0 ? (
        <EmptyState
          icon={Package}
          title="No charts found"
          description="Try adjusting your search or category filter."
        />
      ) : (
        <div className="grid grid-cols-1 sm:grid-cols-2 lg:grid-cols-3 xl:grid-cols-4 gap-4">
          {(charts || []).map((chart) => (
            <button
              key={chart.id}
              onClick={() => onSelectChart(chart)}
              className="rounded-lg border border-border p-4 text-left hover:border-foreground/20 hover:bg-muted/30
                transition-colors group"
            >
              <div className="flex items-start gap-3">
                <div className="flex-shrink-0 h-10 w-10 rounded-lg bg-muted/60 flex items-center justify-center overflow-hidden">
                  {chart.iconUrl ? (
                    <img
                      src={chart.iconUrl}
                      alt={chart.displayName}
                      width={32}
                      height={32}
                      loading="lazy"
                      className="h-8 w-8 object-contain"
                    />
                  ) : (
                    <Package className="h-5 w-5 text-muted-foreground" />
                  )}
                </div>
                <div className="flex-1 min-w-0">
                  <p className="font-medium text-foreground text-sm truncate group-hover:text-primary transition-colors">
                    {chart.displayName || chart.name}
                  </p>
                  <p className="text-xs text-muted-foreground truncate">{chart.repositoryName}</p>
                </div>
              </div>
              <p className="text-xs text-muted-foreground mt-2 line-clamp-2 min-h-[2rem]">
                {chart.description || 'No description available'}
              </p>
              <div className="flex items-center justify-between mt-3">
                <CategoryChip category={chart.category} className="text-2xs px-1.5 py-0.5" />
                <span className="text-xs font-mono text-muted-foreground">v{chart.latestVersion}</span>
              </div>
            </button>
          ))}
        </div>
      )}
    </div>
  );
}
