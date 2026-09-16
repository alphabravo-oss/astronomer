import { describe, expect, it } from "vitest";
import { targetOverridesFromForm } from "./target-overrides-editor";

describe("targetOverridesFromForm", () => {
  it("parses deterministic renderer-specific inputs", () => {
    const form = new FormData();
    form.set("override_helm_values", '{"replicaCount":3}');
    form.set("override_patches", "kind: Deployment\nmetadata:\n  name: a\n---\nkind: Service\nmetadata:\n  name: b");
    expect(targetOverridesFromForm(form)).toEqual({
      helm_values: { replicaCount: 3 },
      patches: [
        "kind: Deployment\nmetadata:\n  name: a",
        "kind: Service\nmetadata:\n  name: b",
      ],
    });
  });

  it("rejects non-object Helm values", () => {
    const form = new FormData();
    form.set("override_helm_values", "[]");
    expect(() => targetOverridesFromForm(form)).toThrow("JSON object");
  });
});
