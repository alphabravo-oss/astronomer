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
      }),
    ).toEqual({
      display_name: "Production",
      environment: "production",
      description: "",
      api_server_url: "https://api.example.test:6443",
      ca_certificate: "public-ca",
    });
  });
});
