import { useState } from "react";
import { fireEvent, render, screen, within } from "@testing-library/react";
import { SchedulingFields } from "../guided-scheduling-fields";
import { IngressRulesSection } from "../guided-rule-fields";
import {
  containerPath,
  podSpecPath,
  updateManifest,
  validateGuidedResource,
  type KubernetesManifest,
} from "../guided-resource-model";
import type { GuidedFormState } from "../guided-resource-fields";
import { validateAffinity } from "../guided-affinity-validation";
import { validateIngress } from "../guided-ingress-validation";

const required = "requiredDuringSchedulingIgnoredDuringExecution";
const preferred = "preferredDuringSchedulingIgnoredDuringExecution";
function Harness({
  initial,
  change,
}: {
  initial: KubernetesManifest;
  change: (value: KubernetesManifest) => void;
}) {
  const [value, setValue] = useState(initial);
  const apply = (next: KubernetesManifest) => {
    setValue(next);
    change(next);
  };
  const kind = String(value.kind);
  const form: GuidedFormState = {
    value,
    kind,
    podPath: podSpecPath(kind),
    containerPath: containerPath(kind),
    errors: validateGuidedResource(value),
    identityReadOnly: false,
    onChange: apply,
    doc: () => "",
    set: (path, next) => apply(updateManifest(value, path, next)),
    setNumber: (path, next) =>
      apply(
        updateManifest(value, path, next === "" ? undefined : Number(next)),
      ),
  };
  return kind === "Ingress" ? (
    <IngressRulesSection form={form} />
  ) : (
    <SchedulingFields form={form} />
  );
}

it("edits preferred node affinity while preserving required terms, matchFields and pod scheduling fields", () => {
  const node = {
    [required]: {
      nodeSelectorTerms: [
        {
          matchFields: [
            { key: "metadata.name", operator: "In", values: ["node-a"] },
          ],
        },
      ],
    },
    [preferred]: [
      {
        weight: 10,
        preference: {
          matchFields: [
            { key: "metadata.name", operator: "In", values: ["node-b"] },
          ],
          matchExpressions: [{ key: "disk", operator: "In", values: ["ssd"] }],
        },
      },
    ],
  };
  const initial = {
    kind: "Deployment",
    metadata: { name: "app" },
    spec: {
      template: {
        spec: {
          schedulerName: "custom",
          containers: [{ image: "demo" }],
          affinity: { nodeAffinity: node },
        },
      },
    },
  };
  let current: KubernetesManifest = initial;
  render(
    <Harness
      initial={initial}
      change={(next) => {
        current = next;
      }}
    />,
  );
  fireEvent.change(screen.getByLabelText("Weight (1–100)"), {
    target: { value: "80" },
  });
  expect(current).toHaveProperty(
    `spec.template.spec.affinity.nodeAffinity.${preferred}.0.weight`,
    80,
  );
  expect(current).toHaveProperty(
    `spec.template.spec.affinity.nodeAffinity.${required}`,
    node[required],
  );
  expect(current).toHaveProperty(
    `spec.template.spec.affinity.nodeAffinity.${preferred}.0.preference.matchFields`,
    node[preferred][0].preference.matchFields,
  );
  expect(current).toHaveProperty("spec.template.spec.schedulerName", "custom");
  expect(
    initial.spec.template.spec.affinity.nodeAffinity[preferred][0].weight,
  ).toBe(10);
});

it("keeps null selectors distinct from all-namespace selectors and preserves unknown pod term fields", () => {
  const initial = {
    kind: "Deployment",
    metadata: { name: "app" },
    spec: {
      template: {
        spec: {
          containers: [{ image: "demo" }],
          affinity: {
            podAntiAffinity: {
              [required]: [
                {
                  topologyKey: "zone",
                  namespaceSelector: null,
                  labelSelector: null,
                  matchLabelKeys: ["pod-template-hash"],
                  namespaces: ["team-a"],
                },
              ],
            },
          },
        },
      },
    },
  };
  let current: KubernetesManifest = initial;
  render(
    <Harness
      initial={initial}
      change={(next) => {
        current = next;
      }}
    />,
  );
  fireEvent.change(screen.getByLabelText("Topology key"), {
    target: { value: "rack" },
  });
  const prefix = `spec.template.spec.affinity.podAntiAffinity.${required}.0`;
  expect(current).toHaveProperty(`${prefix}.namespaceSelector`, null);
  fireEvent.change(screen.getByLabelText("Namespace selector mode"), {
    target: { value: "selector" },
  });
  expect(current).toHaveProperty(`${prefix}.namespaceSelector`, {});
  expect(current).toHaveProperty(`${prefix}.labelSelector`, null);
  expect(current).toHaveProperty(`${prefix}.matchLabelKeys`, [
    "pod-template-hash",
  ]);
  expect(current).toHaveProperty(`${prefix}.namespaces`, ["team-a"]);
  fireEvent.change(screen.getByLabelText("Namespace selector mode"), {
    target: { value: "absent" },
  });
  expect(current).not.toHaveProperty(`${prefix}.namespaceSelector`);
});

