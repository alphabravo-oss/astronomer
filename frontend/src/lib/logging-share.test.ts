import { describe, expect, it } from "vitest";
import {
  buildSharedLoggingURL,
  clearSharedLoggingURL,
  parseSharedLoggingFilters,
} from "./logging-share";

describe("logging share URL", () => {
  it("round trips bounded filters without losing unrelated URL state", () => {
    const url = buildSharedLoggingURL(
      "https://astro.example/dashboard/logging?tab=outputs",
      {
        outputId: "output-1",
        query: `{app="api"} |= "error"`,
        namespaces: ["payments", "platform"],
        limit: 250,
        start: "2026-08-23T00:00:00Z",
        end: "2026-08-23T01:00:00Z",
      },
    );
    expect(new URL(url).searchParams.get("tab")).toBe("outputs");
    expect(parseSharedLoggingFilters(url)).toEqual({
      outputId: "output-1",
      query: `{app="api"} |= "error"`,
      namespaces: ["payments", "platform"],
      limit: 250,
      start: "2026-08-23T00:00:00Z",
      end: "2026-08-23T01:00:00Z",
    });
  });

  it("clamps hostile limits and removes only logging filters", () => {
    const parsed = parseSharedLoggingFilters(
      "https://astro.example/dashboard/logging?log_output=x&log_limit=999999&log_namespaces=a,b,c",
    );
    expect(parsed?.limit).toBe(1000);
    const cleared = clearSharedLoggingURL(
      "https://astro.example/dashboard/logging?tab=outputs&log_output=x&log_query=secret",
    );
    expect(new URL(cleared).search).toBe("?tab=outputs");
  });
});
