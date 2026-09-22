import { Box } from "lucide-react";
import {
  clusterDiscoveryFromDefinitions,
  type ClusterDiscovery,
} from "./cluster-discovery-model";
import {
  filterNavGroupsByDiscovery,
  withDiscoveredNavigation,
  withStarredTypes,
} from "./cluster-discovery-navigation";
import {
  activeNavGroupLabel,
  filterNavGroups,
  getClusterNavGroups,
  type NavGroup,
} from "./sidebar-navigation";
import { navGroupItems } from "./nav-group-items";

const discovery = (
  definitions: Parameters<typeof clusterDiscoveryFromDefinitions>[0] = [],
): ClusterDiscovery => ({
  ...clusterDiscoveryFromDefinitions(definitions),
  isLoading: false,
  isError: false,
});
const definition = (group: string, plural: string, kind: string) => ({
  spec: {
    group,
    scope: "Namespaced",
    names: { plural, kind },
    versions: [{ name: "v1", served: true, storage: true }],
  },
});
const allItems = (groups: NavGroup[]) => groups.flatMap(navGroupItems);

it("hides only unserved Gateway kinds and preserves Astronomer API pages", () => {
  const groups = getClusterNavGroups("c-1");
  const empty = filterNavGroupsByDiscovery(groups, discovery());
  expect(allItems(empty).filter((item) => item.ifHaveGroup)).toHaveLength(0);
  expect(allItems(empty).map((item) => item.label)).toEqual(
    expect.arrayContaining(["Image Scans", "Service Mesh"]),
  );
  expect(groups.find((group) => group.label === "Cluster")?.items).toHaveLength(
    7,
  );
  expect(groups.some((group) => group.label === "Gateway API")).toBe(false);
  const gateway = filterNavGroupsByDiscovery(
    groups,
    discovery([definition("gateway.networking.k8s.io", "gateways", "Gateway")]),
  );
  expect(allItems(gateway).some((item) => item.label === "Gateways")).toBe(
    true,
  );
  expect(allItems(gateway).some((item) => item.label === "HTTPRoutes")).toBe(
    false,
  );
  // Preserve all built-in destinations: the original plan's <=40 target
  // could only be met by hiding unrelated supported pages.
  expect(allItems(empty)).toHaveLength(47);
});

it.each(["isLoading", "isError"] as const)(
  "does not infer absence during %s",
  (state) => {
    const groups = getClusterNavGroups("c-1");
    expect(
      filterNavGroupsByDiscovery(groups, { ...discovery(), [state]: true }),
    ).toBe(groups);
  },
);

it("groups CRDs, avoids duplicate built-in Gateway links, and caps dynamic kinds at 40", () => {
  const data = discovery([
    definition("gateway.networking.k8s.io", "gateways", "Gateway"),
    definition("cert-manager.io", "certificates", "Certificate"),
    definition("cert-manager.io", "issuers", "Issuer"),
    ...Array.from({ length: 45 }, (_, index) =>
      definition("z.example.io", `resources${index}`, `Resource${index}`),
    ),
  ]);
  const groups = withDiscoveredNavigation(
    getClusterNavGroups("c-1"),
    data,
    "c-1",
  );
  const more = groups.find((group) => group.label === "More Resources")!;
  expect(
    more.subgroups?.find((group) => group.label === "Cert Manager")?.items,
  ).toHaveLength(2);
  expect(
    allItems(groups).filter((item) => item.countKey?.startsWith("crd:")),
  ).toHaveLength(40);
  expect(
    allItems(groups).filter(
      (item) => item.resourceType === "gateway.networking.k8s.io/gateways",
    ),
  ).toHaveLength(1);
  expect(
    allItems(groups).some((item) => item.label === "All custom resources →"),
  ).toBe(true);
});

it("finds nested active routes and filters subgroup permissions before starring", () => {
  const groups: NavGroup[] = [
    {
      label: "More Resources",
      items: [],
      subgroups: [
        {
          label: "Private",
          items: [
            {
              label: "Private",
              href: "/private",
              icon: Box,
              resourceType: "test.io/private",
              permission: { resource: "custom_resources", verb: "read" },
            },
          ],
        },
      ],
    },
  ];
  expect(activeNavGroupLabel(groups, "/private/object")).toBe("More Resources");
  const filtered = filterNavGroups(groups, null);
  expect(withStarredTypes(filtered, ["test.io/private"])).toEqual([]);
});

it("stars canonical identities without plural collisions or duplicates", () => {
  const groups = withDiscoveredNavigation(
    getClusterNavGroups("c-1"),
    discovery([definition("test.io", "deployments", "CustomDeployment")]),
    "c-1",
  );
  const result = withStarredTypes(groups, [
    "apps/deployments",
    "test.io/deployments",
    "apps/deployments",
    "absent.io/types",
  ]);
  expect(result[0].label).toBe("Starred");
  expect(result[0].items.map((item) => item.label)).toEqual([
    "Deployments",
    "CustomDeployment",
  ]);
});

it("retains a starred CRD even when it sorts beyond the 40-kind cap", () => {
  const data = discovery(
    Array.from({ length: 60 }, (_, i) =>
      definition(
        "test.io",
        `types${i}`,
        `Type${i.toString().padStart(2, "0")}`,
      ),
    ),
  );
  const groups = withDiscoveredNavigation(
    getClusterNavGroups("c-1"),
    data,
    "c-1",
    ["test.io/types59"],
  );
  const starred = withStarredTypes(groups, ["test.io/types59"]);
  expect(starred[0].items[0].resourceType).toBe("test.io/types59");
  expect(
    allItems(groups).filter((item) => item.countKey?.startsWith("crd:")),
  ).toHaveLength(40);
});
