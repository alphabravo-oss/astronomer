/** Support is independent of publisher, install path, and catalog integrity. */
export type SupportLevel = "Assured" | "Community" | "Experimental";
export interface OfferingSupport {
  level: SupportLevel;
  description: string;
  profile?: string;
  supportUntil?: string;
  evidenceURL?: string;
}

const digest = /^sha256:[a-f0-9]{64}$/;
const checks = [
  "install",
  "functional",
  "upgrade",
  "rollback",
  "uninstall",
  "security",
  "isolation",
  "airgap",
];

export function object(value: unknown): Record<string, unknown> {
  return value !== null && typeof value === "object" && !Array.isArray(value)
    ? (value as Record<string, unknown>)
    : {};
}

export function offeringSupport(
  input?: {
    tier?: string;
    artifact?: Record<string, unknown>;
    verification?: string;
    revoked?: boolean;
    version?: string;
  },
  now = Date.now(),
): OfferingSupport {
  if (input?.tier === "upstream" && !input.revoked)
    return {
      level: "Community",
      description:
        "Astronomer manages deployment. Application behavior and updates are not assured by Astronomer; upstream support terms apply.",
    };
  const experimental: OfferingSupport = {
    level: "Experimental",
    description:
      "No current Astronomer assurance for this configuration. Evaluate before production use.",
  };
  const artifact = input?.artifact ?? {};
  const assurance = object(artifact.assurance);
  const results = object(assurance.checks);
  const until = Date.parse(String(assurance.supportUntil ?? ""));
  const qualified = Date.parse(String(assurance.qualifiedAt ?? ""));
  const valid =
    input?.tier === "astronomer" &&
    !input.revoked &&
    ["verified", "digest-verified"].includes(input.verification ?? "") &&
    typeof artifact.version === "string" &&
    assurance.chartVersion === artifact.version &&
    (!input.version || input.version === artifact.version) &&
    [
      assurance.chartDigest,
      assurance.valuesDigest,
      assurance.evidenceDigest,
    ].every((v) => typeof v === "string" && digest.test(v)) &&
    typeof assurance.profile === "string" &&
    assurance.profile.trim() !== "" &&
    typeof assurance.owner === "string" &&
    assurance.owner.trim() !== "" &&
    typeof assurance.evidenceURL === "string" &&
    /^https:\/\/[^\s]+$/.test(assurance.evidenceURL) &&
    Array.isArray(assurance.images) &&
    assurance.images.length > 0 &&
    assurance.images.every(
      (v) => typeof v === "string" && /^.+@sha256:[a-f0-9]{64}$/.test(v),
    ) &&
    Array.isArray(assurance.environments) &&
    assurance.environments.length > 0 &&
    assurance.environments.every(
      (v) => typeof v === "string" && v.trim() !== "",
    ) &&
    qualified <= now &&
    until > now &&
    until > qualified &&
    checks.every(
      (key) =>
        results[key] === "pass" ||
        (key === "rollback" && results[key] === "not-supported"),
    );
  if (!valid) return experimental;
  return {
    level: "Assured",
    profile: String(assurance.profile),
    supportUntil: String(assurance.supportUntil),
    evidenceURL: String(assurance.evidenceURL),
    description: `Qualified profile: ${assurance.profile}, chart ${artifact.version}. Applies only to the recorded values, images and tested environments. Changed values require requalification.`,
  };
}

export function catalogIntegrity(status?: string, revoked = false): string {
  if (revoked || status === "revoked") return "Catalog revoked";
  if (status === "verified") return "Catalog signature verified";
  if (status === "digest-verified") return "Catalog digest verified";
  if (status === "failed") return "Catalog verification failed";
  return "Catalog signature not verified";
}
