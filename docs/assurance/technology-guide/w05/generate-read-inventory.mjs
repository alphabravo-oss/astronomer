import fs from "node:fs";
import path from "node:path";
import { createRequire } from "node:module";
import { fileURLToPath } from "node:url";

const root = path.resolve(
  path.dirname(fileURLToPath(import.meta.url)),
  "../../../..",
);
const frontend = path.join(root, "frontend");
const ts = createRequire(path.join(frontend, "package.json"))("typescript");
const source = path.join(frontend, "src");
const files = [];
function walk(directory) {
  for (const entry of fs.readdirSync(directory, { withFileTypes: true })) {
    const file = path.join(directory, entry.name);
    if (entry.isDirectory()) walk(file);
    else if (
      /\.tsx?$/.test(file) &&
      !/\.(test|spec)\./.test(file) &&
      !file.includes("/generated/") &&
      !file.endsWith(".gen.ts")
    )
      files.push(file);
  }
}
walk(source);
const apiFiles = files.filter((file) => file.includes("/lib/api/"));
const config = ts.readConfigFile(
  path.join(frontend, "tsconfig.json"),
  ts.sys.readFile,
);
const parsed = ts.parseJsonConfigFileContent(config.config, ts.sys, frontend);
const program = ts.createProgram(apiFiles, parsed.options);
const checker = program.getTypeChecker();
const generated = path.join(source, "lib/api/generated/client.ts");
const metadata = new Map(
  [
    ...fs
      .readFileSync(generated, "utf8")
      .matchAll(/"([^"]+)": \{ method: "([^"]+)", path: "([^"]+)"/g),
  ].map((match) => [
    match[1],
    { operation: match[1], method: match[2], path: match[3] },
  ]),
);
const queries = [];
const adapters = [];
const operationCache = new Map();
function operations(node, active = new Set()) {
  if (
    node.getSourceFile().fileName === generated &&
    metadata.has(node.name?.text)
  )
    return [metadata.get(node.name.text)];
  if (operationCache.has(node)) return operationCache.get(node);
  if (active.has(node)) return [];
  const nextActive = new Set(active).add(node);
  const found = new Map();
  function visit(child) {
    if (ts.isCallExpression(child)) {
      if (
        /^executeOpenAPI/.test(
          ts.isPropertyAccessExpression(child.expression)
            ? child.expression.name.text
            : child.expression.getText(),
        ) &&
        ts.isStringLiteral(child.arguments[0])
      ) {
        const operation = metadata.get(child.arguments[0].text);
        if (operation) found.set(operation.operation, operation);
      }
      let symbol = checker.getSymbolAtLocation(child.expression);
      if (symbol?.flags & ts.SymbolFlags.Alias)
        symbol = checker.getAliasedSymbol(symbol);
      for (const declaration of symbol?.declarations ?? []) {
        if (
          ts.isFunctionDeclaration(declaration) &&
          declaration.body &&
          declaration
            .getSourceFile()
            .fileName.startsWith(path.join(source, "lib"))
        ) {
          for (const operation of operations(declaration, nextActive))
            found.set(operation.operation, operation);
        }
      }
    }
    ts.forEachChild(child, visit);
  }
  visit(node.body);
  const result = [...found.values()];
  operationCache.set(node, result);
  return result;
}
function ownership(name, file, resolved) {
  if (name === "transitionCharlieFinding")
    return {
      kind: "mutation-or-command",
      lifetime:
        "Manually reviewed conditional dispatch selects acknowledge/resolve/dismiss command; its lifetime is independent of reads.",
    };
  if (["streamPodLogs", "subscribeCharlieSessionEvents"].includes(name))
    return {
      kind: "long-lived-stream",
      lifetime: "Caller-owned subscription; outside TanStack query lifetime.",
    };
  if (file.endsWith("authentication-refresh.ts"))
    return {
      kind: "session-lifetime",
      lifetime:
        "Shared authentication owner; cancellation of a waiter cannot cancel refresh.",
    };
  const dynamicRead =
    /^(get|list|downloadComplianceExportBlob|k8sGet|k8sDryRunYaml|pollOperation)/.test(
      name,
    );
  if (
    !resolved.length &&
    [
      "cluster-registration.ts",
      "crd-counts.ts",
      "kubernetes-proxy.ts",
      "kubernetes-resources.ts",
      "rbac-binding-page.ts",
      "rbac-role-page.ts",
      "operation-polling.ts",
      "settings-compliance-export.ts",
    ].some((suffix) => file.endsWith(suffix))
  ) {
    return dynamicRead
      ? {
          kind: "bounded-observation",
          lifetime:
            "Manually reviewed dynamic adapter: read/polling/download/dry-run may cancel observation; existing server operation remains independent.",
        }
      : {
          kind: "mutation-or-command",
          lifetime:
            "Manually reviewed dynamic proxy or workflow: mutating methods retain their caller/operation lifetime; no query cancellation attached.",
        };
  }
  if (!resolved.length)
    return {
      kind: "local-helper",
      lifetime:
        "No statically resolved generated operation; mapping, URLs, error handling, polling or local state. Dynamic boundaries require the separately recorded manual review.",
    };
  const read = resolved.every(
    (operation) =>
      ["GET", "HEAD", "OPTIONS"].includes(operation.method) ||
      /\/preview\/?$/.test(operation.path) ||
      /preview/i.test(operation.operation),
  );
  return read
    ? {
        kind: "bounded-observation",
        lifetime:
          "Read or bounded preview; caller may cancel observation. Returned operation/credential ownership remains on the server.",
      }
    : {
        kind: "mutation-or-command",
        lifetime:
          "Save, command, or mixed workflow retains its own lifetime; never inherit cancellation from an unrelated query. Existing explicit caller options remain compatibility behavior.",
      };
}
for (const file of files) {
  const text = fs.readFileSync(file, "utf8");
  const sf =
    program.getSourceFile(file) ??
    ts.createSourceFile(
      file,
      text,
      ts.ScriptTarget.Latest,
      true,
      ts.ScriptKind.TSX,
    );
  function visit(node) {
    if (ts.isPropertyAssignment(node) && node.name.getText() === "queryFn") {
      const calls = [];
      function collect(child) {
        if (ts.isCallExpression(child)) calls.push(child.expression.getText());
        ts.forEachChild(child, collect);
      }
      collect(node.initializer);
      queries.push({
        file: path.relative(root, file),
        line: sf.getLineAndCharacterOfPosition(node.getStart()).line + 1,
        callees: [...new Set(calls)],
      });
    }
    if (
      apiFiles.includes(file) &&
      ts.isFunctionDeclaration(node) &&
      node.modifiers?.some(
        (modifier) => modifier.kind === ts.SyntaxKind.ExportKeyword,
      )
    ) {
      const resolved = operations(node);
      adapters.push({
        file: path.relative(root, file),
        name: node.name?.text,
        async: node.modifiers.some(
          (modifier) => modifier.kind === ts.SyntaxKind.AsyncKeyword,
        ),
        parameters: node.parameters.map((parameter) => parameter.getText()),
        directSignalParameter: node.parameters.some(
          (parameter) => parameter.name.getText() === "signal",
        ),
        referencesSignal: /\bsignal\b/.test(node.body?.getText() ?? ""),
        ownership: ownership(node.name?.text, file, resolved),
        generatedOperations: resolved,
      });
    }
    ts.forEachChild(node, visit);
  }
  visit(sf);
}
const counts = Object.fromEntries(
  [...new Set(adapters.map((adapter) => adapter.ownership.kind))].map(
    (kind) => [
      kind,
      adapters.filter((adapter) => adapter.ownership.kind === kind).length,
    ],
  ),
);
const manualReview = {
  optionsForwarding: [
    "listCharlieSessions forwards CharlieRequestOptions through getCharlieOverview",
    "getClusterManifest/getClusterManifestWithToken forward RegistrationRequestOptions through fetchClusterManifest",
    "getNativeRulePage forwards OpenAPIRequestOptions through listNativeRules",
  ],
  dynamicBoundaries: [
    "k8sGet/list and Kubernetes-resource helpers use the canonical bounded proxy adapters and explicit read signals; API writes keep their own lifetime.",
    "streamPodLogs and subscribeCharlieSessionEvents are subscriptions whose callers own close/cancellation; neither is a queryFn.",
    "Generated executeOpenAPI takes a runtime operation ID, with canonical operationMetadata supplying the method/path; per-operation generated exports are governed by openapi:check.",
  ],
  qualification:
    "Ownership classification follows statically resolved generated operations and explicit shared/session/stream boundaries. It is not a whole-callgraph or every-branch cancellation proof; real HTTP tests and browser behavior provide representative runtime evidence.",
};
fs.writeFileSync(
  path.join(
    root,
    "docs/assurance/technology-guide/w05/read-boundary-inventory.json",
  ),
  JSON.stringify(
    {
      scope:
        "Every production queryFn property and every exported non-generated API function declaration, including synchronous wrappers, helpers, streams and mutations.",
      counts,
      manualReview,
      queries,
      adapters,
    },
    null,
    2,
  ) + "\n",
);
console.log(
  JSON.stringify({
    queries: queries.length,
    apiExports: adapters.length,
    ownership: counts,
  }),
);
