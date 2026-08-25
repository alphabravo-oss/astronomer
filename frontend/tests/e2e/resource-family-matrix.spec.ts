import { expect, test, type Page } from "@playwright/test";

import { authMeWire, seedAuth } from "./helpers/auth";

const adminUser = {
  id: "user-admin",
  username: "admin",
  email: "admin@example.com",
  displayName: "Admin User",
  provider: "local",
  globalRoles: ["admin"],
  isSuperuser: true,
  roles: { global: [], cluster: [], project: [] },
  enabled: true,
  lastLogin: new Date().toISOString(),
  createdAt: new Date().toISOString(),
};

const CLUSTER_ID = "cluster-resource-matrix";
const NAMESPACE = "platform";

interface ResourceFamily {
  resourceType: string;
  apiBase: string;
  plural: string;
  kind: string;
  namespaced: boolean;
}

const families: ResourceFamily[] = [
  {
    resourceType: "deployments",
    apiBase: "apis/apps/v1",
    plural: "deployments",
    kind: "Deployment",
    namespaced: true,
  },
  {
    resourceType: "statefulsets",
    apiBase: "apis/apps/v1",
    plural: "statefulsets",
    kind: "StatefulSet",
    namespaced: true,
  },
  {
    resourceType: "daemonsets",
    apiBase: "apis/apps/v1",
    plural: "daemonsets",
    kind: "DaemonSet",
    namespaced: true,
  },
  {
    resourceType: "jobs",
    apiBase: "apis/batch/v1",
    plural: "jobs",
    kind: "Job",
    namespaced: true,
  },
  {
    resourceType: "cronjobs",
    apiBase: "apis/batch/v1",
    plural: "cronjobs",
    kind: "CronJob",
    namespaced: true,
  },
  {
    resourceType: "services",
    apiBase: "api/v1",
    plural: "services",
    kind: "Service",
    namespaced: true,
  },
  {
    resourceType: "ingresses",
    apiBase: "apis/networking.k8s.io/v1",
    plural: "ingresses",
    kind: "Ingress",
    namespaced: true,
  },
  {
    resourceType: "gateways",
    apiBase: "apis/gateway.networking.k8s.io/v1",
    plural: "gateways",
    kind: "Gateway",
    namespaced: true,
  },
  {
    resourceType: "configmaps",
    apiBase: "api/v1",
    plural: "configmaps",
    kind: "ConfigMap",
    namespaced: true,
  },
  {
    resourceType: "secrets",
    apiBase: "api/v1",
    plural: "secrets",
    kind: "Secret",
    namespaced: true,
  },
  {
    resourceType: "persistentvolumeclaims",
    apiBase: "api/v1",
    plural: "persistentvolumeclaims",
    kind: "PersistentVolumeClaim",
    namespaced: true,
  },
  {
    resourceType: "namespaces",
    apiBase: "api/v1",
    plural: "namespaces",
    kind: "Namespace",
    namespaced: false,
  },
  {
    resourceType: "serviceaccounts",
    apiBase: "api/v1",
    plural: "serviceaccounts",
    kind: "ServiceAccount",
    namespaced: true,
  },
  {
    resourceType: "k8s-roles",
    apiBase: "apis/rbac.authorization.k8s.io/v1",
    plural: "roles",
    kind: "Role",
    namespaced: true,
  },
  {
    resourceType: "k8s-rolebindings",
    apiBase: "apis/rbac.authorization.k8s.io/v1",
    plural: "rolebindings",
    kind: "RoleBinding",
    namespaced: true,
  },
  {
    resourceType: "networkpolicies",
    apiBase: "apis/networking.k8s.io/v1",
    plural: "networkpolicies",
    kind: "NetworkPolicy",
    namespaced: true,
  },
  {
    resourceType: "hpa",
    apiBase: "apis/autoscaling/v2",
    plural: "horizontalpodautoscalers",
    kind: "HorizontalPodAutoscaler",
    namespaced: true,
  },
  {
    resourceType: "poddisruptionbudgets",
    apiBase: "apis/policy/v1",
    plural: "poddisruptionbudgets",
    kind: "PodDisruptionBudget",
    namespaced: true,
  },
];

function apiResponse<T>(data: T) {
  return { status: 200, data };
}

