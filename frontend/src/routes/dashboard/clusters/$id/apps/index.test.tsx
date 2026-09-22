import { render, screen } from "@testing-library/react";
import type { ComponentProps } from "react";
import { describe, expect, it, vi } from "vitest";
import type { PermissionDecision } from "@/lib/permissions";
import type { PaginatedResponse } from "@/types";
import type { ClusterAppRow, RecommendedChart } from "@/lib/api/cluster-apps";
import { InstalledView, RecommendedView } from "./-page";

type InstalledQuery = ComponentProps<typeof InstalledView>["q"];
type RecommendedQuery = ComponentProps<typeof RecommendedView>["q"];

vi.mock("@tanstack/react-router", async (importOriginal) => {
  const { RouterLinkStub } = await import("@/test/router-link");
  return {
    ...(await importOriginal<typeof import("@tanstack/react-router")>()),
    Link: RouterLinkStub,
  };
});

const allowedDecision: PermissionDecision = {
  allowed: true,
  permission: "catalog:manage",
  scope: { type: "global" },
  scopeLabel: "global",
  reason: "",
  grantedBy: [],
  requestAccessHint: "",
};

function emptyInstalledPage(): PaginatedResponse<ClusterAppRow> {
  return {
    data: [],
    pagination: {
      total: 0,
      limit: 50,
      offset: 0,
      has_more: false,
      next_offset: null,
    },
  };
}

function installedQuery(overrides: {
  data?: PaginatedResponse<ClusterAppRow>;
  error?: unknown;
  isError?: boolean;
}): InstalledQuery {
  return {
    data: undefined,
    error: undefined,
    isError: false,
    isLoading: false,
    refetch: vi.fn(),
    ...overrides,
  } as unknown as InstalledQuery;
}

function recommendedQuery(overrides: {
  data?: RecommendedChart[];
  error?: unknown;
  isError?: boolean;
}): RecommendedQuery {
  return {
    data: undefined,
    error: undefined,
    isError: false,
    isLoading: false,
    refetch: vi.fn(),
    ...overrides,
  } as unknown as RecommendedQuery;
}

describe("InstalledView query states", () => {
  it("renders an error state and not the empty-state copy when the list query fails", () => {
    render(
      <InstalledView
        clusterId="cluster-1"
        q={installedQuery({ isError: true, error: new Error("boom") })}
        onUpgrade={vi.fn()}
        onUninstall={vi.fn()}
        onDeleteFailed={vi.fn()}
        updateDecision={allowedDecision}
        deleteDecision={allowedDecision}
      />,
    );
    expect(screen.getByRole("alert")).toBeInTheDocument();
    expect(screen.queryByText("No apps installed yet")).not.toBeInTheDocument();
  });

  it("still shows the empty-state copy when the query succeeds with zero rows", () => {
    render(
      <InstalledView
        clusterId="cluster-1"
        q={installedQuery({ data: emptyInstalledPage() })}
        onUpgrade={vi.fn()}
        onUninstall={vi.fn()}
        onDeleteFailed={vi.fn()}
        updateDecision={allowedDecision}
        deleteDecision={allowedDecision}
      />,
    );
    expect(screen.getByText("No apps installed yet")).toBeInTheDocument();
  });
});

describe("RecommendedView query states", () => {
  it("renders an error state and not the empty-state copy when the recommendations query fails", () => {
    render(
      <RecommendedView
        q={recommendedQuery({ isError: true, error: new Error("boom") })}
        installed={[]}
        installDecision={allowedDecision}
        onInstall={vi.fn()}
      />,
    );
    expect(screen.getByRole("alert")).toBeInTheDocument();
    expect(
      screen.queryByText("No recommendations yet"),
    ).not.toBeInTheDocument();
  });

  it("still shows the empty-state copy when the query succeeds with zero rows", () => {
    render(
      <RecommendedView
        q={recommendedQuery({ data: [] })}
        installed={[]}
        installDecision={allowedDecision}
        onInstall={vi.fn()}
      />,
    );
    expect(screen.getByText("No recommendations yet")).toBeInTheDocument();
  });
});
