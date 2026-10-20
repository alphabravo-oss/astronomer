import { expect, test } from "@playwright/test";

// Observability contract (guide ch.05): the running binary exposes
// service_build_info and the RED counters on its metrics listener.
test("server exposes service_build_info on the metrics listener", async ({ request }) => {
  const metricsURL = process.env.PLAYWRIGHT_METRICS_URL ?? "http://127.0.0.1:9090/metrics";
  const res = await request.get(metricsURL);
  expect(res.status()).toBe(200);
  const text = await res.text();
  expect(text).toMatch(/^service_build_info\{.*version=.*\} 1$/m);
  expect(text).toContain("astronomer_http_requests_total");
});
