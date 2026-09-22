import { describe, expect, it } from "vitest";

import {
  descriptionForPath,
  updateManifest,
  validateGuidedResource,
  type KubernetesManifest,
} from "./guided-resource-form";

describe("guided resource form model", () => {
  it("immutably creates nested workload fields and validates image/port", () => {
    const source: KubernetesManifest = {
      apiVersion: "apps/v1",
      kind: "Deployment",
      metadata: { name: "web", namespace: "default" },
      spec: { template: { spec: { containers: [{}] } } },
    };
    const withImage = updateManifest(
      source,
      ["spec", "template", "spec", "containers", 0, "image"],
      "example/web@sha256:abc",
    );
    const complete = updateManifest(
      withImage,
      [
        "spec",
        "template",
        "spec",
        "containers",
        0,
        "ports",
        0,
        "containerPort",
      ],
      8080,
    );

    expect(source).not.toHaveProperty("spec.template.spec.containers.0.image");
    expect(complete).toHaveProperty(
      "spec.template.spec.containers.0.image",
      "example/web@sha256:abc",
    );
    expect(validateGuidedResource(complete)).toEqual({});
  });

  it("reports actionable identity, image, port, and cron errors", () => {
    const errors = validateGuidedResource({
      kind: "CronJob",
      metadata: { name: "Bad Name", namespace: "Bad_Namespace" },
      spec: {
        schedule: "not cron",
        jobTemplate: {
          spec: {
            template: {
              spec: { containers: [{ ports: [{ containerPort: 70000 }] }] },
            },
          },
        },
      },
    });
    expect(errors).toMatchObject({
      name: expect.any(String),
      namespace: expect.any(String),
      image: expect.any(String),
      containerPort: expect.any(String),
      schedule: expect.any(String),
    });
  });

  it("leaves probes unvalidated when absent, but blocks a configured probe with no port", () => {
    const base: KubernetesManifest = {
      kind: "Deployment",
      metadata: { name: "web" },
      spec: {
        template: {
          spec: {
            containers: [{ image: "nginx", readinessProbe: { httpGet: {} } }],
          },
        },
      },
    };
    expect(validateGuidedResource(base)).toHaveProperty(
      "readinessProbe",
      expect.any(String),
    );

    const withPort = updateManifest(
      base,
      [
        "spec",
        "template",
        "spec",
        "containers",
        0,
        "readinessProbe",
        "httpGet",
        "port",
      ],
      8080,
    );
    expect(validateGuidedResource(withPort)).not.toHaveProperty(
      "readinessProbe",
    );

    // No probe configured at all — never flagged.
    const noProbe: KubernetesManifest = {
      kind: "Deployment",
      metadata: { name: "web" },
      spec: {
        template: { spec: { containers: [{ image: "nginx" }] } },
      },
    };
    expect(validateGuidedResource(noProbe)).not.toHaveProperty(
      "readinessProbe",
    );
  });

  it("blocks an exec probe with no command arguments", () => {
    const errors = validateGuidedResource({
      kind: "Deployment",
      metadata: { name: "web" },
      spec: {
        template: {
          spec: {
            containers: [
              { image: "nginx", livenessProbe: { exec: { command: [] } } },
            ],
          },
        },
      },
    });
    expect(errors).toHaveProperty("livenessProbe", expect.any(String));
  });

  it("resolves descriptions through the bounded schema definition closure", () => {
    const root = {
      properties: {
        spec: { $ref: "#/components/schemas/DeploymentSpec" },
      },
    };
    const definitions = {
      DeploymentSpec: {
        properties: {
          replicas: { type: "integer", description: "Desired pod count." },
        },
      },
    };
    expect(descriptionForPath(root, definitions, ["spec", "replicas"])).toBe(
      "Desired pod count.",
    );
  });
});
