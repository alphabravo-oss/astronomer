#!/usr/bin/env node
import fs from "node:fs";
import path from "node:path";
import { createRequire } from "node:module";
import { fileURLToPath } from "node:url";
const repoRoot = path.resolve(
  path.dirname(fileURLToPath(import.meta.url)),
  "..",
);
const require = createRequire(path.join(repoRoot, "frontend/package.json"));
const ts = require("typescript");

/** Parse every queryFn, including multiline functions and colocated components.
 * Consumption requires forwarding a reference in a call's arguments; merely
 * accepting or logging a signal does not satisfy the read transport contract.
 * SSE/WebSocket streams are separate subscription lifetimes, not queryFns.
 */
export function findDroppedQuerySignals(source) {
  const tree = ts.createSourceFile(
    "query.tsx",
    source,
    ts.ScriptTarget.Latest,
    true,
    ts.ScriptKind.TSX,
  );
  const findings = [];
  const names = (pattern) => {
    if (ts.isIdentifier(pattern)) return [pattern.text];
    return (
      pattern.elements?.flatMap((element) =>
        element.name ? names(element.name) : [],
      ) ?? []
    );
  };
  const queryBinding = (fn) => {
    const parameter = fn.parameters[0];
    if (!parameter) return undefined;
    if (ts.isIdentifier(parameter.name))
      return { context: parameter.name.text };
    if (ts.isObjectBindingPattern(parameter.name)) {
      const signal = parameter.name.elements.find(
        (element) =>
          (element.propertyName?.getText(tree) ??
            element.name.getText(tree)) === "signal",
      );
      if (signal && ts.isIdentifier(signal.name))
        return { signal: signal.name.text };
    }
    return undefined;
  };
  const referencesBinding = (node, fn, binding) => {
    const identity = (identifier, name) => {
      if (identifier.text !== name) return false;
      for (
        let scope = identifier.parent;
        scope && scope !== fn;
        scope = scope.parent
      ) {
        if (
          (ts.isArrowFunction(scope) ||
            ts.isFunctionExpression(scope) ||
            ts.isFunctionDeclaration(scope)) &&
          scope.parameters.some((parameter) =>
            names(parameter.name).includes(name),
          )
        )
          return false;
        if (
          ts.isBlock(scope) &&
          scope.statements.some(
            (statement) =>
              ts.isVariableStatement(statement) &&
              statement.declarationList.declarations.some((declaration) =>
                names(declaration.name).includes(name),
              ),
          )
        )
          return false;
      }
      return true;
    };
    let found = false;
    const visit = (child) => {
      if (
        binding.signal &&
        ts.isIdentifier(child) &&
        identity(child, binding.signal) &&
        !(ts.isPropertyAssignment(child.parent) && child.parent.name === child)
      )
        found = true;
      if (
        binding.context &&
        ts.isPropertyAccessExpression(child) &&
        child.name.text === "signal" &&
        ts.isIdentifier(child.expression) &&
        identity(child.expression, binding.context)
      )
        found = true;
      ts.forEachChild(child, visit);
    };
    visit(node);
    return found;
  };
  const forwarded = (fn, binding) => {
    let found = false;
    const visit = (child) => {
      if (ts.isCallExpression(child)) {
        const callee = ts.isPropertyAccessExpression(child.expression)
          ? child.expression.name.getText(tree)
          : child.expression.getText(tree);
        // Read adapters and bounded read helpers have explicit read names.
        // Logging, Promise combinators, and arbitrary side effects cannot prove forwarding.
        const read =
          /^(get|list|fetch|search|read|k8s|preview|b2[A-Z]|executeOpenAPI)/.test(
            callee,
          );
        if (
          read &&
          child.arguments.some((argument) =>
            referencesBinding(argument, fn, binding),
          )
        )
          found = true;
      }
      ts.forEachChild(child, visit);
    };
    visit(fn.body);
    return found;
  };
  const visit = (node) => {
    if (
      ts.isPropertyAssignment(node) &&
      node.name.getText(tree).replace(/['"]/g, "") === "queryFn"
    ) {
      const fn = node.initializer;
      if (
        !(ts.isArrowFunction(fn) || ts.isFunctionExpression(fn)) ||
        !queryBinding(fn) ||
        !forwarded(fn, queryBinding(fn))
      ) {
        findings.push({
          line:
            tree.getLineAndCharacterOfPosition(node.getStart(tree)).line + 1,
          snippet: node.getText(tree).slice(0, 160),
        });
      }
    }
    ts.forEachChild(node, visit);
  };
  visit(tree);
  return findings;
}

export function discoverQueryFiles(directory) {
  return fs.readdirSync(directory, { withFileTypes: true }).flatMap((entry) => {
    const file = path.join(directory, entry.name);
    if (entry.isDirectory()) return discoverQueryFiles(file);
    return /\.tsx?$/.test(file) &&
      !/\.(test|spec)\./.test(file) &&
      !file.endsWith(".gen.ts") &&
      !file.includes(`${path.sep}generated${path.sep}`)
      ? [file]
      : [];
  });
}

function main() {
  const files = discoverQueryFiles(path.join(repoRoot, "frontend/src"));
  const failures = files.flatMap((file) =>
    findDroppedQuerySignals(fs.readFileSync(file, "utf8")).map(
      (f) => `${path.relative(repoRoot, file)}:${f.line}: ${f.snippet}`,
    ),
  );
  if (failures.length) {
    console.error(
      `TanStack queries must accept and forward signal:\n${failures.join("\n")}`,
    );
    process.exitCode = 1;
  } else
    console.log(
      `frontend query cancellation contract passed: ${files.length} source modules discovered`,
    );
}
if (
  process.argv[1] &&
  path.resolve(process.argv[1]) === fileURLToPath(import.meta.url)
)
  main();
