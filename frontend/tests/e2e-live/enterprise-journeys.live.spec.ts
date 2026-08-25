import { expect, test } from "@playwright/test";
import { createHash, X509Certificate } from "node:crypto";
import { readFile, writeFile } from "node:fs/promises";
import { load as loadYaml } from "js-yaml";

import { loginViaForm, loginViaFormAs } from "./live.helpers";

interface DownloadedKubeconfig {
  apiVersion?: string;
  kind?: string;
  clusters?: Array<{
    name?: string;
    cluster?: {
      server?: string;
      "certificate-authority-data"?: string;
      "insecure-skip-tls-verify"?: boolean;
    };
  }>;
  contexts?: Array<{
    name?: string;
    context?: { cluster?: string; user?: string };
  }>;
  "current-context"?: string;
  users?: Array<{
    name?: string;
    user?: {
      token?: string;
      exec?: unknown;
      "client-key-data"?: string;
      "client-certificate-data"?: string;
    };
  }>;
}

const clusterID = process.env.LIVE_FIXTURE_CLUSTER_ID!;
const projectID = process.env.LIVE_FIXTURE_PROJECT_ID!;
const rolloutID = process.env.LIVE_FIXTURE_ROLLOUT_ID!;
const rollbackRolloutID = process.env.LIVE_FIXTURE_ROLLBACK_ROLLOUT_ID!;
const trivyEnabled = process.env.LIVE_FIXTURE_TRIVY_ENABLED === "1";
const trivyRolloutID = process.env.LIVE_FIXTURE_TRIVY_ROLLOUT_ID ?? "";
const loggingOutput = process.env.LIVE_FIXTURE_LOGGING_OUTPUT!;
const backupNamespace = process.env.LIVE_FIXTURE_BACKUP_NAMESPACE!;
const backupConfigMap = process.env.LIVE_FIXTURE_BACKUP_CONFIGMAP!;
const backupMessage = process.env.LIVE_FIXTURE_BACKUP_MESSAGE!;
const restrictedEmail = process.env.LIVE_RESTRICTED_EMAIL!;
const restrictedPassword = process.env.LIVE_RESTRICTED_PASSWORD!;
const directEndpoint = process.env.LIVE_FIXTURE_DIRECT_ENDPOINT!;
const directCASHA256 = process.env.LIVE_FIXTURE_DIRECT_CA_SHA256!;
const directKubeconfigPath = process.env.LIVE_FIXTURE_DIRECT_KUBECONFIG_PATH!;

test.beforeAll(() => {
  for (const [name, value] of Object.entries({
    LIVE_FIXTURE_CLUSTER_ID: clusterID,
    LIVE_FIXTURE_PROJECT_ID: projectID,
    LIVE_FIXTURE_ROLLOUT_ID: rolloutID,
    LIVE_FIXTURE_ROLLBACK_ROLLOUT_ID: rollbackRolloutID,
    LIVE_FIXTURE_LOGGING_OUTPUT: loggingOutput,
    LIVE_FIXTURE_BACKUP_NAMESPACE: backupNamespace,
    LIVE_FIXTURE_BACKUP_CONFIGMAP: backupConfigMap,
    LIVE_FIXTURE_BACKUP_MESSAGE: backupMessage,
    LIVE_RESTRICTED_EMAIL: restrictedEmail,
    LIVE_RESTRICTED_PASSWORD: restrictedPassword,
    LIVE_FIXTURE_DIRECT_ENDPOINT: directEndpoint,
    LIVE_FIXTURE_DIRECT_CA_SHA256: directCASHA256,
    LIVE_FIXTURE_DIRECT_KUBECONFIG_PATH: directKubeconfigPath,
  })) {
    expect(
      value,
      `${name} must be supplied by scripts/test-live-browser.sh`,
    ).toBeTruthy();
  }
  if (trivyEnabled) {
    expect(
      trivyRolloutID,
      "LIVE_FIXTURE_TRIVY_ROLLOUT_ID must identify the Flux-native optional-tool rollout",
    ).toBeTruthy();
  }
});

test("RBAC denial is explicit and hides privileged controls", async ({
  page,
}) => {
  await loginViaFormAs(page, restrictedEmail, restrictedPassword);
  await page.goto("/dashboard/cluster-templates");

  await expect(
    page.getByText("Permission required", { exact: true }),
  ).toBeVisible();
  await expect(
    page.getByText("cluster_templates:read", { exact: true }),
  ).toBeVisible();
  await expect(page.getByRole("button", { name: /create/i })).toHaveCount(0);
});

