import { render, screen } from "@testing-library/react";
import {
  lastTerminationSummary,
  PodContainersSubRow,
} from "@/components/resources/pod-containers-subrow";
import type { Container, Pod } from "@/types";

const container = (extra: Partial<Container> = {}): Container => ({
  name: "app",
  image: "nginx:1.25",
  status: "running",
  ready: true,
  restartCount: 2,
  ...extra,
});

describe("PodContainersSubRow", () => {
  it("summarizes the last termination", () => {
    expect(lastTerminationSummary(container())).toBe("None");
    expect(
      lastTerminationSummary(
        container({
          lastState: { terminated: { reason: "OOMKilled", exitCode: 137 } },
        }),
      ),
    ).toBe("OOMKilled, exit 137");
  });

  it("lists each container with state, restarts and image", () => {
    const pod = {
      name: "web",
      containers: [container(), container({ name: "init-db", init: true })],
    } as unknown as Pod;
    render(<PodContainersSubRow pod={pod} />);
    const table = screen.getByRole("table", { name: "Containers of web" });
    expect(table).toHaveTextContent("app");
    expect(table).toHaveTextContent("init-db");
    expect(table).toHaveTextContent("(init)");
    expect(table).toHaveTextContent("nginx:1.25");
  });

  it("explains a pod with no container details", () => {
    render(
      <PodContainersSubRow
        pod={{ name: "x", containers: [] } as unknown as Pod}
      />,
    );
    expect(screen.getByText(/No container details/)).toBeInTheDocument();
  });
});
