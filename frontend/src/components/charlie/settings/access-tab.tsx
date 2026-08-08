import { useQuery } from "@tanstack/react-query";
import { Loader2 } from "lucide-react";
import { StatePanel } from "@/components/ui/empty-state";
import { queryKeys } from "@/lib/query-keys";
import { getCharlieAccess } from "@/lib/api/charlie-admin";
import { GrantList, Unavailable } from "./shared";

export function AccessTab() {
  const q = useQuery({
    queryKey: queryKeys.charlie.adminAccess,
    queryFn: getCharlieAccess,
    retry: false,
  });
  if (q.isLoading)
    return (
      <StatePanel
        icon={Loader2}
        iconClassName="animate-spin motion-reduce:animate-none"
        title="Loading Charlie access"
      />
    );
  if (q.isError)
    return <Unavailable name="Access report" retry={() => void q.refetch()} />;
  return (
    <div className="grid gap-4 lg:grid-cols-2">
      <GrantList
        title="Effective user permissions"
        items={q.data?.effectivePermissions ?? []}
      />
      <GrantList
        title="Automation grants"
        items={q.data?.automationGrants ?? []}
      />
    </div>
  );
}
