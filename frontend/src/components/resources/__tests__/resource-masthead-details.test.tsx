import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import { beforeEach, describe, expect, it, vi } from "vitest";

import type { K8sObject } from "@/components/resources/resource-detail-model";
import {
  ConditionsStrip,
  MastheadDetails,
  PodContainerSummary,
} from "@/components/resources/resource-masthead-details";
import { ResourceMasthead } from "@/components/ui/page";
import { OverviewUnavailable } from "@/components/resources/overview-unavailable";

vi.mock("@tanstack/react-router", () => ({
  Link: ({ children, to }: { children: React.ReactNode; to: string }) => (
    <a href={to}>{children}</a>
  ),
}));

const toasts = vi.hoisted(() => ({ success: vi.fn(), error: vi.fn() }));
vi.mock("@/lib/toast", () => ({
  toastSuccess: toasts.success,
  toastError: toasts.error,
}));

const labels = Object.fromEntries(
  Array.from({ length: 8 }, (_, i) => [`k${i}`, `v${i}`]),
);

describe("MastheadDetails", () => {
  beforeEach(() => {
    vi.clearAllMocks();
    Object.assign(navigator, {
      clipboard: { writeText: vi.fn().mockResolvedValue(undefined) },
    });
  });

  it("collapses labels after six and expands on +N more", () => {
    render(<MastheadDetails isPod={false} obj={{ metadata: { labels } }} />);
    expect(screen.getAllByRole("button", { name: /^Copy k/ })).toHaveLength(6);
    fireEvent.click(screen.getByRole("button", { name: "+2 more" }));
    expect(screen.getAllByRole("button", { name: /^Copy k/ })).toHaveLength(8);
  });

  it("copies key=value on click", async () => {
    render(<MastheadDetails isPod={false} obj={{ metadata: { labels } }} />);
    fireEvent.click(screen.getByRole("button", { name: "Copy k0=v0" }));
    await waitFor(() =>
      expect(navigator.clipboard.writeText).toHaveBeenCalledWith("k0=v0"),
    );
    expect(toasts.success).toHaveBeenCalled();
  });

  it("renders nothing for chip groups without metadata", () => {
    const { container } = render(
      <MastheadDetails isPod={false} obj={{ metadata: {} }} />,
    );
    expect(container).toBeEmptyDOMElement();
  });
});

describe("ConditionsStrip", () => {
  it("renders one toned chip per condition", () => {
    render(
      <ConditionsStrip
        conditions={[
          { type: "Ready", status: "True" },
          { type: "DiskPressure", status: "True", reason: "Low" },
        ]}
      />,
    );
    const items = screen.getAllByRole("listitem");
    expect(items).toHaveLength(2);
    expect(items[0].querySelector("[data-tone]")).toHaveAttribute(
      "data-tone",
      "ok",
    );
    expect(items[1].querySelector("[data-tone]")).toHaveAttribute(
      "data-tone",
      "bad",
    );
  });
});

describe("PodContainerSummary", () => {
  it("shows ready/total, restarts and last termination", () => {
    const obj: K8sObject = {
      status: {
        containerStatuses: [
          {
            ready: false,
            restartCount: 4,
            lastState: { terminated: { reason: "OOMKilled", exitCode: 137 } },
          },
        ],
      },
    };
    render(<PodContainerSummary obj={obj} />);
    expect(screen.getByText("0/1")).toBeInTheDocument();
    expect(screen.getByText("4")).toBeInTheDocument();
    expect(screen.getByText("OOMKilled (exit 137)")).toBeInTheDocument();
  });
});

describe("ResourceMasthead loading", () => {
  it("shows skeletons, hides meta and details, marks aria-busy", () => {
    const { container } = render(
      <ResourceMasthead
        title="web"
        loading
        meta={[{ label: "Age", value: "3d" }]}
        details={<span>chips</span>}
      />,
    );
    expect(screen.queryByText("Age: 3d")).not.toBeInTheDocument();
    expect(screen.queryByText("chips")).not.toBeInTheDocument();
    expect(container.firstElementChild).toHaveAttribute("aria-busy", "true");
  });
});

describe("OverviewUnavailable", () => {
  it("is a typed empty state, not bare text", () => {
    render(<OverviewUnavailable resourceType="deployments" />);
    expect(screen.getByText("No overview available")).toBeInTheDocument();
    expect(screen.queryByText("No data.")).not.toBeInTheDocument();
  });
});
