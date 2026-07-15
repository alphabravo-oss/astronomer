// Route files are the eslint-exempted surface for direct router imports.
// Body lives in the `-`-prefixed co-located module so the route stays
// code-splittable while the co-located test imports the component directly.
import { createFileRoute } from '@tanstack/react-router';
import { ClusterGatekeeperPage } from './-page';

export const Route = createFileRoute('/dashboard/clusters/$id/gatekeeper/')({
  component: ClusterGatekeeperPage,
});
