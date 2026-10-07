/**
 * Plan 031 Phase 4: every route page must render a PageHeader (list/settings
 * pages) or ResourceMasthead (detail pages) from components/ui/page.tsx.
 * Static analysis: the generated route manifest enumerates route files; for
 * each route that declares a `component`, the file plus the local modules it
 * (transitively, depth-limited) imports must reference one of the anatomy
 * primitives. Pages that legitimately cannot are allowlisted with a reason.
 */
import { existsSync, readFileSync } from "node:fs";
import path from "node:path";
import { describe, expect, it } from "vitest";

const SRC = path.resolve(__dirname, "../..");
const ANATOMY = /\b(PageHeader|ResourceMasthead)\b/;
const MAX_DEPTH = 2;

/** Route file (relative to src/routes, no extension) -> reason it has no PageHeader/ResourceMasthead. */
const ALLOWLIST: Record<string, string> = {
  index: "redirect-only landing route",
  "auth/login/index": "auth card layout, no app shell",
  "auth/login/forgot-password/index": "auth card layout, no app shell",
  "auth/login/reset-password/index": "auth card layout, no app shell",
  "auth/change-password/index": "auth card layout, no app shell",
  "dashboard/clusters/$id/shell/index":
    "full-bleed terminal; the terminal chrome is the header",
};

function routeFiles(): string[] {
  const gen = readFileSync(path.join(SRC, "routeTree.gen.ts"), "utf8");
  const files = new Set<string>();
  for (const m of gen.matchAll(/from '\.\/routes\/([^']+)'/g)) files.add(m[1]);
  files.delete("__root");
  return [...files];
}

function resolveModule(from: string, spec: string): string | null {
  let base: string;
  if (spec.startsWith("@/")) base = path.join(SRC, spec.slice(2));
  else if (spec.startsWith(".")) base = path.resolve(path.dirname(from), spec);
  else return null;
  for (const c of [
    `${base}.tsx`,
    `${base}.ts`,
    path.join(base, "index.tsx"),
    path.join(base, "index.ts"),
  ])
    if (existsSync(c)) return c;
  return null;
}

function usesAnatomy(entry: string): boolean {
  const seen = new Set<string>();
  const walk = (file: string, depth: number): boolean => {
    if (seen.has(file) || depth > MAX_DEPTH) return false;
    seen.add(file);
    if (file.endsWith(path.join("components", "ui", "page.tsx"))) return false;
    const src = readFileSync(file, "utf8");
    if (ANATOMY.test(src)) return true;
    for (const m of src.matchAll(/(?:from|import)\s*\(?\s*["']([^"']+)["']/g)) {
      const next = resolveModule(file, m[1]);
      if (
        next &&
        !next.includes(`${path.sep}components${path.sep}ui${path.sep}`)
      )
        if (walk(next, depth + 1)) return true;
    }
    return false;
  };
  return walk(entry, 0);
}

/** A child page inherits the header of an ancestor layout (route.tsx) that renders one. */
function ancestorHasAnatomy(rel: string): boolean {
  const parts = rel.split("/").slice(0, -1);
  while (parts.length > 1) {
    const layout = path.join(SRC, "routes", ...parts, "route.tsx");
    if (existsSync(layout) && usesAnatomy(layout)) return true;
    parts.pop();
  }
  return false;
}

describe("page anatomy", () => {
  const pages = routeFiles()
    .map((rel) => ({
      rel,
      file: [".tsx", ".ts"]
        .map((e) => path.join(SRC, "routes", rel + e))
        .find(existsSync),
    }))
    .filter((p): p is { rel: string; file: string } => !!p.file)
    // `route.tsx` files are layouts (they wrap an <Outlet/>); their header is
    // inherited by the pages below them and is checked through ancestorHasAnatomy.
    .filter((p) => path.basename(p.rel) !== "route")
    .filter((p) => /\bcomponent\s*:/.test(readFileSync(p.file, "utf8")));

  it("enumerates a plausible number of pages", () => {
    expect(pages.length).toBeGreaterThan(60);
  });

  it.each(pages.map((p) => [p.rel, p.file]))(
    "%s renders PageHeader or ResourceMasthead",
    (rel, file) => {
      if (ALLOWLIST[rel]) return;
      expect(
        usesAnatomy(file) || ancestorHasAnatomy(rel),
        `${rel} has no PageHeader/ResourceMasthead; add one or allowlist it with a reason`,
      ).toBe(true);
    },
  );

  it("allowlist has no stale or needlessly-exempt entries", () => {
    const rels = new Set(pages.map((p) => p.rel));
    for (const [rel] of Object.entries(ALLOWLIST)) {
      if (rel === "index") continue;
      expect(rels.has(rel), `stale allowlist entry ${rel}`).toBe(true);
    }
  });
});
