import { describe, expect, it } from "vitest";
import {
  categoricalValues,
  type CategoryName,
} from "@/types/categories.generated";
import {
  classifyCategory,
  CategoricalResponseError,
  validateCategoricalResponse,
} from "./categorical-values";
import {
  availableRolloutActions,
  rolloutIsTerminal,
} from "./delivery-rollouts";
import { mapBackupRestore } from "./backups";
import { camelizeWire } from "@/lib/camelize";

const rolloutPage = (state: unknown = "progressing") => ({
  data: [{ state, strategy: { type: "rolling", on_failure: "pause" } }],
});

describe("checked categorical response boundary", () => {
  it("round trips every canonical category without accepting malformed or invented values", () => {
    for (const category of Object.keys(categoricalValues) as CategoryName[]) {
      for (const value of categoricalValues[category]) {
        expect(
          classifyCategory(category, JSON.parse(JSON.stringify(value))),
        ).toEqual({ kind: "known", category, value });
      }
      for (const value of [
        null,
        undefined,
        1,
        {},
        [],
        "unsupported",
        "",
        " succeeded ",
      ]) {
        expect(classifyCategory(category, value)).toEqual({
          kind: "unknown",
          category,
        });
      }
    }
  });

  it("checks nested response categories before typed adapters consume them", () => {
    expect(() =>
      validateCategoricalResponse("getDeliveryRollouts", 200, rolloutPage()),
    ).not.toThrow();
    expect(
      validateCategoricalResponse(
        "getDeliveryRollouts",
        200,
        rolloutPage("unknown-vendor-state"),
      ),
    ).toEqual(rolloutPage("unsupported"));
    for (const value of [rolloutPage(null), { data: [{}] }, { data: {} }, {}]) {
      expect(() =>
        validateCategoricalResponse("getDeliveryRollouts", 200, value),
      ).toThrow(CategoricalResponseError);
    }
    expect(() =>
      validateCategoricalResponse("getDeliveryRollouts", 201, rolloutPage()),
    ).toThrow(CategoricalResponseError);
    const inherited = Object.create({
      state: "succeeded",
      strategy: { type: "rolling", on_failure: "pause" },
    });
    expect(() =>
      validateCategoricalResponse("getDeliveryRollouts", 200, {
        data: [inherited],
      }),
    ).toThrow(CategoricalResponseError);
  });

  it("keeps unsupported wire values out of diagnostic errors", () => {
    const secretLike = "attacker-controlled-category-value";
    try {
      const checked = validateCategoricalResponse(
        "getDeliveryRollouts",
        200,
        rolloutPage(secretLike),
      );
      expect(JSON.stringify(checked)).not.toContain(secretLike);
      validateCategoricalResponse(
        "getDeliveryRollouts",
        200,
        rolloutPage({ private: secretLike }),
      );
    } catch (error) {
      expect(error).toBeInstanceOf(CategoricalResponseError);
      expect(String(error)).not.toContain(secretLike);
    }
  });

  it("uses an exhaustive action matrix and disables all unknown states", () => {
    const expected = {
      draft: [],
      resolving: ["pause", "abort"],
      awaiting_approval: ["abort"],
      rejected: [],
      queued: ["pause", "abort"],
      progressing: ["pause", "abort"],
      paused: ["resume", "abort", "retry", "rollback"],
      aborted: [],
      succeeded: ["rollback"],
      failed: ["abort", "retry", "rollback"],
      rolling_back: [],
      rolled_back: [],
      rollback_failed: [],
    };
    expect(Object.keys(expected).sort()).toEqual(
      [...categoricalValues.DeliveryRolloutState].sort(),
    );
    for (const [state, actions] of Object.entries(expected))
      expect(availableRolloutActions(state)).toEqual(actions);
    for (const state of [
      undefined,
      null,
      {},
      "future-state",
      "toString",
      "__proto__",
    ])
      expect(availableRolloutActions(state)).toEqual([]);
    for (const state of [
      "rejected",
      "aborted",
      "succeeded",
      "failed",
      "rolled_back",
      "rollback_failed",
    ] as const)
      expect(rolloutIsTerminal(state)).toBe(true);
    expect(rolloutIsTerminal("rolling_back")).toBe(false);
  });

  it("preserves open historical backup wire statuses as neutral unknown views", () => {
    const wire = {
      id: "restore-1",
      backup_id: "backup-1",
      created_at: "2026-10-09T00:00:00Z",
      updated_at: "2026-10-09T00:00:00Z",
    };
    expect(mapBackupRestore({ ...wire, status: "running" }).status).toBe(
      "running",
    );
    expect(mapBackupRestore({ ...wire, status: "in_progress" }).status).toBe(
      "in_progress",
    );
    expect(
      mapBackupRestore({ ...wire, status: "vendor-future-success" }).status,
    ).toBe("unknown");
    expect(mapBackupRestore(wire).status).toBe("unknown");
  });

  it("maps view keys without changing category values or optional/null array fields", () => {
    const mapped = camelizeWire({
      rollout_state: "aborted" as const,
      cluster_items: [{ current_phase: "unknown" as const }],
      nullable_items: null,
    });
    expect(mapped).toEqual({
      rolloutState: "aborted",
      clusterItems: [{ currentPhase: "unknown" }],
      nullableItems: null,
    });
  });
});
