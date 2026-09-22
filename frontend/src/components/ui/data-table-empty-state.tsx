import { Inbox } from "lucide-react";
import { EmptyState } from "@/components/ui/empty-state";

export interface TableEmptyState {
  title: string;
  description: string;
  action?: { label: string } & (
    { href: string; onClick?: never } | { onClick: () => void; href?: never }
  );
}

export function resolveTableEmptyState(
  state: TableEmptyState,
  filtered: boolean,
  clearFilters?: () => void,
): TableEmptyState {
  return filtered
    ? {
        title: "No matching results",
        description: "Try a different search or clear the active filters.",
        action: clearFilters
          ? { label: "Clear filters", onClick: clearFilters }
          : undefined,
      }
    : state;
}

export function TableEmptyPanel({ state }: { state: TableEmptyState }) {
  return (
    <EmptyState
      icon={Inbox}
      title={state.title}
      description={state.description}
      actionLabel={state.action?.label}
      actionHref={state.action?.href}
      onAction={state.action?.onClick}
      variant="table"
    />
  );
}