test("CIS scan shows running state and durable completed findings", async ({
  page,
}) => {
  await loginViaForm(page);
  await page.goto("/dashboard/security/scans/new");

  await page.getByRole("button", { name: /Live Browser Cluster/ }).click();
  await page.getByRole("button", { name: "Next" }).click();
  await expect(page.getByText("cis-1.8", { exact: true })).toBeVisible();
  await page.getByRole("button", { name: "Next" }).click();
  await page.getByRole("button", { name: "Run Scan" }).click();

  await expect(page).toHaveURL(/\/dashboard\/security\/scans\/[0-9a-f-]+/);
  await expect(
    page.getByText("Polling for results", { exact: true }),
  ).toBeVisible();
  // The durable task-outbox dispatcher runs every 15s and its first tick is
  // scheduler-relative, so allow two dispatch windows plus tunnel ingestion.
  await expect(page.getByText("Completed", { exact: true })).toBeVisible({
    timeout: 60_000,
  });
  await expect(
    page.getByText("Anonymous authentication disabled", { exact: true }),
  ).toBeVisible();
});

test("system Loki query returns tenant-scoped tunnel results", async ({
  page,
}) => {
  await loginViaForm(page);
  await page.goto("/dashboard/logging");

  await page.getByRole("button", { name: `Query ${loggingOutput}` }).click();
  await page
    .locator("#logging-query")
    .fill('{namespace="payments"} |= "enterprise"');
  await page.getByRole("button", { name: "Run query" }).click();

  const results = page.getByRole("region", { name: "Log query results" });
  await expect(results).toBeVisible();
  await expect(results).toContainText("astronomer_loki");
  await expect(results).toContainText("enterprise-live-query-result");
});

test("delivery rollout exposes durable fenced progress", async ({ page }) => {
  await loginViaForm(page);
  await page.goto(
    `/dashboard/clusters/${clusterID}/delivery/rollouts/${rolloutID}?project=${projectID}`,
  );

  await expect(
    page.getByText(rolloutID, { exact: true }).first(),
  ).toBeVisible();
  await expect(page.getByText(/immutable plan sha256:/)).toBeVisible();
  await expect(page.getByText("Fence", { exact: true })).toBeVisible();
  await expect(
    page.getByRole("heading", { name: "Per-cluster progress" }),
  ).toBeVisible();
});

test("delivery reconciliation is visible through the real member API", async ({
  page,
}) => {
  await loginViaForm(page);
  const resourcePath = `/dashboard/clusters/${clusterID}/configmaps/live-delivery/flux-kustomize-managed`;

  await page.goto(resourcePath);
  await expect(
    page.getByText("flux-kustomize-managed", { exact: true }).first(),
  ).toBeVisible();
  await expect(page.getByText("desired", { exact: true }).first()).toBeVisible({
    timeout: 60_000,
  });
});

if (trivyEnabled) {
  test("optional Flux-native Trivy delivery is current and its real report reaches API and UI", async ({
    page,
  }) => {
    test.setTimeout(120_000);
    await loginViaForm(page);

    await page.goto(
      `/dashboard/clusters/${clusterID}/delivery/rollouts/${trivyRolloutID}?project=${projectID}`,
    );
    await expect(
      page.getByText(trivyRolloutID, { exact: true }).first(),
    ).toBeVisible();
    await expect(page.getByText(/^succeeded$/i).first()).toBeVisible();

    const response = await page.request.get(
      `/api/v1/clusters/${clusterID}/vulnerabilities/images/?namespace=live-delivery`,
    );
    expect(response.ok()).toBeTruthy();
    const payload = (await response.json()) as {
      data?: Array<{
        cluster_id?: string;
        namespace?: string;
        image_repo?: string;
      }>;
    };
    expect(payload.data).toEqual(
      expect.arrayContaining([
        expect.objectContaining({
          cluster_id: clusterID,
          namespace: "live-delivery",
          image_repo: expect.stringContaining("alpine"),
        }),
      ]),
    );

    await page.goto(`/dashboard/clusters/${clusterID}/image-scans`);
    await expect(
      page.getByRole("heading", { name: "Image Scans" }),
    ).toBeVisible();
    await expect(
      page.getByRole("cell", { name: /alpine/ }).first(),
    ).toBeVisible();
    await expect(
      page.getByRole("cell", { name: "live-delivery" }).first(),
    ).toBeVisible();
  });
}

