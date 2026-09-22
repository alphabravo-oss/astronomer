import { ArrowRight } from "lucide-react";
import { Link as RouterLink } from "@tanstack/react-router";

/**
 * Header row above the Home clusters table. Home only ever fetches the
 * first page (`pageSize: 10`), so once the estate grows past that page the
 * "View all" link alone hides how much is being clipped — this adds the
 * count back without pulling the rest of the rows onto the dashboard.
 */
export function ClustersSectionHeader({
  shown,
  total,
}: {
  shown: number;
  total: number;
}) {
  return (
    <div className="flex items-center justify-between">
      <h2 className="text-lg font-medium text-foreground">Clusters</h2>
      <div className="flex items-center gap-2">
        {total > shown && (
          <span className="text-sm text-muted-foreground">
            Showing {shown} of {total} clusters ·
          </span>
        )}
        <RouterLink
          to="/dashboard/clusters"
          className="inline-flex items-center gap-1 text-sm text-muted-foreground hover:text-foreground transition-colors"
        >
          View all
          <ArrowRight className="h-3.5 w-3.5" />
        </RouterLink>
      </div>
    </div>
  );
}
