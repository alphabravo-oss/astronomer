import { readFileSync, readdirSync } from "node:fs";
import { resolve } from "node:path";
import { describe, expect, it } from "vitest";

const operationalGrids = [
  "src/routes/dashboard/clusters/$id/workloads/index.tsx",
  "src/components/extensions/ExtTable.tsx",
  "src/components/resources/namespace-detail-page.tsx",
];

describe("TanStack table adoption", () => {
  const rawHTMLTablePattern = new RegExp("<" + "table\\b");
  const anyTablePattern = new RegExp("<" + "table\\b|<" + "Table\\b");

  for (const file of operationalGrids) {
    it(`${file} uses the shared DataTable instead of raw table markup`, () => {
      const source = readFileSync(resolve(process.cwd(), file), "utf8");
      expect(source).toContain("<DataTable");
      expect(source).not.toMatch(anyTablePattern);
    });
  }

  it("does not allow unreviewed raw HTML tables in application code", () => {
    const root = resolve(process.cwd(), "src");
    const files: string[] = [];
    const walk = (dir: string) => {
      for (const entry of readdirSync(dir, { withFileTypes: true })) {
        const path = resolve(dir, entry.name);
        if (entry.isDirectory()) walk(path);
        else if (entry.name.endsWith(".tsx")) files.push(path);
      }
    };
    walk(root);
    const allowed = new Set([
      resolve(root, "components/charlie/safe-markdown.tsx"),
      resolve(root, "components/ui/data-table.tsx"),
      resolve(root, "components/ui/table.tsx"),
    ]);
    const offenders = files.filter(
      (file) =>
        !file.includes("/__tests__/") &&
        !allowed.has(file) &&
        rawHTMLTablePattern.test(readFileSync(file, "utf8")),
    );
    expect(offenders).toEqual([]);
  });
});