test("resource YAML dry-run gates and then applies the reviewed manifest", async ({
  page,
}) => {
  await loginViaForm(page);
  await page.goto(
    `/dashboard/clusters/${clusterID}/configmaps/default/live-config`,
  );
  await page.getByRole("tab", { name: "YAML" }).click();
  await page.getByRole("button", { name: "Edit" }).click();

  // JSON is valid YAML and avoids Monaco's interactive auto-indentation
  // changing whitespace while Playwright inserts a multiline document.
  const manifest = JSON.stringify({
    apiVersion: "v1",
    kind: "ConfigMap",
    metadata: {
      name: "live-config",
      namespace: "default",
      labels: { "fixture-stage": "applied" },
    },
    data: { message: "after-live-apply" },
  });
  await page.locator(".monaco-editor").click();
  await page.keyboard.press("Control+A");
  await page.keyboard.press("Backspace");
  // Start with a YAML comment so Monaco does not auto-close the opening JSON
  // quote/brace around the bulk insertion.
  await page.keyboard.insertText(`# live-browser apply fixture\n${manifest}\n`);
  await page.getByRole("button", { name: "Dry run" }).click();
  await expect(page.getByText("Dry run passed", { exact: true })).toBeVisible();
  await expect(page.getByText("Apply preview", { exact: true })).toBeVisible();
  await page.getByRole("button", { name: "Save" }).click();
  await expect(
    page.getByText("Resource updated", { exact: true }),
  ).toBeVisible();
  await expect(page.locator(".view-lines")).toContainText("after-live-apply");
});

test("proxy kubeconfig download is structurally valid and secret-safe in output", async ({
  page,
}) => {
  await loginViaForm(page);
  await page.goto(`/dashboard/clusters/${clusterID}`);

  const downloadPromise = page.waitForEvent("download");
  await page.getByRole("button", { name: "Proxy kubeconfig" }).click();
  const download = await downloadPromise;
  expect(download.suggestedFilename()).toBe(
    "live-browser-cluster-proxy-kubeconfig.yaml",
  );
  const path = await download.path();
  expect(path).toBeTruthy();
  const config = loadYaml(
    await readFile(path!, "utf8"),
  ) as DownloadedKubeconfig;
  expect(config.apiVersion).toBe("v1");
  expect(config.kind).toBe("Config");
  expect(config.clusters?.[0]?.cluster?.server).toContain(
    `/api/v1/clusters/${clusterID}/k8s`,
  );
  expect(config.users?.[0]?.user?.token).toMatch(/^.{20,}$/);
  expect(config.users?.[0]?.user?.token).not.toBe("REPLACE_WITH_API_TOKEN");
});

