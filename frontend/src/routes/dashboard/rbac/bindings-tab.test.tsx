import { fireEvent, render, screen } from "@testing-library/react";
import type { ComponentProps } from "react";
import { describe, expect, it, vi } from "vitest";

import { BindingsTab } from "./-bindings-tab";

const binding = {
  id: "binding-1",
  scope: "global",
  roleId: "role-1",
  userId: "user-1",
  createdAt: "2026-09-10T12:00:00Z",
};

function renderBindings(overrides: Partial<ComponentProps<typeof BindingsTab>> = {}) {
  const onRevoke = vi.fn();
  render(
    <BindingsTab
      bindings={[binding] as never}
      globalRoles={[{ id: "role-1", name: "Viewer" }] as never}
      clusterRoles={[]}
      projectRoles={[]}
      clusters={[]}
      projects={[]}
      users={[{ id: "user-1", username: "alex", email: "alex@example.test" }] as never}
      loading={false}
      isError={false}
      onRetry={vi.fn()}
      onRevoke={onRevoke}
      {...overrides}
    />,
  );
  return { onRevoke };
}

describe("BindingsTab", () => {
  it("renders a resolved binding and revokes the exact binding", () => {
    const { onRevoke } = renderBindings();
    expect(screen.getByText("alex")).toBeInTheDocument();
    expect(screen.getByText("Viewer")).toBeInTheDocument();
    fireEvent.click(screen.getByTitle("Revoke binding"));
    expect(onRevoke).toHaveBeenCalledWith(binding);
  });

  it("keeps permission failures distinct from an empty binding list", () => {
    renderBindings({ bindings: [], isError: true, error: { status: 403 } });
    expect(screen.getByText(/permission required/i)).toBeInTheDocument();
    expect(screen.queryByText("No role bindings found")).not.toBeInTheDocument();
  });
});
