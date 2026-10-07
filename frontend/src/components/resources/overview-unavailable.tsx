import { FileQuestion } from "lucide-react";

import { EmptyState } from "@/components/ui/empty-state";

/** Typed empty state for an Overview whose object has no metadata to render. */
export function OverviewUnavailable({
  resourceType,
}: {
  resourceType: string;
}) {
  return (
    <EmptyState
      icon={FileQuestion}
      title="No overview available"
      description={`The cluster returned no metadata for this ${resourceType.replace(/s$/, "")}. Check the YAML or Events tab, or reload if it was just deleted.`}
      className="py-12"
      // Terminal: the other detail tabs are already one click away in the strip.
      terminal
    />
  );
}
