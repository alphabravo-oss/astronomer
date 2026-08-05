import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import { describe, expect, it, vi } from "vitest";
import { CharlieShell } from "../charlie-shell";

vi.mock("@/lib/api/charlie", () => ({
  createCharlieSession: vi.fn(),
  getCharlieHistory: vi.fn(),
  searchCharlieContext: vi.fn().mockResolvedValue([
    { type: "alert", id: "active", requiredVerb: "read", label: "Alerts", summary: "Current authorized alert scope" },
  ]),
  sendCharlieMessage: vi.fn(),
  subscribeCharlieSessionEvents: vi.fn(),
}));

vi.mock("@/lib/navigation", () => ({
  usePathname: () => "/dashboard/alerting",
}));
vi.mock("@/lib/link", () => ({
  Link: ({
    href,
    children,
    ...props
  }: {
    href: string;
    children: React.ReactNode;
  }) => (
    <a href={href} {...props}>
      {children}
    </a>
  ),
}));

describe("Charlie global shell accessibility", () => {
  it("opens with the non-command-palette shortcut, exposes route context, and closes with Escape", async () => {
    const client = new QueryClient({
      defaultOptions: { queries: { retry: false } },
    });
    render(
      <QueryClientProvider client={client}>
        <CharlieShell>
          <main>Dashboard</main>
        </CharlieShell>
      </QueryClientProvider>,
    );
    const launcher = screen.getByRole("button", {
      name: "Open Charlie assistant",
    });
    expect(launcher).toHaveAttribute("aria-expanded", "false");
    launcher.focus();
    fireEvent.keyDown(window, { key: ".", ctrlKey: true, shiftKey: true });
    expect(
      await screen.findByRole("dialog", { name: "Charlie" }),
    ).toBeInTheDocument();
    expect(screen.getByText("Alerts")).toBeInTheDocument();
    expect(
      screen.getByRole("button", { name: "Remove Alerts" }),
    ).toBeInTheDocument();
    expect(screen.getByRole("listitem", { name: /Alerts: Current authorized/ })).toBeInTheDocument();
    expect(screen.getByRole("dialog")).toHaveClass("max-sm:max-w-none");
    expect(launcher).toHaveAttribute("aria-expanded", "true");

    const composer = screen.getByLabelText("Message Charlie");
    composer.focus();
    fireEvent.keyDown(composer, { key: ".", ctrlKey: true, shiftKey: true });
    expect(screen.getByRole("dialog", { name: "Charlie" })).toBeInTheDocument();

    fireEvent.click(screen.getByRole("button", { name: "Remove Alerts" }));
    expect(screen.queryByRole("button", { name: "Remove Alerts" })).toBeNull();
    fireEvent.click(screen.getByRole("button", { name: "Add context" }));
    fireEvent.change(screen.getByLabelText("Search authorized context"), {
      target: { value: "alerts" },
    });
    fireEvent.click(await screen.findByRole("button", { name: /Alerts/ }));
    expect(screen.getByRole("button", { name: "Remove Alerts" })).toBeInTheDocument();

    fireEvent.keyDown(document, { key: "Escape" });
    await waitFor(() => expect(screen.queryByRole("dialog")).toBeNull());
    expect(launcher).toHaveFocus();
  });
});
