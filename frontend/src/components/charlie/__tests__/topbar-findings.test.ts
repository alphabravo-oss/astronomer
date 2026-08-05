import { describe, expect, it } from "vitest";
import type { CharlieFinding } from "@/lib/api/charlie";
import { selectImportantCharlieFindings } from "../topbar-findings";

function finding(
  id: string,
  severity: CharlieFinding["severity"],
  state: CharlieFinding["state"],
  reasonNoAction: string,
): CharlieFinding {
  return {
    id,
    title: id,
    severity,
    state,
    reasonNoAction,
    affectedResource: { type: "installation", id: "deployment-a", requiredVerb: "read" },
    summary: "bounded",
  };
}

describe("Charlie topbar finding semantics", () => {
  it("includes an actionable medium read-only finding", () => {
    expect(
      selectImportantCharlieFindings([
        finding("read-only", "medium", "open", "read_only"),
      ]).map((item) => item.id),
    ).toEqual(["read-only"]);
  });

  it("excludes resolved critical and inert disabled findings", () => {
    expect(
      selectImportantCharlieFindings([
        finding("resolved", "critical", "resolved", "read_only"),
        finding("disabled", "critical", "open", "product_disabled"),
        finding("approval", "warning", "acknowledged", "approval_required"),
      ]).map((item) => item.id),
    ).toEqual(["approval"]);
  });

  it("deduplicates before applying the bounded notification limit", () => {
    const duplicate = finding("same", "high", "open", "scope_denied");
    expect(
      selectImportantCharlieFindings([
        duplicate,
        duplicate,
        finding("next", "critical", "open", "read_only"),
      ], 2).map((item) => item.id),
    ).toEqual(["same", "next"]);
  });
});
