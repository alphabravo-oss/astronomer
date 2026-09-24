import {
  clusterDiscoveryFromDefinitions,
  type ClusterDiscovery,
} from "@/components/layout/cluster-discovery-model";
import {
  createManifestBatch,
  createPathForManifest,
} from "./create-resource-manifest";

const discovery: ClusterDiscovery = {
  ...clusterDiscoveryFromDefinitions([
    {
      spec: {
        group: "example.io",
        scope: "Namespaced",
        names: { plural: "people", kind: "Person" },
        versions: [
          { name: "v1", served: true, storage: true },
          { name: "v1beta1", served: true },
          { name: "v1alpha1", served: false },
        ],
      },
    },
    {
      spec: {
        group: "example.io",
        scope: "Cluster",
        names: { plural: "clusterpeople", kind: "ClusterPerson" },
        versions: [{ name: "v1", served: true }],
      },
    },
    {
      spec: {
        group: "other.io",
        scope: "Cluster",
        names: { plural: "persons", kind: "Person" },
        versions: [{ name: "v1", served: true }],
      },
    },
    {
      spec: {
        group: "example.io",
        scope: "Cluster",
        names: { plural: "customsecrets", kind: "Secret" },
        versions: [{ name: "v1", served: true }],
      },
    },
  ]),
  isLoading: false,
  isError: false,
};

it.each([
  ["example.io/v1", "Person", "apis/example.io/v1/namespaces/team-a/people"],
  [
    "example.io/v1beta1",
    "Person",
    "apis/example.io/v1beta1/namespaces/team-a/people",
  ],
  ["example.io/v1", "ClusterPerson", "apis/example.io/v1/clusterpeople"],
  ["other.io/v1", "Person", "apis/other.io/v1/persons"],
  ["example.io/v1", "Secret", "apis/example.io/v1/customsecrets"],
])(
  "resolves %s %s using the actual plural, version, and scope",
  (apiVersion, kind, path) => {
    expect(
      createPathForManifest(
        { apiVersion, kind, metadata: { namespace: "team-a" } },
        undefined,
        undefined,
        discovery,
      ),
    ).toBe(path);
  },
);

it.each([
  ["example.io/v1alpha1", "Person"],
  ["example.io/v2", "Person"],
  ["unknown.io/v1", "Person"],
  ["unknown.io/v1", "Secret"],
  ["example.io/v1/extra", "Person"],
  ["example.io/v1", "Unknown"],
])("does not guess an endpoint for %s %s", (apiVersion, kind) => {
  expect(
    createPathForManifest(
      { apiVersion, kind },
      undefined,
      undefined,
      discovery,
    ),
  ).toBeUndefined();
});

it("defaults namespace and resolves mixed built-in/custom documents independently", () => {
  const items = createManifestBatch(
    [
      { apiVersion: "v1", kind: "ConfigMap" },
      { apiVersion: "example.io/v1", kind: "Person" },
      { apiVersion: "example.io/v1", kind: "ClusterPerson" },
    ],
    undefined,
    "wrong/primary/path",
    discovery,
  );
  expect(items.map((item) => item.path)).toEqual([
    "api/v1/namespaces/default/configmaps",
    "apis/example.io/v1/namespaces/default/people",
    "apis/example.io/v1/clusterpeople",
  ]);
});

it.each([
  [{ isLoading: true }, "still loading"],
  [{ isError: true }, "unavailable or access was denied"],
])("rejects unresolved CRDs while discovery is %j", (state, message) => {
  expect(() =>
    createManifestBatch(
      [{ apiVersion: "example.io/v1", kind: "Person" }],
      undefined,
      undefined,
      { ...discovery, ...state },
    ),
  ).toThrow(message);
  expect(
    createManifestBatch(
      [{ apiVersion: "v1", kind: "Secret" }],
      undefined,
      undefined,
      { ...discovery, ...state },
    )[0].path,
  ).toBe("api/v1/namespaces/default/secrets");
});
