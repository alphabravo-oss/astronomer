import { readFileSync, readdirSync } from "node:fs";
import { resolve } from "node:path";
import { describe, expect, it } from "vitest";

const rawPalette = /\b(?:text|bg|border|ring)-(?:gray|slate|zinc|neutral|stone|red|orange|amber|yellow|green|emerald|teal|cyan|sky|blue|indigo|violet|purple|fuchsia|pink|rose)-[0-9]+\b/;
const intentionalVisualPalettes = new Set([
  "src/routes/auth/login/index.tsx",
  "src/routes/dashboard/catalog/-category.tsx",
  "src/components/projects/cloud-credentials/provider-badge.tsx",
]);

describe("global design-token colors", () => {
  it("uses semantic tokens for operational UI state", () => {
    const root = resolve(process.cwd(), "src");
    const offenders: string[] = [];
    const walk = (dir: string) => {
      for (const entry of readdirSync(dir, { withFileTypes: true })) {
        const path = resolve(dir, entry.name);
        if (entry.isDirectory()) walk(path);
        else if (/\.tsx?$/.test(entry.name)) {
          const relative = `src/${path.slice(root.length + 1)}`;
          if (!path.includes("/__tests__/") && !intentionalVisualPalettes.has(relative) && rawPalette.test(readFileSync(path, "utf8"))) offenders.push(relative);
        }
      }
    };
    walk(root);
    expect(offenders).toEqual([]);
  });
});
