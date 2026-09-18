import { readFileSync } from "node:fs";
import { resolve } from "node:path";

// High-visibility page actions must use the shared primitive. Raw buttons on
// these pages previously drifted in radius, muted text, disabled treatment,
// loading icons, and dark-mode colors while still looking acceptable in one
// theme. Add newly scrubbed pages here so adoption is ratcheted forward and CI
// prevents them from regressing.
const actionSurfaces = ["src/routes/dashboard/clusters/$id/index.tsx"];

describe("design-system adoption", () => {
  for (const file of actionSurfaces) {
    it(`${file} uses ActionButton for page actions`, () => {
      const source = readFileSync(resolve(process.cwd(), file), "utf8");
      expect(source).toContain("@/components/ui/action-button");
      expect(source).not.toMatch(/<button\b/);
    });
  }
});
