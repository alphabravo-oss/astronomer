import { previewToolFieldValues } from "@/components/clusters/tool-values";
import type { ToolFormField } from "@/types";

const fields: ToolFormField[] = [
  {
    path: "base.defaultRevision",
    label: "Revision",
    group: "Base",
    type: "string",
  },
  {
    path: "istiod.replicaCount",
    label: "Replicas",
    group: "Control",
    type: "number",
  },
  {
    path: "istiod.autoscaleEnabled",
    label: "Autoscale",
    group: "Control",
    type: "boolean",
  },
];

it("isolates release value keys and uses the declared default namespace", () => {
  expect(
    previewToolFieldValues(
      [
        {
          chartName: "base",
          chartVersion: "1",
          namespace: "mesh",
          valuesYaml: "defaultRevision: default",
        },
        {
          chartName: "istiod",
          chartVersion: "1",
          namespace: "mesh",
          valuesYaml: "replicaCount: 1\nautoscaleEnabled: false",
        },
      ],
      {
        defaultNamespace: "mesh",
        charts: [
          {
            chartName: "base",
            repoUrl: "https://example.com",
            namespace: "",
            order: 0,
            valuesKey: "base",
          },
          {
            chartName: "istiod",
            repoUrl: "https://example.com",
            namespace: "mesh",
            order: 1,
            valuesKey: "istiod",
          },
        ],
      },
      fields,
    ),
  ).toEqual({
    "base.defaultRevision": "default",
    "istiod.replicaCount": "1",
    "istiod.autoscaleEnabled": "false",
  });
});

it("keeps single-release values at the root and leaves absent fields unset", () => {
  expect(
    previewToolFieldValues(
      [
        {
          chartName: "controller",
          chartVersion: "1",
          namespace: "mesh",
          valuesYaml: "replicas: 2",
        },
      ],
      {
        defaultNamespace: "mesh",
        charts: [
          {
            chartName: "controller",
            repoUrl: "https://example.com",
            namespace: "mesh",
            order: 0,
          },
        ],
      },
      [
        {
          path: "replicas",
          label: "Replicas",
          group: "Control",
          type: "number",
        },
        { path: "missing", label: "Missing", group: "Control", type: "string" },
      ],
    ),
  ).toEqual({ replicas: "2" });
});
