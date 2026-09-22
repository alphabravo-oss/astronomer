/**
 * Row-action coverage for the workload action menu (plan 024 step 3):
 * "Clone" and "Download YAML" must be present alongside the existing
 * View/Edit YAML, Scale, Restart and Delete actions, gated by the read/
 * create permission decisions the way every other action is.
 */
import { fireEvent, render, screen } from "@testing-library/react";

vi.mock("@tanstack/react-router", async (importOriginal) => {
  const original =
    await importOriginal<typeof import("@tanstack/react-router")>();
  return {
    ...original,
    useParams: () => ({ id: "cluster-1" }),
  };
});

import { WorkloadActions } from "@/components/resources/resource-list-page";
import type { ResourcePermissionDecisions } from "@/components/resources/resource-action-policy";
import type { PermissionDecision } from "@/lib/permissions";
import type { Workload } from "@/types";

function decision(overrides: Partial<PermissionDecision> = {}): PermissionDecision {
  return {
    allowed: true,
    permission: "workloads:read",
    scope: { type: "cluster", id: "cluster-1" },
    scopeLabel: "cluster",
    reason: "",
    grantedBy: [],
    requestAccessHint: "",
    ...overrides,
  };
}

const allow = decision();
const permissions: ResourcePermissionDecisions = {
  create: allow,
  read: allow,
  update: allow,
  delete: allow,
  scale: allow,
  restart: allow,
  exec: allow,
  logs: allow,
  manage: allow,
};

const row: Workload = {
  kind: "Deployment",
  name: "api",
  namespace: "production",
  clusterId: "cluster-1",
  clusterName: "cluster-1",
  status: "Running",
  ready: "3/3",
  upToDate: 3,
  available: 3,
  replicas: 3,
  desiredReplicas: 3,
  images: ["nginx:1.25"],
  labels: {},
  annotations: {},
  createdAt: "2026-01-01T00:00:00Z",
  age: "2d",
};

function renderActions(overrides: Partial<ResourcePermissionDecisions> = {}) {
  render(
    <WorkloadActions
      row={row}
      permissions={{ ...permissions, ...overrides }}
      podPermissions={permissions}
      onOpenStream={() => {}}
      onYaml={() => {}}
      onScale={() => {}}
      onRestart={() => {}}
      onDelete={() => {}}
    />,
  );
  fireEvent.click(screen.getByLabelText("Open actions menu"));
}

describe("WorkloadActions row menu", () => {
  it("includes Clone and Download YAML alongside the existing actions", () => {
    renderActions();
    expect(screen.getByRole("menuitem", { name: /clone/i })).toBeEnabled();
    expect(
      screen.getByRole("menuitem", { name: /download yaml/i }),
    ).toBeEnabled();
    expect(screen.getByRole("menuitem", { name: /view yaml/i })).toBeEnabled();
    expect(screen.getByRole("menuitem", { name: /edit yaml/i })).toBeEnabled();
    expect(screen.getByRole("menuitem", { name: /scale/i })).toBeEnabled();
    expect(screen.getByRole("menuitem", { name: /restart/i })).toBeEnabled();
    expect(screen.getByRole("menuitem", { name: /delete/i })).toBeEnabled();
  });

  it("disables Download YAML without read permission", () => {
    renderActions({ read: decision({ allowed: false, reason: "No read access" }) });
    expect(
      screen.getByRole("menuitem", { name: /download yaml/i }),
    ).toBeDisabled();
  });

  it("disables Clone without create permission", () => {
    renderActions({
      create: decision({ allowed: false, reason: "No create access" }),
    });
    expect(screen.getByRole("menuitem", { name: /clone/i })).toBeDisabled();
  });
});
