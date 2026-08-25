import { describe, expect, it } from "vitest";

import {
  CREATABLE_GENERIC_RESOURCES,
  RESOURCE_TITLES,
  WORKLOAD_KINDS,
  WORKLOAD_TEMPLATE_BY_KIND,
  isGenericResourceType,
} from "./resource-route-config";

describe("resource route configuration", () => {
  it("gives every generic resource a title", () => {
    const genericTypes = [
      "jobs",
      "cronjobs",
      "configmaps",
      "secrets",
      "hpa",
      "resourcequotas",
      "limitranges",
      "poddisruptionbudgets",
      "crds",
      "serviceaccounts",
      "k8s-clusterroles",
      "k8s-clusterrolebindings",
      "k8s-roles",
      "k8s-rolebindings",
      "endpoints",
      "replicasets",
    ];

    expect(genericTypes.every(isGenericResourceType)).toBe(true);
    expect(genericTypes.every((type) => Boolean(RESOURCE_TITLES[type]))).toBe(
      true,
    );
  });

  it("limits create templates to resources handled by the generic adapter", () => {
    expect(
      Object.keys(CREATABLE_GENERIC_RESOURCES).every(isGenericResourceType),
    ).toBe(true);
  });

  it("maps every specialized workload route to a supported create template", () => {
    for (const kind of Object.values(WORKLOAD_KINDS)) {
      if (kind === "Job" || kind === "CronJob") continue;
      expect(WORKLOAD_TEMPLATE_BY_KIND[kind]).toBeTruthy();
    }
  });

  it("exposes immutable route dictionaries", () => {
    expect(Object.isFrozen(RESOURCE_TITLES)).toBe(true);
    expect(Object.isFrozen(WORKLOAD_KINDS)).toBe(true);
    expect(Object.isFrozen(CREATABLE_GENERIC_RESOURCES)).toBe(true);
  });
});
