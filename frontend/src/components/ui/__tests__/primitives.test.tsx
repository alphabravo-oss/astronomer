import { fireEvent, render, screen } from "@testing-library/react";
import { Input } from "@/components/ui/input";
import { Select } from "@/components/ui/select";
import { Textarea } from "@/components/ui/textarea";
import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card";
import { Badge } from "@/components/ui/badge";
import { Switch } from "@/components/ui/switch";
import { TabStrip } from "@/components/ui/tabs";
import { MetricCard } from "@/components/ui/metric-card";
import { Field } from "@/components/form/fields";

vi.mock("@tanstack/react-router", async (importOriginal) => {
  const { RouterLinkStub } = await import("@/test/router-link");
  return {
    ...(await importOriginal<typeof import("@tanstack/react-router")>()),
    Link: RouterLinkStub,
  };
});

describe("form primitives", () => {
  it("renders an input with the shared control chrome", () => {
    render(<Input aria-label="Host" placeholder="smtp.example.com" />);
    const input = screen.getByLabelText("Host");
    expect(input).toHaveClass("h-9", "rounded-md", "border-input");
    expect(input).toHaveAttribute("placeholder", "smtp.example.com");
  });

  it("renders a native select and textarea on the same chrome", () => {
    render(
      <>
        <Select aria-label="Provider" containerClassName="max-w-xs">
          <option value="aws">AWS</option>
        </Select>
        <Textarea aria-label="Notes" />
      </>,
    );
    expect(screen.getByLabelText("Provider")).toHaveClass(
      "h-9",
      "rounded-md",
      "appearance-none",
      "pr-9",
    );
    expect(screen.getByLabelText("Provider").nextElementSibling).toHaveAttribute(
      "aria-hidden",
      "true",
    );
    expect(screen.getByLabelText("Provider").parentElement).toHaveClass(
      "max-w-xs",
    );
    expect(screen.getByLabelText("Notes")).toHaveClass("min-h-[120px]");
  });
});

describe("Card / Badge / Switch / Tabs", () => {
  it("renders a card frame", () => {
    render(
      <Card>
        <CardHeader>
          <CardTitle>Instances</CardTitle>
        </CardHeader>
        <CardContent>Table</CardContent>
      </Card>,
    );
    expect(screen.getByText("Instances")).toBeInTheDocument();
    expect(screen.getByText("Table")).toBeInTheDocument();
  });

  it("supports padding and radius variants without double-padding sub-components", () => {
    const { container: bare } = render(<Card padding="lg">Bare</Card>);
    expect(bare.firstChild).toHaveClass("p-6", "rounded-lg");

    const { container: sub } = render(
      <Card radius="xl">
        <CardHeader>
          <CardTitle>Title</CardTitle>
        </CardHeader>
      </Card>,
    );
    // Default padding is "none" so CardHeader's own p-5 isn't stacked with a
    // second padding from Card itself.
    expect(sub.firstChild).toHaveClass("p-0", "rounded-xl");
  });

  it("applies status badge variants", () => {
    const { container } = render(<Badge variant="error">Failed</Badge>);
    expect(container.firstChild).toHaveClass("text-status-error");
  });

  it("toggles the switch via onCheckedChange", () => {
    const onCheckedChange = vi.fn();
    render(
      <Switch
        checked={false}
        onCheckedChange={onCheckedChange}
        aria-label="Enabled"
      />,
    );
    fireEvent.click(screen.getByRole("switch", { name: "Enabled" }));
    expect(onCheckedChange).toHaveBeenCalledWith(true);
  });

  it("renders a smaller switch track/thumb with the sm size", () => {
    const { container } = render(
      <Switch checked size="sm" aria-label="Compact" />,
    );
    const track = container.firstChild as HTMLElement;
    expect(track).toHaveClass("h-5", "w-9");
    expect(track.firstChild).toHaveClass("h-3.5", "w-3.5");
  });

  it("renders an underline tab strip and reports the clicked key", () => {
    const onChange = vi.fn();
    render(
      <TabStrip
        value="rules"
        onChange={onChange}
        tabs={[
          { key: "rules", label: "Alert Rules" },
          { key: "channels", label: "Channels" },
        ]}
      />,
    );
    fireEvent.click(screen.getByRole("tab", { name: "Channels" }));
    expect(onChange).toHaveBeenCalledWith("channels");
  });
});

describe("MetricCard", () => {
  it("accepts label as an alias for title", () => {
    render(<MetricCard label="CPU" value="42%" />);
    expect(screen.getByText("CPU")).toBeInTheDocument();
  });

  it("still supports the title prop", () => {
    render(<MetricCard title="Memory" value="1 GiB" />);
    expect(screen.getByText("Memory")).toBeInTheDocument();
  });

  it("applies a fixed tone instead of the percentage-derived color", () => {
    render(<MetricCard title="Errors" value="12" tone="error" />);
    expect(screen.getByText("12")).toHaveClass("text-status-error");
  });

  it("renders as a link when href is set", () => {
    render(<MetricCard title="Nodes" value="5" href="/dashboard/clusters" />);
    expect(screen.getByRole("link")).toHaveAttribute(
      "href",
      "/dashboard/clusters",
    );
  });

  it("uses a tighter type scale in dense mode", () => {
    render(<MetricCard title="Pods" value="3" dense />);
    expect(screen.getByText("3")).toHaveClass("text-lg");
  });
});

describe("Field", () => {
  it("renders a label, control, and description", () => {
    render(
      <Field label="Host" description="Reachable from the cluster." htmlFor="host">
        <input id="host" />
      </Field>,
    );
    expect(
      screen.getByLabelText("Host", { selector: "input" }),
    ).toBeInTheDocument();
    expect(
      screen.getByText("Reachable from the cluster."),
    ).toBeInTheDocument();
  });

  it("renders an alert instead of the description when there's an error", () => {
    render(
      <Field
        label="Host"
        description="Reachable from the cluster."
        error="Host is required"
        htmlFor="host"
        required
      >
        <input id="host" />
      </Field>,
    );
    expect(screen.getByRole("alert")).toHaveTextContent("Host is required");
    expect(
      screen.queryByText("Reachable from the cluster."),
    ).not.toBeInTheDocument();
  });
});
