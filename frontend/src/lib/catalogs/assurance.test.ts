import { catalogIntegrity, offeringSupport } from "./assurance";
import { supportForChart } from "./support";

const now = Date.parse("2026-10-12T00:00:00Z");
const profile = {
  profile: "monitoring-small",
  owner: "platform",
  chartVersion: "1.0.0",
  chartDigest: `sha256:${"a".repeat(64)}`,
  valuesDigest: `sha256:${"b".repeat(64)}`,
  evidenceDigest: `sha256:${"c".repeat(64)}`,
  evidenceURL: "https://example.test/report",
  images: [`example/monitor@sha256:${"d".repeat(64)}`],
  environments: ["kind 1.35 amd64"],
  qualifiedAt: "2026-10-10T00:00:00Z",
  supportUntil: "2026-11-10T00:00:00Z",
  checks: Object.fromEntries(
    [
      "install",
      "functional",
      "upgrade",
      "rollback",
      "uninstall",
      "security",
      "isolation",
      "airgap",
    ].map((key) => [key, "pass"]),
  ),
};
const input = {
  tier: "astronomer",
  artifact: { version: "1.0.0", assurance: profile },
  verification: "verified",
};

it("grants only a scoped, current, verified profile", () => {
  const result = offeringSupport(input, now);
  expect(result.level).toBe("Assured");
  expect(result.description).toContain(
    "Changed values require requalification",
  );
  expect(offeringSupport({ ...input, version: "2.0.0" }, now).level).toBe(
    "Experimental",
  );
});

it("does not turn a legacy tier, signature or publisher into assurance", () => {
  expect(
    offeringSupport({ tier: "astronomer", verification: "verified" }, now)
      .level,
  ).toBe("Experimental");
  expect(offeringSupport(undefined, now).level).toBe("Experimental");
  expect(offeringSupport({ tier: "upstream" }, now).level).toBe("Community");
  for (const verification of ["unsigned", "failed", "revoked", ""]) {
    expect(offeringSupport({ ...input, verification }, now).level).toBe(
      "Experimental",
    );
  }
  expect(offeringSupport({ ...input, revoked: true }, now).level).toBe(
    "Experimental",
  );
  expect(supportForChart(undefined, "grafana").level).toBe("Experimental");
});

it("rejects expired, future, incomplete and malformed receipts", () => {
  for (const change of [
    { supportUntil: "2026-10-01T00:00:00Z" },
    { qualifiedAt: "2026-12-01T00:00:00Z" },
    { images: ["image:latest"] },
    { environments: [] },
    { valuesDigest: "invalid" },
    { evidenceURL: "javascript:alert(1)" },
    { owner: "" },
    { checks: { install: "pass" } },
  ]) {
    expect(
      offeringSupport(
        {
          ...input,
          artifact: { version: "1.0.0", assurance: { ...profile, ...change } },
        },
        now,
      ).level,
    ).toBe("Experimental");
  }
});

it("reports catalog integrity separately and never shows an unsigned catalog as verified", () => {
  expect(catalogIntegrity("digest-verified")).toBe("Catalog digest verified");
  expect(catalogIntegrity("unsigned")).toBe("Catalog signature not verified");
  expect(catalogIntegrity("verified", true)).toBe("Catalog revoked");
});

it("requires unambiguous repository identity before showing catalog support", () => {
  const row = {
    chart_name: "monitor",
    repo_name: "trusted",
    repo_url: "https://charts.example.test",
    support_tier: "upstream",
  } as Parameters<typeof supportForChart>[0] extends (infer R)[] | undefined
    ? R
    : never;
  expect(
    supportForChart([row], "monitor", "https://charts.example.test").level,
  ).toBe("Community");
  expect(supportForChart([row], "monitor", "unrelated").level).toBe(
    "Experimental",
  );
  expect(supportForChart([row], "monitor").level).toBe("Experimental");
  expect(supportForChart([row, row], "monitor", "trusted").level).toBe(
    "Experimental",
  );
});
