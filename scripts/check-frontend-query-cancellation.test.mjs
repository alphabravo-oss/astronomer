import assert from "node:assert/strict";
import test from "node:test";

import { findDroppedQuerySignals } from "./check-frontend-query-cancellation.mjs";

test("rejects anonymous and direct query functions that drop cancellation", () => {
  const findings = findDroppedQuerySignals(`
    queryFn: () => listThings(),
    queryFn: async () => getThing(),
    queryFn: listThings,
  `);
  assert.equal(findings.length, 3);
});

test("accepts query functions that consume a signal", () => {
  const findings = findDroppedQuerySignals(`
    queryFn: ({ signal }) => listThings(signal),
    queryFn: async ({ pageParam, signal }) => listPage(pageParam, signal),
  `);
  assert.deepEqual(findings, []);
});
