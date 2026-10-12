/**
 * FormErrorSummary adoption ratchet (P023.5): every TanStack Router route
 * or colocated component that builds a `useAppForm` must render `FormErrorSummary`
 * so validation and server failures are visible inline, not toast-only.
 */
import { readFileSync, readdirSync } from "node:fs";
import { resolve } from "node:path";
import { describe, expect, it } from "vitest";
import ts from "typescript";

function walk(dir: string): string[] {
  const files: string[] = [];
  for (const entry of readdirSync(dir, { withFileTypes: true })) {
    const path = resolve(dir, entry.name);
    if (entry.isDirectory()) files.push(...walk(path));
    else if (entry.name.endsWith(".tsx")) files.push(path);
  }
  return files;
}

describe("FormErrorSummary adoption on route files", () => {
  const routesRoot = resolve(process.cwd(), "src/routes");
  const routeFiles = walk(routesRoot).filter((file) => {
    const base = file.split("/").pop() ?? "";
    return !/\.test\.tsx$/.test(base);
  });

  const formFiles = routeFiles.filter((file) =>
    readFileSync(file, "utf8").includes("useAppForm"),
  );

  // Sanity check: fail loudly if the walk ever comes back empty (a broken
  // glob would otherwise make every per-file assertion below vacuously pass).
  it("finds at least one route file using useAppForm", () => {
    expect(formFiles.length).toBeGreaterThan(0);
  });

  for (const file of formFiles) {
    it(`${file.slice(resolve(process.cwd(), "src").length)} renders FormErrorSummary`, () => {
      const source = readFileSync(file, "utf8");
      const tree = ts.createSourceFile(
        file,
        source,
        ts.ScriptTarget.Latest,
        true,
        ts.ScriptKind.TSX,
      );
      let rendersSummary = false;
      let clickSubmissions = 0;
      const visit = (node: ts.Node) => {
        if (
          (ts.isJsxOpeningElement(node) || ts.isJsxSelfClosingElement(node)) &&
          /(?:^|\.)FormErrorSummary$/.test(node.tagName.getText(tree))
        )
          rendersSummary = true;
        if (
          ts.isJsxAttribute(node) &&
          node.name.getText(tree) === "onClick" &&
          node.initializer?.getText(tree).includes("handleSubmit")
        )
          clickSubmissions++;
        ts.forEachChild(node, visit);
      };
      visit(tree);
      expect(rendersSummary).toBe(true);
      // Colocated modal submit controls must use the semantic boundary.
      if ((file.split("/").pop() ?? "").startsWith("-"))
        expect(clickSubmissions).toBe(0);
    });
  }
});
