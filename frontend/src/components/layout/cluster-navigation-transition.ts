import { getResourceDef } from "@/lib/k8s-paths";
import { getClusterNamespaces } from "@/lib/api/workloads";
import { getProject } from "@/lib/api/projects";
import { getCompleteResourceDiscovery } from "@/lib/api/resources";
import { projectInCluster } from "@/lib/cluster-scope-collection";
import type { NamespaceSelection } from "@/lib/cluster-scope";
import { clusterDiscoveryFromSummaries } from "./cluster-discovery-model";

export function clusterTransitionPath(
  pathname: string,
  target: string,
  customTypes: readonly string[] = [],
) {
  const base = `/dashboard/clusters/${target}`;
  const parts = pathname
    .replace(/^\/dashboard\/clusters\/[^/]+\/?/, "")
    .split("/")
    .filter(Boolean);
  if (!parts.length) return base;
  const [type, group, version, plural] = parts;
  if (getResourceDef(type)) return `${base}/${type}`;
  if (
    type === "custom-resources" &&
    group &&
    version &&
    plural &&
    customTypes.includes(`${group}/${version}/${plural}`)
  )
    return `${base}/custom-resources/${group}/${version}/${plural}`;
  if (type === "custom-resources" && !group) return `${base}/custom-resources`;
  if (type === "delivery")
    return `${base}/delivery${["sources", "bundles", "targets", "rollouts", "deployments", "configuration-templates", "override-sets", "system-components"].includes(group) ? `/${group}` : ""}`;
  if (
    [
      "apps",
      "tools",
      "metrics",
      "logging",
      "alerting",
      "image-scans",
      "adoption",
      "template",
    ].includes(type)
  )
    return `${base}/${type}`;
  return base;
}

export async function resolveClusterTransition(
  pathname: string,
  target: string,
  stored: NamespaceSelection,
  projectId: string | null,
) {
  const namespaces = await getClusterNamespaces(target);
  const allowed = new Set(namespaces.map((item) => item.name));
  let selection =
    stored === null ? null : stored.filter((name) => allowed.has(name));
  if (projectId) {
    const project = await getProject(projectId);
    if (!projectInCluster(project, target))
      throw new Error(
        "The remembered project is no longer available in this cluster. Clear its scope before switching.",
      );
    selection = project.namespaces.filter((name) => allowed.has(name));
  }
  let types: string[] = [];
  if (pathname.includes("/custom-resources/")) {
    const definitions = await getCompleteResourceDiscovery(target);
    types = [
      ...clusterDiscoveryFromSummaries(definitions.crds).crdsByGroup.values(),
    ]
      .flat()
      .flatMap((type) =>
        type.servedVersions.map(
          (version) => `${type.group}/${version}/${type.plural}`,
        ),
      );
  }
  return {
    path: clusterTransitionPath(pathname, target, types),
    namespaces: selection,
    projectId,
  };
}
