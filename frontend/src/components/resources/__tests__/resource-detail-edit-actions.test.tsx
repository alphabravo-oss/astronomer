import { fireEvent, render, screen } from "@testing-library/react";
import { describe, expect, it, vi } from "vitest";

import { ResourceDetailEditActions } from "@/components/resources/resource-detail-edit-actions";

vi.mock("@/lib/permission-hooks", () => ({
  useClusterResourcePermission: () => ({ allowed: true }),
}));

describe("resource detail edit modes", () => {
  it("selects YAML explicitly for the live ConfigMap", () => {
    const onEdit = vi.fn();
    render(
      <ResourceDetailEditActions
        clusterId="cluster"
        resourceType="configmaps"
        kind="ConfigMap"
        name="live-config"
        k8sPath="/api/v1/namespaces/default/configmaps/live-config"
        updateAllowed
        onEdit={onEdit}
      />,
    );
    fireEvent.click(screen.getByRole("button", { name: "Edit YAML" }));
    expect(onEdit).toHaveBeenLastCalledWith("yaml");
    expect(onEdit).toHaveBeenCalledTimes(1);
  });

  it("keeps YAML distinct from the supported Deployment guided action", () => {
    const onEdit = vi.fn();
    render(
      <ResourceDetailEditActions
        clusterId="cluster"
        resourceType="deployments"
        kind="Deployment"
        name="deployment"
        k8sPath="/apis/apps/v1/namespaces/default/deployments/deployment"
        updateAllowed
        onEdit={onEdit}
      />,
    );
    fireEvent.click(screen.getByRole("button", { name: "Edit as form" }));
    expect(onEdit).toHaveBeenLastCalledWith("guided");
    fireEvent.click(screen.getByRole("button", { name: "Edit YAML" }));
    expect(onEdit).toHaveBeenLastCalledWith("yaml");
    expect(onEdit).toHaveBeenCalledTimes(2);
  });
});
