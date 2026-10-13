import { describe, expect, it } from "vitest";
import {
  changedValuePaths,
  initialCatalogValues,
  mergeCatalogValues,
  type CatalogVersionProfile,
} from "./catalog-configuration";
import { catalogValueErrors } from "./catalog-values-validation";
import { parseHelmValuesYAML } from "./helm-values-schema";

describe("version-bound configuration", () => {
  it("merges a preset without erasing unknown values, arrays, nulls or secret references", () => {
    const current = {
      custom: { untouched: 7 },
      admin: { existingSecret: "grafana-auth" },
      storage: { class: "fast" },
      replicas: 2,
      disabled: null,
      hosts: ["one"],
    };
    const next = mergeCatalogValues(current, { replicas: 3, hosts: ["two"] });
    expect(next).toEqual({ ...current, replicas: 3, hosts: ["two"] });
    expect(changedValuePaths(current, next)).toEqual(["replicas", "hosts"]);
    expect(current.replicas).toBe(2);
  });
  it("blocks prototype properties from untrusted YAML", () => {
    expect(
      mergeCatalogValues(
        {},
        JSON.parse('{"__proto__":{"polluted":true},"constructor":4}'),
      ),
    ).toEqual({});
    expect(({} as Record<string, unknown>).polluted).toBeUndefined();
  });
  it("initializes only the exact returned version's default preset", () => {
    const profile: CatalogVersionProfile = {
      version: "1",
      available: true,
      recommended: true,
      revoked: false,
      deprecated: false,
      supportTier: "upstream",
      configuration: {
        schema: {},
        namespace: "monitoring",
        defaultPreset: "small",
        presets: [
          {
            id: "small",
            label: "Small",
            description: "One replica",
            values: { replicas: 1 },
          },
        ],
      },
    };
    expect(
      parseHelmValuesYAML(
        initialCatalogValues("replicas: 3\ncustom: retained", profile),
      ),
    ).toEqual({ replicas: 1, custom: "retained" });
  });
  it("reports invalid YAML field types and constraints without coercing or disclosing values", () => {
    const schema = {
      type: "object",
      properties: {
        replicas: { type: "integer", minimum: 1 },
        mode: { type: "string", enum: ["safe"] },
        secret: { type: "string", minLength: 3 },
      },
    };
    const values = {
      replicas: "not-a-number",
      mode: "unsupported",
      secret: "pw",
      custom: true,
    };
    const errors = catalogValueErrors(schema, values);
    expect(errors.map((error) => error.path)).toEqual([
      "values.replicas",
      "values.mode",
      "values.secret",
    ]);
    expect(JSON.stringify(errors)).not.toContain("pw");
    expect(values.replicas).toBe("not-a-number");
    expect(
      catalogValueErrors(schema, {
        replicas: 1,
        mode: "safe",
        secret: "ref",
        custom: true,
      }),
    ).toEqual([]);
  });
});
