import { render, screen } from "@testing-library/react";
import { InstallationDestinationView } from "./installation-destination";

it("keeps exact cluster, project, namespaces and multi-release ownership visible", () => {
  render(
    <InstallationDestinationView
      clusterId="cluster-123"
      clusterName="Production West"
      projectId="project-456"
      projectName="Operations"
      manager="Tools"
      releases={[
        { namespace: "mesh-system", releaseName: "base", version: "1.2.3" },
        { namespace: "mesh-gateway", releaseName: "gateway", version: "1.2.3" },
      ]}
    />,
  );
  const summary = screen.getByRole("region", {
    name: "Installation destination",
  });
  for (const text of [
    "Production West",
    "cluster-123",
    "Operations",
    "project-456",
    "mesh-system",
    "mesh-gateway",
    "1.2.3",
    "Managed through Tools",
    "entire cluster",
  ])
    expect(summary).toHaveTextContent(text);
});

it("does not invent a cluster or project when context is missing", () => {
  render(
    <InstallationDestinationView
      clusterId=""
      manager="Applications"
      releases={[]}
    />,
  );
  expect(screen.getByText(/Select a target cluster/)).toBeVisible();
});
