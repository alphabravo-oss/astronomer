import { outputsForCluster, validOutputSelection } from "./-output-scope";
import type { LoggingOutput } from "@/types";

const outputs = [
  { id: "a", clusterId: "one" },
  { id: "b", clusterId: "two" },
  { id: "global" },
] as LoggingOutput[];
describe("logging pipeline output scope", () => {
  it("excludes global and other-cluster outputs", () => {
    expect(
      outputsForCluster(outputs, "one").map((output) => output.id),
    ).toEqual(["a"]);
    expect(outputsForCluster(outputs, "")).toEqual([]);
  });
  it("rejects stale selections after a scope switch or failed read", () => {
    expect(validOutputSelection(["a"], outputsForCluster(outputs, "one"))).toBe(
      true,
    );
    expect(validOutputSelection(["a"], outputsForCluster(outputs, "two"))).toBe(
      false,
    );
    expect(
      validOutputSelection(["a"], outputsForCluster(undefined, "one")),
    ).toBe(false);
    expect(validOutputSelection([], outputs)).toBe(false);
  });
});