function objectName(family: ResourceFamily) {
  return `e2e-${family.resourceType.replace(/^k8s-/, "")}`;
}

function objectPath(family: ResourceFamily) {
  const namespace = family.namespaced ? `/namespaces/${NAMESPACE}` : "";
  return `${family.apiBase}${namespace}/${family.plural}/${objectName(family)}`;
}

function detailPath(family: ResourceFamily) {
  const namespace = family.namespaced ? `/${NAMESPACE}` : "";
  return `/dashboard/clusters/${CLUSTER_ID}/${family.resourceType}${namespace}/${objectName(family)}`;
}

function resourceObject(family: ResourceFamily) {
  return {
    apiVersion:
      family.apiBase === "api/v1"
        ? "v1"
        : family.apiBase.replace(/^apis\//, ""),
    kind: family.kind,
    metadata: {
      name: objectName(family),
      ...(family.namespaced ? { namespace: NAMESPACE } : {}),
      uid: `uid-${family.resourceType}`,
      creationTimestamp: "2026-08-01T00:00:00Z",
      labels: { "app.kubernetes.io/managed-by": "astronomer-e2e" },
    },
    spec: { replicas: 1 },
    status: {
      phase: "Ready",
      conditions: [
        {
          type: "Ready",
          status: "True",
          reason: "Reconciled",
          message: "The fixture is ready",
          lastTransitionTime: "2026-08-01T00:00:00Z",
        },
      ],
    },
  };
}

async function mockApi(page: Page, family: ResourceFamily) {
  await page.route("**/api/v1/**", async (route) => {
    const url = new URL(route.request().url());
    const path =
      url.pathname.replace(/^\/api\/v1/, "").replace(/\/$/, "") || "/";
    const method = route.request().method();

    if (path === "/events/stream")
      return route.fulfill({ status: 204, body: "" });
    if (path === "/auth/me") {
      return route.fulfill({ json: apiResponse(authMeWire(adminUser)) });
    }
    if (path === "/settings/features")
      return route.fulfill({ json: apiResponse({}) });
    if (path === `/clusters/${CLUSTER_ID}` && method === "GET") {
      return route.fulfill({
        json: apiResponse({
          id: CLUSTER_ID,
          name: CLUSTER_ID,
          displayName: "Resource Matrix",
          status: "active",
          health: { status: "active", components: [] },
          labels: {},
          annotations: {},
          isLocal: false,
          createdAt: "2026-08-01T00:00:00Z",
          updatedAt: "2026-08-01T00:00:00Z",
        }),
      });
    }
    if (path === `/clusters/${CLUSTER_ID}/k8s/${objectPath(family)}`) {
      return route.fulfill({ json: resourceObject(family) });
    }
    if (path.includes("/events")) {
      return route.fulfill({
        json: { apiVersion: "v1", kind: "EventList", items: [] },
      });
    }
    if (
      path.endsWith("/replicasets") ||
      path.endsWith("/controllerrevisions") ||
      path.endsWith("/jobs")
    ) {
      return route.fulfill({ json: { items: [] } });
    }
    return route.fulfill({ json: apiResponse([]) });
  });
}

for (const family of families) {
  test(`${family.kind} detail supports overview, conditions, and YAML`, async ({
    context,
    page,
  }) => {
    await mockApi(page, family);
    await seedAuth(context, page, adminUser);
    await page.goto(detailPath(family));

    await expect(
      page.getByRole("heading", { name: objectName(family) }),
    ).toBeVisible();
    await expect(page.getByText(`Kind: ${family.kind}`)).toBeVisible();
    await expect(page.getByText("astronomer-e2e")).toBeVisible();

    const overviewTab = page.getByRole("tab", { name: "Overview" });
    await overviewTab.focus();
    await page.keyboard.press("ArrowRight");
    await expect(page.getByRole("tab", { name: "YAML" })).toHaveAttribute(
      "aria-selected",
      "true",
    );
    await expect(page.getByRole("button", { name: "Edit" })).toBeVisible();

    await page.keyboard.press("ArrowRight");
    await expect(page.getByRole("tab", { name: "Conditions" })).toHaveAttribute(
      "aria-selected",
      "true",
    );
    await expect(page.getByText("Reconciled")).toBeVisible();
    await expect(page.getByText("The fixture is ready")).toBeVisible();
  });
}