test("direct kubeconfig is permission-aware, TLS-pinned, short-lived, and read-only", async ({
  browser,
  page,
}) => {
  const restrictedContext = await browser.newContext();
  const restrictedPage = await restrictedContext.newPage();
  await loginViaFormAs(restrictedPage, restrictedEmail, restrictedPassword);
  await restrictedPage.goto(`/dashboard/clusters/${clusterID}`);
  await expect(
    restrictedPage.getByText("Cluster not found", { exact: true }),
  ).toBeVisible();
  await expect(
    restrictedPage.getByRole("button", { name: "Direct kubeconfig" }),
  ).toHaveCount(0);
  await restrictedContext.close();

  await loginViaForm(page);
  await page.goto(`/dashboard/clusters/${clusterID}`);
  const directButton = page.getByRole("button", {
    name: "Direct kubeconfig",
  });
  await expect(directButton).toBeEnabled();
  await expect(directButton).toHaveAttribute("title", /15 minutes/i);

  const downloadPromise = page.waitForEvent("download");
  const responsePromise = page.waitForResponse(
    (response) =>
      response.request().method() === "POST" &&
      response
        .url()
        .includes(`/api/v1/clusters/${clusterID}/generate-direct-kubeconfig`),
  );
  await directButton.click();
  const [download, response] = await Promise.all([
    downloadPromise,
    responsePromise,
  ]);
  expect(response.status()).toBe(200);
  expect(response.headers()["cache-control"]).toContain("no-store");
  expect(response.headers()["x-astronomer-kubeconfig-mode"]).toBe("direct");
  const headerExpiry = Date.parse(
    response.headers()["x-astronomer-credential-expires-at"] ?? "",
  );
  expect(headerExpiry - Date.now()).toBeGreaterThan(13 * 60 * 1000);
  expect(headerExpiry - Date.now()).toBeLessThanOrEqual(16 * 60 * 1000);
  expect(download.suggestedFilename()).toBe(
    "live-browser-cluster-direct-kubeconfig.yaml",
  );
  const path = await download.path();
  expect(path).toBeTruthy();
  const raw = await readFile(path!, "utf8");
  const config = loadYaml(raw) as DownloadedKubeconfig;
  const cluster = config.clusters?.[0];
  const user = config.users?.[0];
  const context = config.contexts?.[0];
  expect(config.apiVersion).toBe("v1");
  expect(config.kind).toBe("Config");
  expect(cluster?.name).toBe("live-browser-cluster");
  expect(cluster?.cluster?.server).toBe(directEndpoint);
  expect(cluster?.cluster?.["insecure-skip-tls-verify"]).toBeUndefined();
  const ca = Buffer.from(
    cluster?.cluster?.["certificate-authority-data"] ?? "",
    "base64",
  );
  expect(
    createHash("sha256").update(new X509Certificate(ca).raw).digest("hex"),
  ).toBe(directCASHA256);
  expect(user?.name).toBe("astronomer-direct-reader");
  expect(context?.context?.user).toBe("astronomer-direct-reader");
  expect(context?.context?.cluster).toBe("live-browser-cluster");
  expect(config["current-context"]).toBe("live-browser-cluster-direct");
  expect(user?.user?.exec).toBeUndefined();
  expect(user?.user?.["client-key-data"]).toBeUndefined();
  expect(user?.user?.["client-certificate-data"]).toBeUndefined();

  const token = user?.user?.token ?? "";
  const tokenParts = token.split(".");
  expect(tokenParts).toHaveLength(3);
  const claims = JSON.parse(
    Buffer.from(tokenParts[1]!, "base64url").toString("utf8"),
  ) as { exp?: number; iat?: number; sub?: string };
  expect(claims.sub).toBe(
    "system:serviceaccount:astronomer-system:astronomer-direct-reader",
  );
  expect((claims.exp ?? 0) - (claims.iat ?? 0)).toBe(15 * 60);
  expect((claims.exp ?? 0) * 1000 - Date.now()).toBeGreaterThan(13 * 60 * 1000);
  expect((claims.exp ?? 0) * 1000 - Date.now()).toBeLessThanOrEqual(
    16 * 60 * 1000,
  );
  expect(raw).not.toContain("REPLACE_WITH_API_TOKEN");
  expect(raw).not.toContain("insecure-skip-tls-verify");
  await writeFile(directKubeconfigPath, raw, { mode: 0o600 });
  await download.delete();
});

test("succeeded delivery rollout accepts a known-good rollback", async ({
  page,
}) => {
  await loginViaForm(page);
  await page.goto(
    `/dashboard/clusters/${clusterID}/delivery/rollouts/${rollbackRolloutID}?project=${projectID}`,
  );

  await expect(
    page.getByText("succeeded", { exact: true }).first(),
  ).toBeVisible();
  await page.getByRole("button", { name: "rollback", exact: true }).click();
  await page.locator('textarea[name="reason"]').fill("live_browser_rollback");
  await page.getByRole("button", { name: "Confirm rollback" }).click();
  await expect(
    page.getByText("Rollout rollback accepted", { exact: true }),
  ).toBeVisible();
  await expect(
    page.getByText("rolling back", { exact: true }).first(),
  ).toBeVisible();
});

