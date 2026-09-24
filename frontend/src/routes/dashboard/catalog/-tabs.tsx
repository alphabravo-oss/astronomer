import type { ReactNode } from "react";
import { pageCountLabel } from "@/lib/api/pagination";
import type { PaginatedResponse } from "@/types";

export function catalogTabs(
  installed: { data?: PaginatedResponse<unknown>; isError: boolean },
  repos: { data?: PaginatedResponse<unknown>; isError: boolean },
): { key: "browse" | "installed" | "repositories"; label: ReactNode }[] {
  const count = (query: typeof installed) =>
    query.data && !query.isError ? (
      <span className="text-xs px-1.5 py-0.5 rounded-full bg-muted text-muted-foreground tabular-nums">
        {pageCountLabel(query.data)}
      </span>
    ) : null;
  return [
    { key: "browse", label: "Browse Charts" },
    { key: "installed", label: <>Installed{count(installed)}</> },
    { key: "repositories", label: <>Repositories{count(repos)}</> },
  ];
}
