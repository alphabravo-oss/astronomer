// Route files are the eslint-exempted surface for direct router imports.
// Splat half of Next's optional catch-all `[[...slug]]`: the remaining path
// segments ([group, version, plural, ...]) arrive as the `_splat` param.
import { createFileRoute } from '@tanstack/react-router';
import { CustomResourcesPage } from '@/components/clusters/custom-resources-page';

function CustomResourcesSplatRoute() {
  const { _splat } = Route.useParams();
  const slug = _splat ? _splat.split('/').filter(Boolean) : [];
  return <CustomResourcesPage slug={slug} />;
}

export const Route = createFileRoute('/dashboard/clusters/$id/custom-resources/$')({
  component: CustomResourcesSplatRoute,
});
