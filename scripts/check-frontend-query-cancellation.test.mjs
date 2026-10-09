import assert from "node:assert/strict";
import test from "node:test";

import { findDroppedQuerySignals } from "./check-frontend-query-cancellation.mjs";

test("rejects anonymous and direct query functions that drop cancellation", () => {
  const findings = findDroppedQuerySignals(`({
    queryFn: () => listThings(),
    queryFn: async () => getThing(),
    queryFn: listThings,
  })`);
  assert.equal(findings.length, 3);
});

test("accepts query functions that consume a signal", () => {
  const findings = findDroppedQuerySignals(`({
    queryFn: ({ signal }) => listThings(signal),
    queryFn: async ({ pageParam, signal }) => listPage(pageParam, signal),
  })`);
  assert.deepEqual(findings, []);
});

test("rejects a declared but dropped signal and multiline function expressions", () => {
  const findings = findDroppedQuerySignals(`({
    queryFn: ({ signal }) => listThings(),
    queryFn: async function ({ signal }) { return getThing(); },
    queryFn: ({ signal }) => { console.log(signal); return getThing(); },
  })`);
  assert.equal(findings.length, 3);
});

test("discovers read hooks in dash-prefixed components and adapters without a filename allowlist", async () => {
  const fs = await import("node:fs");
  const os = await import("node:os");
  const path = await import("node:path");
  const { discoverQueryFiles } =
    await import("./check-frontend-query-cancellation.mjs");
  const root = fs.mkdtempSync(path.join(os.tmpdir(), "query-discovery-"));
  try {
    fs.mkdirSync(path.join(root, "nested"));
    fs.writeFileSync(
      path.join(root, "nested", "-modal.tsx"),
      "queryFn: () => read()",
    );
    fs.writeFileSync(path.join(root, "arbitrary.ts"), "queryFn: () => read()");
    fs.writeFileSync(path.join(root, "ignored.test.ts"), "");
    assert.equal(discoverQueryFiles(root).length, 2);
  } finally {
    fs.rmSync(root, { recursive: true });
  }
});

test("rejects unrelated signals, logging, and nested shadow bindings", () => {
  const findings = findDroppedQuerySignals(`({
    queryFn: ({ signal }) => { console.warn(signal); return getThings(); },
    queryFn: ({ signal }) => { console.info(signal); return getThings(); },
    queryFn: ({ signal }) => { observe(signal); return getThings(); },
    queryFn: (context) => getThings(new AbortController().signal),
    queryFn: ({ signal }) => { return items.map((signal) => getThing(signal)); },
    queryFn: (context) => { const signal = new AbortController().signal; return getThings(signal); },
    queryFn: ({ signal }) => { return getThings({ signal: otherSignal }); },
  })`);
  assert.equal(findings.length, 7);
});

test("accepts the actual query binding, destructuring aliases and context.signal", () => {
  assert.deepEqual(
    findDroppedQuerySignals(`({
    queryFn: ({ signal: cancellation }) => getThings({ signal: cancellation }),
    queryFn: (context) => getThings({ signal: context.signal }),
    queryFn: ({ signal }) => Promise.all(items.map((item) => getThing(item, signal))),
  })`),
    [],
  );
});
