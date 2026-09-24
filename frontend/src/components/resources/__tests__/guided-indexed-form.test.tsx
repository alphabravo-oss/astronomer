import { useState } from "react";
import { fireEvent, render, screen, within } from "@testing-library/react";
import { GuidedResourceForm } from "../guided-resource-form";
import {
  validateGuidedResource,
  type KubernetesManifest,
} from "../guided-resource-model";
import { renameVolume } from "../guided-volume-model";

const podPath = ["spec", "template", "spec"];
const original = {
  kind: "Deployment",
  metadata: { name: "web", namespace: "app" },
  spec: {
    template: {
      spec: {
        containers: [
          {
            name: "web",
            image: "web:v1",
            ports: [{ containerPort: 80 }, { containerPort: 9000 }],
            env: [
              { name: "MODE", value: "prod" },
              {
                name: "TOKEN",
                valueFrom: { secretKeyRef: { name: "auth", key: "token" } },
              },
            ],
            envFrom: [
              { secretRef: { name: "first", optional: true } },
              { configMapRef: { name: "second" } },
            ],
            volumeMounts: [{ name: "cache", mountPath: "/cache" }],
          },
          {
            name: "sidecar",
            image: "proxy:v1",
            volumeMounts: [{ name: "cache", mountPath: "/shared" }],
          },
        ],
        initContainers: [
          {
            name: "init",
            image: "init:v1",
            volumeMounts: [{ name: "cache", mountPath: "/init" }],
          },
        ],
        volumes: [
          { name: "cache", emptyDir: {} },
          { name: "secret", secret: { secretName: "auth" } },
        ],
      },
    },
  },
};

function Harness({
  onChange,
  initial = original,
}: {
  onChange: (next: KubernetesManifest) => void;
  initial?: KubernetesManifest;
}) {
  const [value, setValue] = useState(initial);
  return (
    <GuidedResourceForm
      value={value}
      onChange={(next) => {
        setValue(next);
        onChange(next);
      }}
      schema={{}}
      definitions={{}}
    />
  );
}

