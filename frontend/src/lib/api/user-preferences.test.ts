import { isNavigableLandingRoute, landingRouteOptions } from "./user-preferences";

it("honors every selectable landing page including Workloads", () => {
  for (const { value } of landingRouteOptions) {
    expect(isNavigableLandingRoute(value)).toBe(value !== "/dashboard");
  }
  expect(isNavigableLandingRoute("/dashboard/workloads")).toBe(true);
  expect(isNavigableLandingRoute("https://example.com")).toBe(false);
});
