import { ToolInstallPage } from "@/components/clusters/tool-install-page";
import { tool } from "./configuration-tool";
import { useState } from "react";
import { createRoot } from "react-dom/client";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { AppInstallPage } from "@/components/clusters/app-install-page";
import { ActionButton } from "@/components/ui/action-button";
import api from "@/lib/api/transport";
import profile from "./loki-configuration.json";
import "./styles.css";

const client = new QueryClient({
  defaultOptions: { queries: { retry: false } },
});
const page = (data: unknown[]) => ({
  data,
  pagination: {
    limit: 25,
    offset: 0,
    total: data.length,
    has_more: false,
    next_offset: null,
  },
});
const version = (id: string, number: string) => ({
  id,
  chart_id: "chart-loki",
  version: number,
  app_version: "3.6.12",
  created_at: "2026-10-12T00:00:00Z",
  ...(id === "reviewed"
    ? { catalog_version: { ...profile, deprecated: false, revoked: false } }
    : {}),
});
// Synthetic API adapter: production UI and query hooks, zero network mutations.
api.defaults.adapter = async (config) => {
  const url = config.url ?? "";
  let data: unknown;
  if (url.includes("/tools/") && url.endsWith("/preview/"))
    data = {
      data: {
        preset: "development",
        checks: [],
        charts: [
          {
            chart_name: "base",
            release_name: "istio-base",
            namespace: "istio-system",
            chart_version: "1.31.0",
            values_yaml: "defaultRevision: default\n",
          },
          {
            chart_name: "istiod",
            release_name: "istiod",
            namespace: "istio-system",
            chart_version: "1.31.0",
            values_yaml: "replicaCount: 1\nautoscaleEnabled: false\n",
          },
        ],
      },
    };
  else if (url.includes("/versions/"))
    data = page(
      config.params?.view === "all"
        ? [version("reviewed", "7.3.0"), version("upstream", "7.4.0")]
        : [version("reviewed", "7.3.0")],
    );
  else if (url.includes("/values/"))
    data = {
      data: {
        chart: "loki",
        version: config.params?.version,
        default_values:
          "custom:\n  preserved: yes\nsingleBinary:\n  replicas: 1\nadmin:\n  existingSecret: log-credentials\n",
        values_schema: {
          type: "object",
          properties: {
            singleBinary: {
              type: "object",
              properties: { replicas: { type: "integer", minimum: 1 } },
            },
          },
        },
      },
    };
  else if (url.includes("/resources/"))
    data = page([{ name: "fast-storage" }, { name: "standard" }]);
  else if (url.includes("/applications/preview/"))
    data = {
      data: {
        allowed: true,
        checks: [
          {
            code: "fixture",
            status: "advisory",
            title: "Fixture preview",
            description: "Synthetic preview; no cluster installation.",
          },
        ],
        application: "loki",
      },
    };
  else if (url.includes("/applications/")) data = { data: [] };
  else if (url.includes("/charts/chart-loki/"))
    data = {
      data: {
        id: "chart-loki",
        name: "loki",
        repository_id: "grafana",
        created_at: "2026-10-12T00:00:00Z",
        updated_at: "2026-10-12T00:00:00Z",
      },
    };
  else if (url.includes("/repositories/"))
    data = { data: { url: "https://grafana.github.io/helm-charts" } };
  else if (url.includes("/clusters/"))
    data = {
      data: {
        id: "cluster-demo",
        name: "Development",
        display_name: "Development",
        status: "connected",
      },
    };
  else if (url.includes("/projects/"))
    data = { data: { id: "project-demo", name: "Observability" } };
  else throw new Error(`Fixture has no handler: ${url}`);
  return { data, status: 200, statusText: "OK", headers: {}, config };
};
function Fixture() {
  const [open, setOpen] = useState(true);
  const [confirmed, setConfirmed] = useState(false);
  if (new URLSearchParams(location.search).has("tool"))
    return (
      <main className="mx-auto max-w-7xl p-4 sm:p-8">
        <p className="mb-4">
          Synthetic Platform Tool preview. Confirmation records a local fixture
          event only; no installation occurs.
        </p>
        {confirmed ? (
          <p role="status">Fixture confirmation recorded.</p>
        ) : (
          <ToolInstallPage
            tool={tool}
            clusterId="cluster-demo"
            preset="development"
            onClose={() => {
              location.href = "configuration.html";
            }}
            onConfirm={() => setConfirmed(true)}
          />
        )}
      </main>
    );
  return (
    <main className="mx-auto max-w-7xl p-4 sm:p-8">
      {!open && <h1>Catalog configuration fixture</h1>}
      <p className="mb-4">
        Synthetic API fixture using the real application configuration page. No
        installation occurs.
      </p>
      {!open && (
        <ActionButton onClick={() => setOpen(true)}>
          Configure Loki
        </ActionButton>
      )}
      {open && (
        <AppInstallPage
          projectId="project-demo"
          clusterId="cluster-demo"
          mode={{ kind: "install", chartId: "chart-loki", chartName: "loki" }}
          onClose={() => setOpen(false)}
          submitDecision={
            {
              allowed: false,
              reason: "Browser fixture: installation disabled",
            } as never
          }
        />
      )}
    </main>
  );
}
createRoot(document.getElementById("root")!).render(
  <QueryClientProvider client={client}>
    <Fixture />
  </QueryClientProvider>,
);
