import { fireEvent, render, screen } from "@testing-library/react";
import { CatalogPresets } from "./catalog-presets";
import { parseHelmValuesYAML } from "@/lib/helm-values-schema";

it("reviews only changed paths and applies a preset without disclosing secrets or erasing custom values", () => {
  const onChange = vi.fn();
  render(
    <CatalogPresets
      configuration={{
        schema: {},
        namespace: "monitoring",
        defaultPreset: "small",
        presets: [
          {
            id: "small",
            label: "Small",
            description: "One replica",
            values: { replicas: 1 },
          },
        ],
      }}
      valuesYaml={
        "replicas: 3\nadmin:\n  existingSecret: keep-this-reference\nunknown: keep\n"
      }
      onChange={onChange}
    />,
  );
  fireEvent.change(screen.getByLabelText("Configuration preset"), {
    target: { value: "small" },
  });
  expect(onChange).not.toHaveBeenCalled();
  expect(screen.getByText("replicas")).toBeVisible();
  expect(screen.queryByText("keep-this-reference")).toBeNull();
  fireEvent.click(screen.getByRole("button", { name: "Apply preset" }));
  expect(parseHelmValuesYAML(onChange.mock.calls[0][0])).toEqual({
    replicas: 1,
    admin: { existingSecret: "keep-this-reference" },
    unknown: "keep",
  });
});

it("keeps malformed YAML untouched and disables preset application", () => {
  const onChange = vi.fn();
  render(
    <CatalogPresets
      configuration={{
        schema: {},
        namespace: "monitoring",
        defaultPreset: "small",
        presets: [
          {
            id: "small",
            label: "Small",
            description: "One replica",
            values: { replicas: 1 },
          },
        ],
      }}
      valuesYaml="broken: ["
      onChange={onChange}
    />,
  );
  fireEvent.change(screen.getByLabelText("Configuration preset"), {
    target: { value: "small" },
  });
  expect(screen.getByRole("button", { name: "Apply preset" })).toBeDisabled();
  expect(onChange).not.toHaveBeenCalled();
});