test("workload snapshot restores deleted member data through the durable Velero workflow", async ({
  page,
}) => {
  test.setTimeout(300_000);
  await loginViaForm(page);
  const snapshotsPath = `/dashboard/clusters/${clusterID}/snapshots`;
  const resourcePath = `/dashboard/clusters/${clusterID}/configmaps/${backupNamespace}/${backupConfigMap}`;

  await page.goto(snapshotsPath);
  await expect(
    page.getByRole("heading", { name: "Snapshots", exact: true }),
  ).toBeVisible();
  await expect(
    page.getByText("Backup storage location not ready", { exact: true }),
  ).toHaveCount(0);
  await page.getByRole("button", { name: "New Snapshot" }).click();
  await expect(
    page.getByRole("heading", { name: "New snapshot" }),
  ).toBeVisible();
  await page.getByPlaceholder("Filter namespaces…").fill(backupNamespace);
  await page.getByText(backupNamespace, { exact: true }).click();
  await page.getByLabel("Resources (comma-separated)").fill("configmaps");
  await page.getByText("Include PVC snapshots", { exact: true }).click();
  const snapshotAccepted = page.waitForResponse(
    (response) =>
      response.request().method() === "POST" &&
      response.url().includes(`/api/v1/clusters/${clusterID}/snapshots/`),
  );
  await page.getByRole("button", { name: "Create snapshot" }).click();
  expect((await snapshotAccepted).status()).toBe(202);
  await expect(
    page.getByText("Snapshot queued", { exact: true }),
  ).toBeVisible();

  const snapshotRow = page
    .locator("tbody tr")
    .filter({ hasText: "ad-hoc" })
    .last();
  await expect(snapshotRow).toContainText("Completed", { timeout: 180_000 });
  const snapshotName = (
    await snapshotRow.locator("td").first().innerText()
  ).trim();
  expect(snapshotName).toBeTruthy();

  await page.goto(resourcePath);
  await expect(
    page.getByRole("heading", { name: backupConfigMap, exact: true }),
  ).toBeVisible();
  await page.getByRole("tab", { name: "YAML" }).click();
  await expect(page.locator(".view-lines")).toContainText(backupMessage);
  await page.getByRole("button", { name: "Delete", exact: true }).click();
  await expect(
    page.getByRole("heading", { name: "Delete ConfigMap" }),
  ).toBeVisible();
  const deleteDialog = page.getByLabel("Delete ConfigMap");
  await deleteDialog.getByPlaceholder(backupConfigMap).fill(backupConfigMap);
  const deletion = page.waitForResponse(
    (response) =>
      response.request().method() === "DELETE" &&
      response.url().includes(`/api/v1/clusters/${clusterID}/k8s/`),
  );
  await deleteDialog
    .getByRole("button", { name: "Delete", exact: true })
    .click();
  expect((await deletion).ok()).toBe(true);
  await expect(page).toHaveURL(snapshotsPath);
  const deletedResource = await page.request.get(
    `/api/v1/clusters/${clusterID}/k8s/api/v1/namespaces/${backupNamespace}/configmaps/${backupConfigMap}`,
  );
  expect(deletedResource.status()).toBe(404);

  const completedSnapshotRow = page
    .locator("tbody tr")
    .filter({ hasText: snapshotName });
  await expect(completedSnapshotRow).toContainText("Completed");
  await completedSnapshotRow.getByRole("button", { name: "Restore" }).click();
  await expect(
    page.getByRole("heading", { name: `Restore from ${snapshotName}` }),
  ).toBeVisible();
  const restoreDialog = page.getByLabel(`Restore from ${snapshotName}`);
  await restoreDialog.getByText("Advanced", { exact: true }).click();
  await restoreDialog
    .getByLabel("Included namespaces (comma-separated)")
    .fill(backupNamespace);
  await restoreDialog
    .getByText("Restore PersistentVolumes", { exact: true })
    .click();
  const restoreAccepted = page.waitForResponse(
    (response) =>
      response.request().method() === "POST" &&
      response.url().includes(`/api/v1/clusters/${clusterID}/snapshots/`) &&
      response.url().includes("/restore/"),
  );
  await restoreDialog
    .getByRole("button", { name: "Restore", exact: true })
    .click();
  expect((await restoreAccepted).status()).toBe(202);
  await expect(page.getByText("Restore queued", { exact: true })).toBeVisible();

  await expect
    .poll(
      async () => {
        await page.goto(resourcePath);
        try {
          await page
            .getByRole("button", { name: "Delete", exact: true })
            .waitFor({ timeout: 5_000 });
          return true;
        } catch {
          return false;
        }
      },
      { timeout: 180_000, intervals: [3_000] },
    )
    .toBe(true);
  await page.getByRole("tab", { name: "YAML" }).click();
  await expect(page.locator(".view-lines")).toContainText(backupMessage);
  // The durable restore row is reconciled by the production 30-second worker
  // poll. Keep the member tunnel alive for one full interval before the next
  // journey decommissions it; the runner then asserts the terminal DB phase.
  await page.waitForTimeout(35_000);
});

test("cluster decommission completes through the authenticated agent tunnel", async ({
  page,
}) => {
  await loginViaForm(page);
  await page.goto(`/dashboard/clusters/${clusterID}`);
  await page.getByRole("button", { name: "Open actions menu" }).click();
  await page.getByRole("menuitem", { name: "Delete" }).click();
  await expect(
    page.getByRole("heading", { name: "Delete Cluster" }),
  ).toBeVisible();
  await page
    .getByPlaceholder("live-browser-cluster")
    .fill("live-browser-cluster");
  const accepted = page.waitForResponse(
    (response) =>
      response.request().method() === "DELETE" &&
      response.url().includes(`/api/v1/clusters/${clusterID}`),
  );
  await page.getByRole("button", { name: "Delete", exact: true }).click();
  expect((await accepted).status()).toBe(202);
  await expect(page).toHaveURL(/\/dashboard\/clusters\/?$/);
  await expect(
    page.getByText("Live Browser Cluster", { exact: true }),
  ).toHaveCount(0, { timeout: 60_000 });
});
