/**
 * Component-level coverage for the probe editor (plan 024 step 6): switching
 * to a typed probe writes the full probe object, and a missing port renders
 * the inline error `validateGuidedResource` produces for that probe key.
 */
import { useState } from "react";
import { fireEvent, render, screen } from "@testing-library/react";

import { ProbeFields } from "@/components/resources/probe-fields";
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
    kind: "Deployment",
    metadata: { name: "web" },
    spec: { template: { spec: { containers: [{ image: "nginx" }] } } },
  });
  const apply = (next: KubernetesManifest) => {
    setValue(next);
    onManifestChange(next);
  };
  const form: GuidedFormState = {
    value,
    kind: "Deployment",
    podPath: podSpecPath("Deployment"),
    containerPath: containerPath("Deployment"),
    errors: validateGuidedResource(value),
    identityReadOnly: false,
    onChange: apply,
    doc: () => "",
    set: (path: ManifestPath, next: unknown) =>
      apply(updateManifest(value, path, next)),
    setNumber: (path: ManifestPath, next: string) =>
      apply(
        updateManifest(value, path, next === "" ? undefined : Number(next)),
      ),
  };
  return <ProbeFields form={form} kind="readiness" />;
}

describe("ProbeFields", () => {
  it("selecting HTTP GET without a port is invalid; setting the port clears it", () => {
    let manifest: KubernetesManifest = {};
    render(<Harness onManifestChange={(next) => (manifest = next)} />);

    fireEvent.change(screen.getByLabelText("Type"), {
      target: { value: "httpGet" },
    });
    expect(validateGuidedResource(manifest)).toHaveProperty("readinessProbe");
    expect(screen.getByText(/port must be/i)).toBeInTheDocument();

    fireEvent.change(screen.getByLabelText("Port", { exact: false }), {
      target: { value: "8080" },
    });
    expect(validateGuidedResource(manifest)).not.toHaveProperty(
      "readinessProbe",
    );
  });

  it("selecting TCP socket writes a tcpSocket probe", () => {
    let manifest: KubernetesManifest = {};
    render(<Harness onManifestChange={(next) => (manifest = next)} />);

    fireEvent.change(screen.getByLabelText("Type"), {
      target: { value: "tcpSocket" },
    });
    fireEvent.change(screen.getByLabelText("Port", { exact: false }), {
      target: { value: "5432" },
    });

    expect(manifest).toMatchObject({
      spec: {
        template: {
          spec: {
            containers: [
              { readinessProbe: { tcpSocket: { port: "5432" } } },
            ],
          },
        },
      },
    });
  });

  it("selecting None clears the probe", () => {
    let manifest: KubernetesManifest = {};
    render(<Harness onManifestChange={(next) => (manifest = next)} />);
    fireEvent.change(screen.getByLabelText("Type"), {
      target: { value: "httpGet" },
    });
    fireEvent.change(screen.getByLabelText("Type"), {
      target: { value: "none" },
    });
    const container = (
      manifest.spec as Record<string, unknown>
    ) as { template?: { spec?: { containers?: Array<Record<string, unknown>> } } };
    expect(container.template?.spec?.containers?.[0]?.readinessProbe).toBeUndefined();
  });
});
