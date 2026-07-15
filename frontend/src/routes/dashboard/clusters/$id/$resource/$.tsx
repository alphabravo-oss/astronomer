// Route files are the eslint-exempted surface for direct router imports.
import { createFileRoute } from '@tanstack/react-router';
import { ResourceDetail } from '@/components/resources/resource-detail';
import { resolveDetailSlug, k8sResourcePath, getResourceDef } from '@/lib/k8s-paths';

function ResourceDetailPage() {
  // Next's `[...path]` catch-all becomes the `_splat` param: the remaining
  // path segments ([name] or [namespace, name]) joined with '/'.
  const { id: clusterId, resource: resourceType, _splat } = Route.useParams();
  const slug = _splat ? _splat.split('/').filter(Boolean) : [];

  const { namespace, name } = resolveDetailSlug(resourceType, slug);

  // Unknown/typo'd resource types reach this dynamic segment; guard before
  // k8sResourcePath (which throws on unknown types) so they render the empty
  // state instead of crashing into an error boundary.
  if (!name || !getResourceDef(resourceType)) {
    return (
      <div className="flex items-center justify-center py-24 text-sm text-muted-foreground">
        Resource not found.
      </div>
    );
  }

  const k8sPath = k8sResourcePath(resourceType, name, namespace);

  return (
    <ResourceDetail
      clusterId={clusterId}
      resourceType={resourceType}
      namespace={namespace}
      name={name}
      k8sPath={k8sPath}
    />
  );
}

export const Route = createFileRoute('/dashboard/clusters/$id/$resource/$')({
  component: ResourceDetailPage,
});
