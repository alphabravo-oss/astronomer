import { describe, expect, it } from "vitest";

import {
  parseRegistrationSearch,
  registrationSearch,
} from "./registration-flow";

describe("registration route state", () => {
  it("keeps an existing cluster and install tab in the unified flow", () => {
    expect(
      parseRegistrationSearch({ clusterId: "cluster-1", tab: "yaml" }),
    ).toEqual({ clusterId: "cluster-1", tab: "yaml" });
  });

  it("drops invalid search values and clears back to cluster details", () => {
    expect(parseRegistrationSearch({ clusterId: 42, tab: false })).toEqual({
      clusterId: undefined,
      tab: undefined,
    });
    expect(registrationSearch()).toEqual({});
  });
});
