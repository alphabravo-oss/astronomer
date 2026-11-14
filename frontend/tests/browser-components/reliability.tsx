import { useState } from "react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { WidgetGrid } from "@/components/dashboards/widget-grid";
import { ToolCatalogFields } from "@/components/clusters/tool-catalog-fields";
import {
  dumpHelmValuesYAML,
  parseHelmValuesYAML,
  type HelmValuesObject,
} from "@/lib/helm-values-schema";
import { mergeCatalogValues } from "@/lib/catalog-configuration";
import type { ToolPreviewResponse } from "@/types";

const base = {
  annotations: { "example.com/key": "old", example: { "com/key": "nested" } },
  custom: { untouched: true },
  hosts: ["one", "two"],
};
const charts: ToolPreviewResponse["charts"] = [
  {
    chartName: "fixture",
    chartVersion: "1",
    releaseName: "fixture",
    namespace: "apps",
    valuesYaml: dumpHelmValuesYAML(base),
    catalogVersion: {
      version: "1",
      available: true,
      recommended: true,
      revoked: false,
      deprecated: false,
      supportTier: "upstream",
      configuration: {
        namespace: "apps",
        presets: [],
        defaultPreset: "",
        schema: {
          type: "object",
          properties: {
            annotations: {
              type: "object",
              properties: {
                "example.com/key": {
                  type: "string",
                  title: "Literal annotation",
                },
                example: {
                  type: "object",
                  properties: {
                    "com/key": { type: "string", title: "Nested annotation" },
                  },
                },
              },
            },
          },
        },
      },
    },
  },
];
const client = new QueryClient({
  defaultOptions: { queries: { retry: false } },
});
export function ReliabilityFixture() {
  const [scope, setScope] = useState("empty");
  const [requests, setRequests] = useState(0);
  const [overrides, setOverrides] = useState<HelmValuesObject>({});
  const [yaml, setYaml] = useState<string | null>(null);
  const [submitted, setSubmitted] = useState("");
  const values = mergeCatalogValues(base, overrides);
  return (
    <QueryClientProvider client={client}>
      <main>
        <button onClick={() => setScope("a")}>Scope A</button>
        <button onClick={() => setScope("b")}>Scope B</button>
        <output aria-label="Requests">{requests}</output>
        <WidgetGrid
          scope={["browser", scope]}
          fetcher={async () => {
            setRequests((count) => count + 1);
            return scope === "empty"
              ? []
              : [1, 2].map((id) => ({
                  id: `${scope}-${id}`,
                  name: `Scope ${scope} widget ${id}`,
                  widgetType: "prom_stat",
                  specResolved: {},
                  grid: { x: 0, y: 0, w: 4, h: 2 },
                  refreshSeconds: 5,
                }));
          }}
        />
        {yaml === null ? (
          <>
            <ToolCatalogFields
              charts={charts}
              clusterId="fixture"
              overrides={overrides}
              onChange={setOverrides}
            />
            <button onClick={() => setYaml(dumpHelmValuesYAML(values))}>
              Edit YAML
            </button>
          </>
        ) : (
          <>
            <textarea
              aria-label="Values YAML"
              value={yaml}
              onChange={(e) => setYaml(e.target.value)}
            />
            <button
              onClick={() => {
                setOverrides(parseHelmValuesYAML(yaml) ?? {});
                setYaml(null);
              }}
            >
              Use form
            </button>
          </>
        )}
        <button onClick={() => setSubmitted(dumpHelmValuesYAML(values))}>
          Submit values
        </button>
        <output aria-label="Submitted values">{submitted}</output>
      </main>
    </QueryClientProvider>
  );
}