it.each([0, 101, 1.5, undefined])("rejects affinity weight %s", (weight) => {
  expect(
    validateAffinity(
      { nodeAffinity: { [preferred]: [{ weight, preference: {} }] } },
      ["affinity"],
    ),
  ).toHaveProperty(`affinity.nodeAffinity.${preferred}.0.weight`);
});
it("checks node value operators and pod topology/label operators without rewriting the manifest", () => {
  const source = {
    nodeAffinity: {
      [preferred]: [
        {
          weight: 100,
          preference: {
            matchExpressions: [
              { key: "cpu", operator: "Gt", values: ["1", "2"] },
              { key: "gpu", operator: "Exists", values: ["yes"] },
            ],
          },
        },
      ],
    },
    podAffinity: {
      [required]: [
        {
          topologyKey: "",
          labelSelector: {
            matchExpressions: [{ key: "cpu", operator: "Gt", values: ["1"] }],
          },
        },
      ],
    },
  };
  const clone = structuredClone(source);
  expect(Object.keys(validateAffinity(source, ["affinity"]))).toHaveLength(4);
  expect(source).toEqual(clone);
  expect(
    validateAffinity(
      {
        nodeAffinity: {
          [preferred]: [
            {
              weight: 1,
              preference: {
                matchExpressions: [
                  { key: "cpu", operator: "Gt", values: ["-1"] },
                ],
              },
            },
          ],
        },
      },
      [],
    ),
  ).toEqual({});
});

it("edits Ingress resource references and switches unions without dropping sibling rules or extensions", () => {
  const sibling = {
    host: "other.example",
    http: {
      paths: [
        {
          path: "/",
          pathType: "Prefix",
          backend: { service: { name: "other", port: { name: "http" } } },
        },
      ],
    },
  };
  const initial = {
    kind: "Ingress",
    metadata: { name: "web" },
    spec: {
      tls: [{ secretName: "tls" }],
      rules: [
        {
          host: "web.example",
          http: {
            paths: [
              {
                path: "/",
                pathType: "Prefix",
                backend: {
                  resource: {
                    apiGroup: "storage.example",
                    kind: "Bucket",
                    name: "old",
                  },
                  extension: "keep",
                },
              },
            ],
          },
        },
        sibling,
      ],
    },
  };
  let current: KubernetesManifest = initial;
  render(
    <Harness
      initial={initial}
      change={(next) => {
        current = next;
      }}
    />,
  );
  fireEvent.change(screen.getByLabelText("Backend resource name"), {
    target: { value: "assets" },
  });
  expect(current).toHaveProperty(
    "spec.rules.0.http.paths.0.backend.resource.name",
    "assets",
  );
  fireEvent.change(
    screen.getAllByLabelText("Backend type", { exact: false })[0],
    { target: { value: "service" } },
  );
  expect(current).not.toHaveProperty(
    "spec.rules.0.http.paths.0.backend.resource",
  );
  expect(current).toHaveProperty(
    "spec.rules.0.http.paths.0.backend.extension",
    "keep",
  );
  const ports = screen.getAllByLabelText("Backend port");
  fireEvent.change(ports[0], { target: { value: "http" } });
  expect(current).toHaveProperty(
    "spec.rules.0.http.paths.0.backend.service.port",
    { name: "http" },
  );
  expect(current).toHaveProperty("spec.rules.1", sibling);
  expect(current).toHaveProperty("spec.tls", initial.spec.tls);
});
it("creates and removes a default backend explicitly", () => {
  let current: KubernetesManifest = {};
  render(
    <Harness
      initial={{ kind: "Ingress", metadata: { name: "web" }, spec: {} }}
      change={(next) => {
        current = next;
      }}
    />,
  );
  fireEvent.click(
    screen.getByRole("checkbox", { name: "Configure a default backend" }),
  );
  const section = screen.getByRole("group", { name: "Default backend" });
  fireEvent.change(
    within(section).getByLabelText("Backend type", { exact: false }),
    { target: { value: "resource" } },
  );
  fireEvent.change(
    screen.getByLabelText("Backend resource kind", { exact: false }),
    {
      target: { value: "Bucket" },
    },
  );
  fireEvent.change(
    screen.getByLabelText("Backend resource name", { exact: false }),
    {
      target: { value: "assets" },
    },
  );
  expect(validateGuidedResource(current)).toEqual({});
  fireEvent.click(
    screen.getByRole("checkbox", { name: "Configure a default backend" }),
  );
  expect(current).not.toHaveProperty("spec.defaultBackend");
});
it("rejects conflicting Ingress backend and port unions", () => {
  expect(
    validateIngress({ defaultBackend: { resource: {}, service: {} } }),
  ).toHaveProperty("spec.defaultBackend");
  expect(
    validateIngress({
      defaultBackend: {
        service: { name: "web", port: { name: "http", number: 80 } },
      },
    }),
  ).toHaveProperty("spec.defaultBackend.service.port");
  expect(
    validateIngress({
      defaultBackend: { service: { name: "web", port: { number: 65536 } } },
    }),
  ).toHaveProperty("spec.defaultBackend.service.port");
});
