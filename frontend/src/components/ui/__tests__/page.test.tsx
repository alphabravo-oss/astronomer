import { render, screen } from "@testing-library/react";
import {
  PageEyebrowProvider,
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
    expect(container.firstChild).toHaveClass("space-y-(--gap-section)");
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

describe("PageHeader slots", () => {
  it("renders status beside the title and tabs as a nav (never a tablist)", () => {
    render(
      <PageHeader
        title="Project"
        status={<span>Healthy</span>}
        tabs={
          <nav aria-label="Sections">
            <a href="/a">Overview</a>
          </nav>
        }
      />,
    );
    expect(screen.getByRole("heading", { level: 1 })).toHaveTextContent(
      "Project",
    );
    expect(screen.getByText("Healthy")).toBeInTheDocument();
    expect(screen.getByRole("navigation", { name: "Sections" })).toBeVisible();
    expect(screen.queryByRole("tablist")).toBeNull();
  });

  it("shows the layout-provided scope eyebrow unless the page sets its own", () => {
    const { rerender } = render(
      <PageEyebrowProvider value="Continuous Delivery · Project scope">
        <PageHeader title="Sources" />
      </PageEyebrowProvider>,
    );
    expect(
      screen.getByText("Continuous Delivery · Project scope"),
    ).toBeInTheDocument();
    rerender(
      <PageEyebrowProvider value="Continuous Delivery · Project scope">
        <PageHeader title="Sources" eyebrow="Own" />
      </PageEyebrowProvider>,
    );
    expect(screen.getByText("Own")).toBeInTheDocument();
    expect(
      screen.queryByText("Continuous Delivery · Project scope"),
    ).toBeNull();
  });
});
