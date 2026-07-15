// Route files are the eslint-exempted surface for direct router imports.
// Next's optional catch-all `[[...slug]]` splits into this index (empty slug
// → CRD list) plus the sibling `$.tsx` splat route; both mount the shared
// component (route files must not import each other under autoCodeSplitting).
import { createFileRoute } from '@tanstack/react-router';
import { CustomResourcesPage } from '@/components/clusters/custom-resources-page';

function CRDListRoute() {
  return <CustomResourcesPage slug={[]} />;
}

export const Route = createFileRoute('/dashboard/clusters/$id/custom-resources/')({
  component: CRDListRoute,
});
