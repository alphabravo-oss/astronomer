'use client';

import { useParams } from '@/lib/navigation';
import { ResourceDetail } from '@/components/resources/resource-detail';
import { resolveDetailSlug, k8sResourcePath } from '@/lib/k8s-paths';

export default function ResourceDetailPage() {
  const params = useParams();
  const clusterId = params.id as string;
  const resourceType = params.resource as string;
  const rawPath = params.path;
  const slug = Array.isArray(rawPath) ? rawPath : rawPath ? [rawPath] : [];

  const { namespace, name } = resolveDetailSlug(resourceType, slug);

  if (!name) {
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
