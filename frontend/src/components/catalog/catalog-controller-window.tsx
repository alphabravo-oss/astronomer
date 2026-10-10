import { useQuery } from "@tanstack/react-query";
import { useCurrentUser } from "@/lib/hooks/auth";
import { getCatalogControllerWindow, type CatalogControllerOperationWindow } from "@/lib/api/catalog-controller";

export function CatalogControllerWindowView({ window, unavailable }: {
  window?: CatalogControllerOperationWindow | null;
  unavailable: boolean;
}) {
  if (unavailable || !window) return <p role="status" className="text-sm text-muted-foreground">Recent catalog operation summary unavailable. No fleet or queue total is known.</p>;
  return (
    <section aria-label="Recent catalog operations" className="space-y-1 text-sm">
      <p>{window.returned} recent operations from {window.scope === "global" ? "global scope" : "authorized clusters"} (window limit {window.windowLimit}).</p>
      <p>{window.total === null ? "Authorized operation total unknown." : `Separately observed authorized operation count: ${window.total}.`}</p>
      {!window.complete && <p role="status">This window is incomplete or lacks a consistent snapshot. Operation counts and queue depth are not fleet totals.</p>}
      {window.notice && <p className="text-muted-foreground">{window.notice}</p>}
    </section>
  );
}

export function CatalogControllerWindow() {
  const user = useCurrentUser();
  const identity = user.data;
  const query = useQuery({
    queryKey: ["catalog", "controller-window", identity?.id, identity?.isSuperuser, identity?.roles],
    queryFn: ({ signal }) => getCatalogControllerWindow(signal),
    enabled: user.isSuccess && identity?.enabled === true,
    retry: false,
    staleTime: 0,
    gcTime: 0,
    refetchOnMount: "always",
  });
  // A role change selects another query. A failed or in-flight reauthorization
  // never displays retained data, even when the query previously succeeded.
  return <CatalogControllerWindowView window={query.data} unavailable={!user.isSuccess || !identity?.enabled || query.isError || query.isFetching || !query.isSuccess} />;
}
