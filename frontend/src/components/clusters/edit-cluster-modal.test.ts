import { describe, expect, it } from "vitest";

import { buildClusterEditRequest } from "./edit-cluster-modal";

describe("buildClusterEditRequest", () => {
  it("uses the generated wire contract and sends only modal-owned fields", () => {
    expect(
      buildClusterEditRequest({
        displayName: "Production",
        environment: "production",
        description: "",
        apiServerUrl: "https://api.example.test:6443",
        caCertificate: "public-ca",
        badgeText: "Production",
        badgeColor: "red",
        agentOverrides: {},
      }),
    ).toEqual({
      display_name: "Production",
      environment: "production",
      description: "",
      api_server_url: "https://api.example.test:6443",
      ca_certificate: "public-ca",
      badge_text: "Production",
      badge_color: "red",
      agent_overrides: {},
    });
  });
});

it("persists scanner opt-out while preserving other cluster annotations", () => {
  const input = buildClusterEditRequest({
    displayName: "Remote",
    environment: "development",
    description: "",
    apiServerUrl: "",
    caCertificate: "",
    badgeText: "",
    badgeColor: "slate",
    imageScanning: {
      enabled: false,
      annotations: {
        "example.com/owner": "ops",
        "astronomer.io/image-scanning": "enabled",
      },
    },
  });
  expect(input.annotations).toEqual({
    "example.com/owner": "ops",
    "astronomer.io/image-scanning": "disabled",
  });
});
