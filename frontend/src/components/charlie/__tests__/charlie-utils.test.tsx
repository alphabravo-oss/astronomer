import { render, screen } from "@testing-library/react";
import { describe, expect, it } from "vitest";
import { charlieContextRegistry, contextForRoute } from "../context-registry";
import { SafeMarkdown, safeLink } from "../safe-markdown";

describe("Charlie safe UI boundary", () => {
  it("maps supported routes to bounded identifiers", () => {
    expect(contextForRoute("/dashboard/clusters/abc/tools")[0]).toMatchObject({
      type: "agent_connection_record",
      id: "abc",
      requiredVerb: "read",
    });
    expect(contextForRoute("/dashboard/clusters/abc")[0].type).toBe(
      "agent_connection_record",
    );
    expect(contextForRoute("/dashboard/alerting")[0].type).toBe("alert");
    expect(contextForRoute("/dashboard/agents")[0].type).toBe("cluster_agents");
    expect(contextForRoute("/dashboard/backups/run-1")[0]).toMatchObject({
      type: "backup",
      id: "run-1",
      label: "Astronomer backup",
    });
    expect(contextForRoute("/dashboard/settings/backup")[0]).toMatchObject({
      type: "backup",
      id: "management",
    });
    expect(contextForRoute("/dashboard/delivery/targets/target-1")[0].type).toBe(
      "self_management_application",
    );
    expect(contextForRoute("/dashboard/audit")).toEqual([]);
    expect(contextForRoute("/dashboard/logging")).toEqual([]);
  });
  it("sanitizes route identifiers", () =>
    expect(contextForRoute("/dashboard/clusters/a%20b")[0].id).toBe("ab"));
  it("keeps every sensitive product surface explicit", () =>
    expect(charlieContextRegistry.map((a) => a.id)).toEqual(
      expect.arrayContaining([
        "downstream_agent_record",
        "downstream_agent_record_from_component_page",
        "alerts",
        "monitoring",
        "logging",
        "backups",
        "continuous_delivery",
        "audit",
        "agent_record",
        "cluster_agents",
        "agent_tunnel",
      ]),
    ));
  it("rejects unsafe links and raw html", () => {
    expect(safeLink("javascript:alert(1)")).toBeNull();
    render(
      <SafeMarkdown>
        {
          "<img src=x> ![remote](https://example.com/pixel.png) [safe](https://example.com) [bad](javascript:x)"
        }
      </SafeMarkdown>,
    );
    expect(screen.queryByRole("img")).toBeNull();
    expect(screen.getByRole("link", { name: "safe" })).toHaveAttribute(
      "href",
      "https://example.com",
    );
    expect(screen.getByText("[Image omitted: remote]")).toBeInTheDocument();
    expect(screen.queryByRole("link", { name: "bad" })).toBeNull();
  });
  it("renders structured GitHub-flavored Markdown", () => {
    const { container } = render(
      <SafeMarkdown>
        {
          "## Installation\n\nCurrent status is **healthy**.\n\n1. Check the server\n2. Review `events`\n\n| Component | State |\n| --- | --- |\n| API | Ready |"
        }
      </SafeMarkdown>,
    );
    expect(
      screen.getByRole("heading", { level: 2, name: "Installation" }),
    ).toBeInTheDocument();
    expect(screen.getByRole("list")).toHaveClass("list-decimal");
    expect(screen.getByText("healthy").tagName).toBe("STRONG");
    expect(screen.getByText("events").tagName).toBe("CODE");
    expect(container.querySelector("table")).not.toBeNull();
  });
});
