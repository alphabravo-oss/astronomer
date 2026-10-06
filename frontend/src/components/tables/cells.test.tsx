import { describe, expect, it } from "vitest";
import { render, screen } from "@testing-library/react";
import {
  ChipsCell,
  EntityCell,
  ImageRefCell,
  StatusReasonCell,
  TimestampCell,
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
  it("renders relative text for a timestamp", () => {
    render(<TimestampCell value={new Date().toISOString()} />);
    expect(screen.getByText(/ago/)).toBeTruthy();
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
