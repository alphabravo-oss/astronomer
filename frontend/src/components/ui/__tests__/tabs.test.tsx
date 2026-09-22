import { fireEvent, render, screen } from "@testing-library/react";
import { useState } from "react";
import { TabsContent, TabStrip } from "@/components/ui/tabs";

type TabKey = "overview" | "yaml" | "events";

function Harness() {
  const [tab, setTab] = useState<TabKey>("overview");
  return (
    <>
      <TabStrip<TabKey>
        value={tab}
        onChange={setTab}
        tabs={[
          { key: "overview", label: "Overview" },
          { key: "yaml", label: "YAML" },
          { key: "events", label: "Events", count: 3 },
        ]}
      />
      <TabsContent value="overview" active={tab === "overview"}>
        Overview panel
      </TabsContent>
      <TabsContent value="yaml" active={tab === "yaml"}>
        YAML panel
      </TabsContent>
      <TabsContent value="events" active={tab === "events"}>
        Events panel
      </TabsContent>
    </>
  );
}

describe("Tabs", () => {
  it("exposes tablist/tab/tabpanel roles wired by id", () => {
    render(<Harness />);

    const tablist = screen.getByRole("tablist");
    expect(tablist).toBeInTheDocument();

    const overviewTab = screen.getByRole("tab", { name: "Overview" });
    expect(overviewTab).toHaveAttribute("aria-selected", "true");
    expect(overviewTab).toHaveAttribute("id", "tab-overview");
    // TabStrip intentionally does not auto-wire aria-controls: none of its
    // real consumers render a TabsContent with a matching `value` today, so
    // a guessed reference would point at a non-existent id (an axe
    // "aria-valid-attr-value" violation) — see tabs.tsx.
    expect(overviewTab).not.toHaveAttribute("aria-controls");
    expect(overviewTab).toHaveAttribute("tabindex", "0");

    const yamlTab = screen.getByRole("tab", { name: "YAML" });
    expect(yamlTab).toHaveAttribute("aria-selected", "false");
    expect(yamlTab).toHaveAttribute("tabindex", "-1");

    const panel = screen.getByRole("tabpanel", { name: "Overview" });
    expect(panel).toHaveAttribute("id", "tabpanel-overview");
    expect(panel).toHaveTextContent("Overview panel");
  });

  it("renders an optional count badge", () => {
    render(<Harness />);
    const eventsTab = screen.getByRole("tab", { name: /Events/ });
    expect(eventsTab).toHaveTextContent("3");
  });

  it("moves selection and focus with ArrowRight, wrapping at the end", () => {
    render(<Harness />);
    const overviewTab = screen.getByRole("tab", { name: "Overview" });
    const yamlTab = screen.getByRole("tab", { name: "YAML" });
    const eventsTab = screen.getByRole("tab", { name: /Events/ });

    overviewTab.focus();
    fireEvent.keyDown(overviewTab, { key: "ArrowRight" });
    expect(yamlTab).toHaveFocus();
    expect(yamlTab).toHaveAttribute("aria-selected", "true");

    fireEvent.keyDown(yamlTab, { key: "ArrowRight" });
    expect(eventsTab).toHaveFocus();

    // Wraps back to the first tab.
    fireEvent.keyDown(eventsTab, { key: "ArrowRight" });
    expect(overviewTab).toHaveFocus();
    expect(overviewTab).toHaveAttribute("aria-selected", "true");
  });

  it("moves selection and focus with ArrowLeft, wrapping at the start", () => {
    render(<Harness />);
    const overviewTab = screen.getByRole("tab", { name: "Overview" });
    const eventsTab = screen.getByRole("tab", { name: /Events/ });

    overviewTab.focus();
    fireEvent.keyDown(overviewTab, { key: "ArrowLeft" });
    expect(eventsTab).toHaveFocus();
    expect(eventsTab).toHaveAttribute("aria-selected", "true");
  });

  it("Home and End jump to the first and last tab", () => {
    render(<Harness />);
    const overviewTab = screen.getByRole("tab", { name: "Overview" });
    const yamlTab = screen.getByRole("tab", { name: "YAML" });
    const eventsTab = screen.getByRole("tab", { name: /Events/ });

    yamlTab.focus();
    fireEvent.keyDown(yamlTab, { key: "End" });
    expect(eventsTab).toHaveFocus();

    fireEvent.keyDown(eventsTab, { key: "Home" });
    expect(overviewTab).toHaveFocus();
  });
});
