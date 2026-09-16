import {
  shouldRetryQuery,
  shouldThrowQueryError,
} from "@/lib/query-retry";

describe("shouldRetryQuery", () => {
  it.each([400, 401, 403, 404, 409, 422])(
    "does not retry deterministic HTTP %i responses",
    (status) => {
      expect(shouldRetryQuery(0, { status })).toBe(false);
    },
  );

  it.each([408, 429, 500, 502, 503])(
    "retries transient HTTP %i responses within the budget",
    (status) => {
      expect(shouldRetryQuery(0, { response: { status } })).toBe(true);
      expect(shouldRetryQuery(1, { status })).toBe(true);
      expect(shouldRetryQuery(2, { status })).toBe(false);
    },
  );

  it("retries transport errors without an HTTP status within the budget", () => {
    expect(shouldRetryQuery(0, new Error("connection reset"))).toBe(true);
    expect(shouldRetryQuery(2, new Error("connection reset"))).toBe(false);
  });
});

describe("shouldThrowQueryError", () => {
  it.each([500, 502, 503])(
    "routes HTTP %i through the error boundary",
    (status) => {
      expect(shouldThrowQueryError({ status })).toBe(true);
    },
  );

  it.each([400, 401, 403, 404, 409, 422])(
    "leaves HTTP %i available for page-specific states",
    (status) => {
      expect(shouldThrowQueryError({ response: { status } })).toBe(false);
    },
  );

  it("does not throw status-less transport errors during render", () => {
    expect(shouldThrowQueryError(new Error("offline"))).toBe(false);
  });
});
