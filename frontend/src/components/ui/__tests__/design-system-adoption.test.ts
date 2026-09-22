import { readFileSync, readdirSync } from "node:fs";
import { resolve } from "node:path";

// High-visibility page actions must use the shared primitive. Raw buttons on
// these pages previously drifted in radius, muted text, disabled treatment,
// loading icons, and dark-mode colors while still looking acceptable in one
// theme. Add newly scrubbed pages here so adoption is ratcheted forward and CI
// prevents them from regressing.
const actionSurfaces = [
  "src/routes/dashboard/clusters/$id/-page.tsx",
  "src/routes/dashboard/account/security/index.tsx",
  "src/routes/dashboard/delivery/targets/$targetId/index.tsx",
  "src/routes/dashboard/delivery/targets/index.tsx",
  "src/routes/dashboard/settings/widgets/index.tsx",
  "src/routes/dashboard/delivery/sources/index.tsx",
];

describe("design-system adoption", () => {
  for (const file of actionSurfaces) {
    it(`${file} uses ActionButton for page actions`, () => {
      const source = readFileSync(resolve(process.cwd(), file), "utf8");
      expect(source).toContain("@/components/ui/action-button");
      expect(source).not.toMatch(/<button\b/);
    });
  }
});

/**
 * Counting ratchets (plan 021): raw `<button` and the hand-rolled
 * `border border-border bg-card` card-frame literal must never grow across
 * src/routes. These are drift indicators for ActionButton/Card adoption —
 * migrating a page should lower the baseline (see docs/design-system.md);
 * a raw new usage should not raise it.
 */
function walkRouteFiles(dir: string): string[] {
  const out: string[] = [];
  for (const entry of readdirSync(dir, { withFileTypes: true })) {
    if (entry.name === "__tests__") continue;
    const path = resolve(dir, entry.name);
    if (entry.isDirectory()) {
      out.push(...walkRouteFiles(path));
    } else if (
      entry.name.endsWith(".tsx") &&
      !entry.name.includes(".test.") &&
      entry.name !== "routeTree.gen.ts"
    ) {
      out.push(path);
    }
  }
  return out;
}

function countAcrossRoutes(pattern: RegExp): number {
  const root = resolve(process.cwd(), "src/routes");
  let count = 0;
  for (const file of walkRouteFiles(root)) {
    const source = readFileSync(file, "utf8");
    const matches = source.match(pattern);
    if (matches) count += matches.length;
  }
  return count;
}

// Measured 2026-09-21 when the ratchet was added (plan 021 step 8).
// Lowered 2026-09-21 after plan 022 step 4's button/card migration of the
// top-5 raw-<button> files (account/security, delivery targets ×2,
// settings/widgets, delivery/sources).
const BASELINE_BUTTONS = 196;
const BASELINE_CARD_FRAME_LITERALS = 116;

describe("design-system adoption ratchets", () => {
  it("does not add raw <button> elements under src/routes", () => {
    expect(countAcrossRoutes(/<button\b/g)).toBeLessThanOrEqual(
      BASELINE_BUTTONS,
    );
  });

  it("does not add hand-rolled card-frame literals under src/routes", () => {
    expect(
      countAcrossRoutes(/border border-border bg-card/g),
    ).toBeLessThanOrEqual(BASELINE_CARD_FRAME_LITERALS);
  });
});
