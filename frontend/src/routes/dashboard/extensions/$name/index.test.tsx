import { render, screen } from "@testing-library/react";
import type { MockedFunction } from "vitest";
import { emptyRegistry } from "@/lib/extensions/registry";
import type { ExtensionMount } from "@/lib/api/extensions";

const paramsState = vi.hoisted(() => ({ name: "cost" }));

vi.mock("@tanstack/react-router", async (importOriginal) => {
  const { RouterLinkStub } = await import("@/test/router-link");
  return {
    ...(await importOriginal<typeof import("@tanstack/react-router")>()),
    Link: RouterLinkStub,
    getRouteApi: () => ({ useParams: () => paramsState }),
    // The real `createFileRoute(...)(...)` needs a mounted router to back
    // `Route.useParams()`. This test drives the page component directly, so
    // stub it down to just the piece the component actually reads.
    createFileRoute: () => (routeOptions: Record<string, unknown>) => ({
      useParams: () => paramsState,
      options: routeOptions,
    }),
  };
});

vi.mock("@/components/extensions/ExtensionProvider", () => ({
  __esModule: true,
  useExtensionMounts: vi.fn(),
  useExtensionRuntime: vi.fn(),
}));

vi.mock("@/components/extensions/SandboxedExtension", () => ({
  SandboxedExtension: ({ mount }: { mount: ExtensionMount }) => (
    <div data-testid="sandboxed">{mount.extension}</div>
  ),
}));

import {
  useExtensionMounts,
  useExtensionRuntime,
} from "@/components/extensions/ExtensionProvider";
import { ExtensionPage } from "./-page";

const mockedMounts = useExtensionMounts as MockedFunction<
  typeof useExtensionMounts
>;
const mockedRuntime = useExtensionRuntime as MockedFunction<
  typeof useExtensionRuntime
>;

function mount(over: Partial<ExtensionMount> = {}): ExtensionMount {
  return {
    extension: "cost",
    displayName: "Cost Insights",
    point: "sidebar",
    pointId: "cost",
    tier: 1,
    render: { declarative: { kind: "table", dataSource: "d1" } },
    label: "Cost",
    path: "/whatever",
    ...over,
  };
}

describe("extensions/$name page", () => {
  afterEach(() => vi.clearAllMocks());

  it("shows a loading state while the registry is still loading", () => {
    mockedRuntime.mockReturnValue({
      registry: emptyRegistry(),
      isLoading: true,
      isError: false,
    });
    mockedMounts.mockReturnValue([]);
    render(<ExtensionPage />);
    expect(screen.getByText(/loading extension/i)).toBeInTheDocument();
  });

  it("shows an inline not-available panel (not a thrown notFound) when no sidebar mount matches the name", () => {
    mockedRuntime.mockReturnValue({
      registry: emptyRegistry(),
      isLoading: false,
      isError: false,
    });
    mockedMounts.mockReturnValue([]);
    render(<ExtensionPage />);
    expect(screen.getByText(/not available/i)).toBeInTheDocument();
  });

  it("renders the declarative (tier 1) widget for a matching mount", () => {
    mockedRuntime.mockReturnValue({
      registry: { ...emptyRegistry(), sidebar: [mount()] },
      isLoading: false,
      isError: false,
    });
    mockedMounts.mockReturnValue([mount()]);
    render(<ExtensionPage />);
    expect(screen.getByRole("heading", { name: "Cost" })).toBeInTheDocument();
  });

  it("renders the sandboxed (tier 2) iframe wrapper for a bundle mount", () => {
    const bundleMount = mount({
      render: {
        bundle: {
          url: "https://ext.example/app.js",
          sha256: "deadbeef",
          integrity: "sha256-deadbeef",
          entry: "https://ext.example/app.js",
          sandboxOrigin: "https://ext.example",
          component: "App",
        },
      },
      tier: 2,
    });
    mockedRuntime.mockReturnValue({
      registry: { ...emptyRegistry(), sidebar: [bundleMount] },
      isLoading: false,
      isError: false,
    });
    mockedMounts.mockReturnValue([bundleMount]);
    render(<ExtensionPage />);
    expect(screen.getByTestId("sandboxed")).toHaveTextContent("cost");
  });
});
