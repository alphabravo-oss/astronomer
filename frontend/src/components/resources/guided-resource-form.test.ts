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
