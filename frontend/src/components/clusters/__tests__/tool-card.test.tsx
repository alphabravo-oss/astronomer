import { fireEvent, render, screen, within } from "@testing-library/react";
import { ToolCard } from "@/components/clusters/tool-card";
import type { ClusterTool, ClusterToolStatus } from "@/types";

const tool: ClusterTool = {
  id: "tool-1",
  icon: "shield",
  category: "security",
  versionConstraint: "1.0.0",
  defaultNamespace: "trivy-system",
  isBuiltin: true,
  isEnabled: true,
  presets: {},
  serviceName: "",
  servicePort: null,
  servicePath: "",
  subServices: [],
  createdAt: "2026-09-10",
  updatedAt: "2026-09-10",
  slug: "trivy-operator",
  name: "Trivy Operator",
  description: "Scanner",
  charts: [],
};

// The preset (development/staging/production) is an install-time chart-values
// choice and belongs in the install dialog. It used to render on every card,
// where it read as a per-tool environment switch — and appeared next to tools
// that were not installed at all. The card must stay free of it.
describe("ToolCard", () => {
  it("shows releases in execution order", () => {
    render(
      <ToolCard
        tool={{
          ...tool,
          name: "Istio",
          charts: [
            {
              chartName: "istiod",
              releaseName: "istiod",
              repoUrl: "https://example.com",
              namespace: "istio-system",
              order: 1,
              version: "1.31.0",
            },
            {
              chartName: "base",
              releaseName: "istio-base",
              repoUrl: "https://example.com",
              namespace: "istio-system",
              order: 0,
              version: "1.31.0",
            },
          ],
        }}
        onInstall={vi.fn()}
        onUninstall={vi.fn()}
        onAdopt={vi.fn()}
      />,
    );
    const releases = within(
      screen.getByRole("list", { name: "Istio releases" }),
    ).getAllByRole("listitem");
    expect(releases[0]).toHaveTextContent("1. istio-base · 1.31.0");
    expect(releases[1]).toHaveTextContent("2. istiod · 1.31.0");
  });

  it("retries the durable operation and offers rollback without starting a new install", () => {
    const onRecover = vi.fn();
    const onInstall = vi.fn();
    const toolStatus: ClusterToolStatus = {
      slug: tool.slug,
      name: tool.name,
      status: "failed",
      releaseName: null,
      namespace: null,
      presetUsed: null,
      error: "second release failed",
      operation: {
        id: "op-1",
        targetType: "tool_installation",
        targetKey: "cluster:istio",
        operationType: "install",
        status: "failed",
        attemptCount: 1,
        errorMessage: "failed",
        createdAt: "2026-09-10",
        startedAt: null,
        completedAt: null,
        updatedAt: "2026-09-10",
      },
    };
    render(
      <ToolCard
        tool={tool}
        toolStatus={toolStatus}
        onInstall={onInstall}
        onRecover={onRecover}
        onUninstall={vi.fn()}
        onAdopt={vi.fn()}
      />,
    );
    fireEvent.click(screen.getByRole("button", { name: "Retry" }));
    fireEvent.click(screen.getByRole("button", { name: "Roll back" }));
    expect(onRecover.mock.calls).toEqual([
      [tool.slug, "retry"],
      [tool.slug, "rollback"],
    ]);
    expect(onInstall).not.toHaveBeenCalled();
  });
  it("renders no preset dropdown", () => {
    render(
      <ToolCard
        tool={tool}
        onInstall={() => {}}
        onUninstall={() => {}}
        onAdopt={() => {}}
      />,
    );
    expect(screen.queryByRole("combobox")).toBeNull();
    expect(screen.queryByText(/staging/i)).toBeNull();
  });

  it("asks to install by slug alone, leaving the preset to the dialog", () => {
    const onInstall = vi.fn();
    render(
      <ToolCard
        tool={tool}
        onInstall={onInstall}
        onUninstall={() => {}}
        onAdopt={() => {}}
      />,
    );
    fireEvent.click(screen.getByRole("button", { name: /enable/i }));
    expect(onInstall).toHaveBeenCalledWith("trivy-operator");
  });
});
