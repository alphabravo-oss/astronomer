import { isNamespaced } from "@/lib/k8s-paths";

export interface ClusterScopeApplicability {
  project: boolean;
  namespaces: boolean;
}

const NONE: ClusterScopeApplicability = {
  project: false,
  namespaces: false,
};

const PROJECT_ROUTES = new Set(["delivery", "apps"]);
const NAMESPACED_WORKSPACES = new Set([
  "workloads",
  "resources",
  "network-policies",
]);

/**
 * Declare which shared scope controls actually govern a cluster page.
 *
 * Scope remains remembered when a control is hidden, but cluster-wide pages
 * must not imply that their data is filtered by a project or namespace.
 */
export function clusterScopeApplicability(
  pathname: string,
  searchStr = "",
): ClusterScopeApplicability {
  const match = pathname.match(
    /^\/dashboard\/clusters\/[^/]+(?:\/([^/]+))?(?:\/(.*))?$/,
  );
  if (!match) return NONE;

  const section = match[1] ?? "";
  const remainder = match[2] ?? "";

  if (PROJECT_ROUTES.has(section)) {
    if (
      section === "apps" &&
      new URLSearchParams(searchStr).get("section") === "repositories"
    )
      return NONE;
    return { project: true, namespaces: false };
  }

  if (section === "custom-resources") {
    // The landing page is discovery-only. Once a concrete G/V/R collection is
    // open, keep the namespace control available for namespaced CRDs.
    return remainder.split("/").filter(Boolean).length >= 3
      ? { project: true, namespaces: true }
      : NONE;
  }

  if (NAMESPACED_WORKSPACES.has(section) || isNamespaced(section))
    return { project: true, namespaces: true };

  return NONE;
}
