import { toolStatusMetric } from "./tool-status-metric";
import type { ClusterToolStatus } from "@/types";

describe("cluster overview operational truth", () => {
  it("does not claim an empty tool response is fully installed", () => {
    expect(
      toolStatusMetric({ data: [], isLoading: false, isError: false }),
    ).toEqual({ value: "—", subtitle: "No tool status reported" });
  });
  it("masks cached installed counts on denial and distinguishes loading", () => {
    const data = [
      { slug: "prometheus", status: "installed" },
    ] as ClusterToolStatus[];
    expect(
      toolStatusMetric({
        data,
        isLoading: false,
        isError: true,
        error: { status: 403 },
      }),
    ).toEqual({ value: "—", subtitle: "Tool access denied" });
    expect(
      toolStatusMetric({ data, isLoading: true, isError: false }).value,
    ).toBe("—");
    expect(
      toolStatusMetric({ data, isLoading: false, isError: false }).value,
    ).toBe("1/1");
  });
});
