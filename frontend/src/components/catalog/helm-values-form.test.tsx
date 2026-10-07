import { useState } from "react";
import { fireEvent, render, screen } from "@testing-library/react";
import { HelmValuesForm } from "@/components/catalog/helm-values-form";
import type {
  HelmValuesObject,
  HelmValuesSchemaNode,
} from "@/lib/helm-values-schema";

function ControlledForm({ schema }: { schema: HelmValuesSchemaNode }) {
  const [value, setValue] = useState<HelmValuesObject>({
    ingress: { enabled: false, host: "apps.example.test" },
    replicas: 1,
  });
  return (
    <>
      <HelmValuesForm schema={schema} value={value} onChange={setValue} />
      <output data-testid="values">{JSON.stringify(value)}</output>
    </>
  );
}

it("applies Rancher-style conditional questions and groups", () => {
  render(
    <ControlledForm
      schema={{
        type: "object",
        properties: {
          ingress: {
            type: "object",
            properties: {
              enabled: {
                type: "boolean",
                title: "Enable ingress",
                "x-astronomer-group": "Networking",
              },
              host: {
                type: "string",
                title: "Ingress host",
                "x-astronomer-show-when": {
                  path: "ingress.enabled",
                  equals: "true",
                },
              },
            },
          },
          replicas: {
            type: "integer",
            title: "Replicas",
            enum: [1, 3, 5],
          },
        },
      }}
    />,
  );
  expect(screen.getByText("Networking")).toBeVisible();
  expect(
    screen.queryByRole("textbox", { name: "Ingress host" }),
  ).not.toBeInTheDocument();
  fireEvent.click(screen.getByRole("checkbox", { name: "Enable ingress" }));
  expect(screen.getByRole("textbox", { name: "Ingress host" })).toHaveValue(
    "apps.example.test",
  );
  fireEvent.change(screen.getByRole("combobox", { name: "Replicas" }), {
    target: { value: "3" },
  });
  expect(screen.getByTestId("values")).toHaveTextContent('"replicas":3');
});
