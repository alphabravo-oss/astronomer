import { generateBreadcrumbs, type Breadcrumb } from "@/lib/breadcrumbs";
import { useDocumentTitle } from "@/lib/use-document-title";

// Generates breadcrumbs for the current path and sets document.title from
// them (dropping the leading, always-generic first segment). Combined into
// one hook so Topbar's baselined render body doesn't grow by adding a
// second, separate hook call for the title.
export function usePageBreadcrumbs(
  pathname: string,
  clusterMap?: Record<string, string>,
): Breadcrumb[] {
  const breadcrumbs = generateBreadcrumbs(pathname, clusterMap);
  useDocumentTitle(breadcrumbs.map((crumb) => crumb.label).slice(1));
  return breadcrumbs;
}
