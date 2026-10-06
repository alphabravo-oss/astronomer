import { describe, expect, it } from "vitest";
import { render, screen } from "@testing-library/react";
import {
  ChipsCell,
  EntityCell,
  ImageRefCell,
  StatusReasonCell,
  TimestampCell,
  UsageGauge,
  shortAge,
  splitImageRef,
} from "@/components/tables/cells";

describe("splitImageRef", () => {
  it("splits registry/repo, tag and digest", () => {
    expect(
      splitImageRef(
        "registry.k8s.io/ingress-nginx/controller:v1.11.2@sha256:abc",
      ),
    ).toEqual({
      repo: "registry.k8s.io/ingress-nginx/controller",
      tag: "v1.11.2",
      digest: "sha256:abc",
    });
  });

  it("does not mistake a registry port for a tag", () => {
    expect(splitImageRef("localhost:5000/app")).toEqual({
      repo: "localhost:5000/app",
      tag: "",
      digest: "",
    });
    expect(splitImageRef("localhost:5000/app:1.2").tag).toBe("1.2");
  });
});

describe("ImageRefCell", () => {
  it("always renders the tag and the +N chip", () => {
    render(
      <ImageRefCell image="ghcr.io/acme/very/long/path/api:v2" extra={2} />,
    );
    expect(screen.getByText(":v2")).toBeTruthy();
    expect(screen.getByText("+2")).toBeTruthy();
  });

  it("renders a dash when there is no image", () => {
    render(<ImageRefCell image="" />);
    expect(screen.getByText("—")).toBeTruthy();
  });
});

describe("ChipsCell", () => {
  it("caps at two chips and summarizes the rest", () => {
    render(<ChipsCell items={["a", "b", "c", "d"]} />);
    expect(screen.getByText("a")).toBeTruthy();
    expect(screen.getByText("b")).toBeTruthy();
    expect(screen.queryByText("c")).toBeNull();
    expect(screen.getByText("+2")).toBeTruthy();
  });

  it("shows the empty marker", () => {
    render(<ChipsCell items={[]} />);
    expect(screen.getByText("—")).toBeTruthy();
  });
});

describe("TimestampCell", () => {
  it("renders compact relative text, with an optional ago suffix", () => {
    const threeHours = new Date(
      Date.now() - 3 * 3_600_000 - 5_000,
    ).toISOString();
    render(
      <>
        <TimestampCell value={threeHours} />
        <TimestampCell value={threeHours} suffix />
      </>,
    );
    expect(screen.getByText("3h")).toBeTruthy();
    expect(screen.getByText("3h ago")).toBeTruthy();
  });

  it("falls back for missing and zero timestamps", () => {
    render(
      <>
        <TimestampCell value={null} fallback="none" />
        <TimestampCell value="0001-01-01T00:00:00Z" fallback="zero" />
      </>,
    );
    expect(screen.getByText("none")).toBeTruthy();
    expect(screen.getByText("zero")).toBeTruthy();
  });
});

describe("EntityCell and StatusReasonCell", () => {
  it("renders primary and secondary lines", () => {
    render(<EntityCell primary="prod-east" secondary="c-1234" />);
    expect(screen.getByText("prod-east")).toBeTruthy();
    expect(screen.getByText("c-1234")).toBeTruthy();
  });

  it("renders a reason under the status", () => {
    render(
      <StatusReasonCell status={<b>Degraded</b>} reason="stale heartbeat" />,
    );
    expect(screen.getByText("Degraded")).toBeTruthy();
    expect(screen.getByText("stale heartbeat")).toBeTruthy();
  });
});

describe("shortAge", () => {
  const now = Date.parse("2026-10-06T12:00:00Z");
  it("compacts to the largest whole unit", () => {
    const at = (ms: number) => new Date(now - ms).toISOString();
    expect(shortAge(at(10_000), now)).toBe("now");
    expect(shortAge(at(14 * 60_000), now)).toBe("14m");
    expect(shortAge(at(5 * 3_600_000), now)).toBe("5h");
    expect(shortAge(at(3 * 86_400_000), now)).toBe("3d");
    expect(shortAge(at(75 * 86_400_000), now)).toBe("2mo");
    expect(shortAge(at(900 * 86_400_000), now)).toBe("2y");
  });
});

describe("UsageGauge", () => {
  it("renders the percent label and clamps the bar", () => {
    const { container } = render(<UsageGauge pct={140} label="140%" />);
    expect(screen.getByText("140%")).toBeTruthy();
    const fill = container.querySelector(".gauge-bar-fill") as HTMLElement;
    expect(fill.style.width).toBe("100%");
    expect(fill.className).toContain("bg-status-error");
  });
});
