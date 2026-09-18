import { createFileRoute } from "@tanstack/react-router";
import { SearchPage, WORKLOAD_TYPES } from "../search/-page";

export const Route = createFileRoute("/dashboard/workloads/")({
  validateSearch: (search: Record<string, unknown>) => search as {
    type?: string;
    namespace?: string;
    label?: string;
    name?: string;
  } & Record<string, unknown>,
  component: WorkloadsPage,
});

function WorkloadsPage() {
  return <SearchPage
    title="Workloads"
    description="Inspect workloads across your authorized clusters"
    resourceTypes={WORKLOAD_TYPES}
  />;
}