describe("indexed guided resource editors", () => {
  it("edits the second RBAC rule while preserving a core-group rule", () => {
    const initial = {
      kind: "Role",
      metadata: { name: "reader" },
      rules: [
        { apiGroups: [""], resources: ["pods"], verbs: ["get"] },
        {
          apiGroups: ["apps"],
          resources: ["deployments"],
          verbs: ["get"],
          resourceNames: ["one"],
        },
      ],
    };
    let next: KubernetesManifest = initial;
    render(
      <Harness
        initial={initial}
        onChange={(value) => {
          next = value;
        }}
      />,
    );
    const second = within(screen.getByRole("group", { name: "RBAC rules 2" }));
    fireEvent.change(second.getByLabelText(/^Verbs/), {
      target: { value: "get, list" },
    });
    expect(next).toHaveProperty("rules.0", initial.rules[0]);
    expect(next).toHaveProperty("rules.1.verbs", ["get", "list"]);
    expect(next).toHaveProperty("rules.1.resourceNames", ["one"]);
    fireEvent.change(second.getByLabelText(/^API groups/), {
      target: { value: "" },
    });
    expect(next).toHaveProperty("rules.1.apiGroups", [""]);
  });
  it("changes an Ingress named backend port without destroying sibling rules", () => {
    const initial = {
      kind: "Ingress",
      metadata: { name: "web" },
      spec: {
        rules: [
          {
            host: "one.example",
            http: {
              paths: [
                {
                  path: "/",
                  backend: { service: { name: "web", port: { number: 80 } } },
                },
              ],
            },
          },
          {
            host: "two.example",
            http: {
              paths: [
                {
                  path: "/api",
                  backend: {
                    resource: {
                      apiGroup: "example.io",
                      kind: "Store",
                      name: "store",
                    },
                  },
                },
              ],
            },
          },
        ],
      },
    };
    let next: KubernetesManifest = initial;
    render(
      <Harness
        initial={initial}
        onChange={(value) => {
          next = value;
        }}
      />,
    );
    fireEvent.change(screen.getByLabelText("Backend port"), {
      target: { value: "http" },
    });
    expect(next).toHaveProperty(
      "spec.rules.0.http.paths.0.backend.service.port",
      { name: "http" },
    );
    expect(next).toHaveProperty("spec.rules.1", initial.spec.rules[1]);
  });
  it("edits a sidecar and init container without changing siblings", () => {
    let next: KubernetesManifest = original;
    render(
      <Harness
        onChange={(value) => {
          next = value;
        }}
      />,
    );
    fireEvent.change(screen.getByLabelText("Edit container"), {
      target: { value: "containers:1" },
    });
    fireEvent.change(screen.getByLabelText(/Container image/), {
      target: { value: "proxy:v2" },
    });
    expect(next).toHaveProperty(
      "spec.template.spec.containers.1.image",
      "proxy:v2",
    );
    expect(next).toHaveProperty(
      "spec.template.spec.containers.0",
      original.spec.template.spec.containers[0],
    );
    fireEvent.change(screen.getByLabelText("Edit container"), {
      target: { value: "initContainers:0" },
    });
    fireEvent.change(screen.getByLabelText(/Container image/), {
      target: { value: "init:v2" },
    });
    expect(next).toHaveProperty(
      "spec.template.spec.initContainers.0.image",
      "init:v2",
    );
    expect(next).toHaveProperty(
      "spec.template.spec.containers.1.image",
      "proxy:v2",
    );
  });
  it("edits literal env and reversed envFrom source order without corrupting references", () => {
    let next: KubernetesManifest = original;
    render(
      <Harness
        onChange={(value) => {
          next = value;
        }}
      />,
    );
    fireEvent.change(screen.getByLabelText("Variable value"), {
      target: { value: "staging" },
    });
    expect(next).toHaveProperty(
      "spec.template.spec.containers.0.env.1",
      original.spec.template.spec.containers[0].env![1],
    );
    const first = within(
      screen.getByRole("group", { name: "Environment sources 1" }),
    );
    fireEvent.change(first.getByLabelText("Environment source name"), {
      target: { value: "new-secret" },
    });
    expect(next).toHaveProperty("spec.template.spec.containers.0.envFrom", [
      { secretRef: { name: "new-secret", optional: true } },
      { configMapRef: { name: "second" } },
    ]);
  });
  it("removes only the chosen port and maintains dense arrays", () => {
    let next: KubernetesManifest = original;
    render(
      <Harness
        onChange={(value) => {
          next = value;
        }}
      />,
    );
    fireEvent.click(screen.getByLabelText("Remove Container ports 1"));
    expect(next).toHaveProperty("spec.template.spec.containers.0.ports", [
      { containerPort: 9000 },
    ]);
  });
  it("adds and removes containers with unique names", () => {
    let next: KubernetesManifest = original;
    render(
      <Harness
        onChange={(value) => {
          next = value;
        }}
      />,
    );
    fireEvent.click(screen.getByRole("button", { name: "Add init container" }));
    expect(next).toHaveProperty(
      "spec.template.spec.initContainers.1.name",
      "container-1",
    );
    expect(validateGuidedResource(next)).toHaveProperty(
      "initContainers.1.image",
    );
    fireEvent.click(
      screen.getByRole("button", { name: "Remove selected container" }),
    );
    expect(next).toHaveProperty(
      "spec.template.spec.initContainers",
      original.spec.template.spec.initContainers,
    );
  });
  it("renames all mount references and prevents deleting a referenced volume", () => {
    const next = renameVolume(original, podPath, 0, "data");
    expect(next).toHaveProperty(
      "spec.template.spec.containers.0.volumeMounts.0.name",
      "data",
    );
    expect(next).toHaveProperty(
      "spec.template.spec.containers.1.volumeMounts.0.name",
      "data",
    );
    expect(next).toHaveProperty(
      "spec.template.spec.initContainers.0.volumeMounts.0.name",
      "data",
    );
    expect(original.spec.template.spec.volumes[0].name).toBe("cache");
    render(<Harness onChange={() => {}} />);
    expect(screen.getByLabelText("Remove Pod volumes 1")).toBeDisabled();
  });
  it("validates all container ports and probes, not only the first", () => {
    const value = structuredClone(original) as KubernetesManifest;
    const invalid = {
      ...original,
      spec: {
        template: {
          spec: {
            ...original.spec.template.spec,
            containers: [
              original.spec.template.spec.containers[0],
              { image: "proxy:v1", ports: [{ containerPort: 99999 }] },
            ],
          },
        },
      },
    };
    expect(validateGuidedResource(invalid)).toHaveProperty(
      "containers.1.containerPort",
    );
    expect(validateGuidedResource(value)).toEqual({});
  });
});
