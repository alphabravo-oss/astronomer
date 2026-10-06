import { LoadingPanel } from "@/components/charlie/loading-panel";
import { useQuery } from "@tanstack/react-query";
import { queryKeys } from "@/lib/query-keys";
import { getCharlieAccess } from "@/lib/api/charlie-admin";
import { GrantList, Unavailable } from "./shared";

export function AccessTab() {
  const q = useQuery({
    queryKey: queryKeys.charlie.adminAccess,
    queryFn: ({ signal }) => getCharlieAccess(signal),
    retry: false,
  });
  if (q.isLoading) return <LoadingPanel title="Loading Charlie access" />;
  if (q.isError)
    return <Unavailable name="Access report" retry={() => void q.refetch()} />;
  return (
    <div className="grid gap-4 lg:grid-cols-2">
      <GrantList
        title="Your Charlie permissions"
        items={q.data?.effectivePermissions ?? []}
      />
      <GrantList
        title="Automation service identity"
        items={q.data?.automationGrants ?? []}
      />
    </div>
  );
}
