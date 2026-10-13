import { createFileRoute } from "@tanstack/react-router";
import { useQuery } from "@tanstack/react-query";
import { QueryStates } from "@/components/ui/query-states";
import { getClusterApp } from "@/lib/api/cluster-apps";
import { queryKeys } from "@/lib/query-keys";
import {
  ApplicationConfiguration,
  type ApplicationConfigurationSearch,
} from "./-configuration-page";

export const Route = createFileRoute("/dashboard/catalog/configure")({
  validateSearch: (
    raw: Record<string, unknown>,
  ): ApplicationConfigurationSearch =>
    Object.fromEntries(
      ["chart", "version", "project", "cluster", "release", "origin"].flatMap(
        (key) => (typeof raw[key] === "string" ? [[key, raw[key]]] : []),
      ),
    ),
  component: ApplicationConfigurationRoute,
});

function ApplicationConfigurationRoute() {
  const search = Route.useSearch();
  const query = useQuery({
    queryKey: queryKeys.catalog.release(search.release ?? ""),
    queryFn: ({ signal }) => getClusterApp(search.release!, signal),
    enabled: !!search.release,
    throwOnError: false,
  });
  if (search.release)
    return (
      <QueryStates
        query={query}
        permission="catalog:read"
        errorTitle="Release unavailable"
      >
        {(release) => (
          <ApplicationConfiguration search={search} release={release} />
        )}
      </QueryStates>
    );
  return <ApplicationConfiguration search={search} />;
}
