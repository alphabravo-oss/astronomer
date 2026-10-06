import { readdirSync, readFileSync, statSync } from "node:fs";
import { join, relative } from "node:path";
import ts from "typescript";
import { describe, expect, it } from "vitest";

// Plan 031 Phase 6b guard: every DataTable column definition must say how wide
// it wants to be, either with a semantic `kind` or an explicit size. A column
// with neither silently falls back to an equal share of the table, which is the
// clipping/wasted-space behavior this phase removed.

const SRC = join(__dirname, "..", "..", "..");
const SIZING_KEYS = new Set([
  "kind",
  "width",
  "size",
  "minSize",
  "grow",
  // Legacy row-action columns keep their fixed 2.5rem footprint on purpose.
  "rowActions",
]);

// A column object that legitimately has no literal sizing keys. Every entry
// needs a reason. Keep this list at zero where possible.
const ALLOWLIST: Record<string, string> = {};

function sourceFiles(dir: string): string[] {
  const out: string[] = [];
  for (const name of readdirSync(dir)) {
    const full = join(dir, name);
    if (statSync(full).isDirectory()) {
      if (name === "node_modules" || name === "__tests__") continue;
      out.push(...sourceFiles(full));
    } else if (/\.tsx?$/.test(name) && !/\.(test|spec|gen)\./.test(name)) {
      out.push(full);
    }
  }
  return out;
}

function propName(p: ts.ObjectLiteralElementLike): string | undefined {
  if (ts.isSpreadAssignment(p)) return "...";
  return p.name && (ts.isIdentifier(p.name) || ts.isStringLiteral(p.name))
    ? p.name.text
    : undefined;
}

/** Column objects: literals with both `key` and `header` that sit in `columns`. */
function columnObjects(file: string): Array<{ key: string; ok: boolean }> {
  const text = readFileSync(file, "utf8");
  if (!/\bkey:/.test(text) || !/\bheader:/.test(text)) return [];
  const sf = ts.createSourceFile(file, text, ts.ScriptTarget.Latest, true);
  const found: Array<{ key: string; ok: boolean }> = [];
  const visit = (node: ts.Node) => {
    if (ts.isObjectLiteralExpression(node)) {
      const names = node.properties.map(propName);
      if (names.includes("key") && names.includes("header")) {
        const keyProp = node.properties.find((p) => propName(p) === "key");
        const keyText =
          keyProp && ts.isPropertyAssignment(keyProp)
            ? keyProp.initializer.getText(sf)
            : "?";
        const spreads = names.includes("...");
        found.push({
          key: keyText,
          ok: spreads || names.some((n) => n && SIZING_KEYS.has(n)),
        });
      }
    }
    ts.forEachChild(node, visit);
  };
  visit(sf);
  return found;
}

describe("DataTable column kinds guard", () => {
  it("every column definition declares a kind or an explicit size", () => {
    const offenders: string[] = [];
    for (const file of sourceFiles(SRC)) {
      const rel = relative(SRC, file);
      for (const col of columnObjects(file)) {
        if (!col.ok && !ALLOWLIST[`${rel}:${col.key}`]) {
          offenders.push(`${rel}:${col.key}`);
        }
      }
    }
    expect(offenders).toEqual([]);
  });

  it("has no stale allowlist entries", () => {
    const live = new Set<string>();
    for (const file of sourceFiles(SRC)) {
      const rel = relative(SRC, file);
      for (const col of columnObjects(file)) live.add(`${rel}:${col.key}`);
    }
    expect(Object.keys(ALLOWLIST).filter((k) => !live.has(k))).toEqual([]);
  });
});
