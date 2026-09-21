import { render, screen } from "@testing-library/react";
import {
  PageHeader,
  PageSection,
  PageShell,
  ResourceMasthead,
} from "@/components/ui/page";

vi.mock("@tanstack/react-router", async (importOriginal) => {
  const { RouterLinkStub } = await import("@/test/router-link");
  return {
    ...(await importOriginal<typeof import("@tanstack/react-router")>()),
    Link: RouterLinkStub,
  };
});

describe("Page layout primitives", () => {
  it("renders a page shell with standard spacing", () => {
    const { container } = render(
      <PageShell>
        <div>Content</div>
      </PageShell>,
    );

    expect(screen.getByText("Content")).toBeInTheDocument();
    expect(container.firstChild).toHaveClass("space-y-6");
  });

  it("renders title, description, eyebrow, and actions", () => {
    render(
      <PageHeader
        eyebrow="Operations"
        title="Backups"
        description="Restore points and storage targets."
        actions={<button>Create</button>}
      />,
    );

    expect(screen.getByText("Operations")).toBeInTheDocument();
    expect(
      screen.getByRole("heading", { name: "Backups" }),
    ).toBeInTheDocument();
    expect(
      screen.getByText("Restore points and storage targets."),
    ).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "Create" })).toBeInTheDocument();
  });

  it("renders an unframed page section with optional heading actions", () => {
    render(
      <PageSection
        title="Instances"
        description="Registered control planes."
        actions={<button>Refresh</button>}
      >
        <div>Table</div>
      </PageSection>,
    );

    expect(
      screen.getByRole("heading", { name: "Instances" }),
    ).toBeInTheDocument();
    expect(screen.getByText("Registered control planes.")).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "Refresh" })).toBeInTheDocument();
    expect(screen.getByText("Table")).toBeInTheDocument();
  });

  it("renders a resource masthead with back link, status, meta, and actions", () => {
    render(
      <ResourceMasthead
        backTo="/dashboard/clusters/c-1"
        backLabel="Back to cluster"
        eyebrow="Deployment"
        title="astronomer-agent"
        mono
        status={<span>Healthy</span>}
        meta={[
          { label: "Namespace", value: "astronomer" },
          { label: "Age", value: "3d" },
        ]}
        actions={<button>Restart</button>}
        description="Runs the in-cluster control-plane agent."
      />,
    );

    const heading = screen.getByRole("heading", { name: "astronomer-agent" });
    expect(heading.tagName).toBe("H1");
    expect(heading).toHaveClass("font-mono");
    expect(screen.getByText("Healthy")).toBeInTheDocument();
    expect(screen.getByText("Namespace: astronomer")).toBeInTheDocument();
    expect(screen.getByText("Age: 3d")).toBeInTheDocument();
    expect(
      screen.getByText("Runs the in-cluster control-plane agent."),
    ).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "Restart" })).toBeInTheDocument();
    expect(
      screen.getByRole("link", { name: "Back to cluster" }),
    ).toHaveAttribute("href", "/dashboard/clusters/c-1");
  });
});
