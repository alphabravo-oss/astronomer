import { Package, SearchX } from "lucide-react";
import { EmptyState } from "@/components/ui/empty-state";

export function ChartEmptyState({
  filtered,
  onClearFilters,
  onManageRepositories,
}: {
  filtered: boolean;
  onClearFilters: () => void;
  onManageRepositories: () => void;
}) {
  return (
    <EmptyState
      icon={filtered ? SearchX : Package}
      title={filtered ? "No charts match these filters" : "No charts available"}
      description={
        filtered
          ? "These filters apply to the current page. Try another page or clear the filters."
          : "Try another page, or add and sync a Helm repository."
      }
      actionLabel={filtered ? "Clear filters" : "Manage repositories"}
      onAction={filtered ? onClearFilters : onManageRepositories}
    />
  );
}
