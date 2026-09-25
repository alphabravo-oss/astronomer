import { createFileRoute } from "@tanstack/react-router";
import { SourcesPage } from "./-page";

export const Route = createFileRoute("/dashboard/delivery/sources/")({
  validateSearch: (search: Record<string, unknown>) => {
    const project = typeof search.project === "string" ? search.project : null;
    const status = typeof search.status === "string" ? search.status : null;
    return {
      ...(project ? { project } : {}),
      ...(status ? { status } : {}),
    };
  },
  component: SourcesPage,
});
