/**
 * Component-level coverage for the typed-Secret picker (plan 024 step 6):
 * selecting a built-in Kubernetes secret type must stamp `type` and render
 * that type's fixed fields instead of the Opaque freeform textarea.
 */
import { useState } from "react";
import { fireEvent, render, screen } from "@testing-library/react";

import { SecretSection } from "@/components/resources/guided-resource-access-sections";
import type { GuidedFormState } from "@/components/resources/guided-resource-fields";
import {
  containerPath,
  podSpecPath,
  updateManifest,
  validateGuidedResource,
  type KubernetesManifest,
  type ManifestPath,
} from "@/components/resources/guided-resource-model";

function Harness({
  onManifestChange,
}: {
  onManifestChange: (manifest: KubernetesManifest) => void;
}) {
  const [value, setValue] = useState<KubernetesManifest>({
    kind: "Secret",
    metadata: { name: "creds" },
  });
  const apply = (next: KubernetesManifest) => {
    setValue(next);
    onManifestChange(next);
  };
  const form: GuidedFormState = {
    value,
    kind: "Secret",
    podPath: podSpecPath("Secret"),
    containerPath: containerPath("Secret"),
    errors: validateGuidedResource(value),
    identityReadOnly: false,
    onChange: apply,
    doc: () => "",
    set: (path: ManifestPath, next: unknown) => apply(updateManifest(value, path, next)),
    setNumber: (path: ManifestPath, next: string) =>
      apply(updateManifest(value, path, next === "" ? undefined : Number(next))),
  };
  return <SecretSection form={form} />;
}

describe("SecretSection typed secrets", () => {
  it("selecting TLS renders both fixed fields and stamps type on the manifest", () => {
    let manifest: KubernetesManifest = {};
    render(<Harness onManifestChange={(next) => (manifest = next)} />);

    fireEvent.change(screen.getByLabelText("Type"), {
      target: { value: "kubernetes.io/tls" },
    });
    expect(manifest.type).toBe("kubernetes.io/tls");

    fireEvent.change(screen.getByLabelText("Certificate (tls.crt)"), {
      target: { value: "-----BEGIN CERTIFICATE-----" },
    });
    fireEvent.change(screen.getByLabelText("Private key (tls.key)"), {
      target: { value: "-----BEGIN PRIVATE KEY-----" },
    });

    expect(manifest).toMatchObject({
      type: "kubernetes.io/tls",
      stringData: {
        "tls.crt": "-----BEGIN CERTIFICATE-----",
        "tls.key": "-----BEGIN PRIVATE KEY-----",
      },
    });
    // The Opaque freeform textarea is gone once a type is picked.
    expect(screen.queryByLabelText("Values")).not.toBeInTheDocument();
  });

  it("selecting basic-auth renders username/password fields", () => {
    let manifest: KubernetesManifest = {};
    render(<Harness onManifestChange={(next) => (manifest = next)} />);

    fireEvent.change(screen.getByLabelText("Type"), {
      target: { value: "kubernetes.io/basic-auth" },
    });
    fireEvent.change(screen.getByLabelText("Username"), {
      target: { value: "admin" },
    });
    fireEvent.change(screen.getByLabelText("Password"), {
      target: { value: "hunter2" },
    });

    expect(manifest).toMatchObject({
      type: "kubernetes.io/basic-auth",
      stringData: { username: "admin", password: "hunter2" },
    });
  });

  it("defaults to Opaque with the freeform textarea", () => {
    render(<Harness onManifestChange={() => {}} />);
    expect(
      screen.getByLabelText("Values", { exact: false }),
    ).toBeInTheDocument();
  });
});
