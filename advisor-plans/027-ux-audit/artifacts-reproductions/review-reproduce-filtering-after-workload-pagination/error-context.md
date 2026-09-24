# Instructions

- Following Playwright test failed.
- Explain why, be concise, respect Playwright best practices.
- Provide a snippet of code with the fix, if possible.

# Test info

- Name: review.spec.ts >> reproduce filtering after workload pagination
- Location: advisor-plans/027-ux-audit/review.spec.ts:115:1

# Error details

```
Error: expect(locator).toBeVisible() failed

Locator: getByRole('button', { name: 'Namespace scope: team-b', exact: true })
Expected: visible
Timeout: 30000ms
Error: element(s) not found

Call log:
  - Expect "toBeVisible" getByRole('button', { name: 'Namespace scope: team-b', exact: true }) with timeout 30000ms
  - waiting for getByRole('button', { name: 'Namespace scope: team-b', exact: true })

```

```yaml
- link "Skip to main content":
  - /url: "#main"
- complementary:
  - link "Astronomer by AlphaBravo":
    - /url: /dashboard
  - button "Collapse sidebar"
  - link "All Clusters":
    - /url: /dashboard/clusters
  - paragraph: v1.31
  - navigation:
    - button "Cluster"
    - button "Observability"
    - button "Workloads" [expanded]
    - link "Overview":
      - /url: /dashboard/clusters/c-smoke-1/workloads
    - link "Deployments":
      - /url: /dashboard/clusters/c-smoke-1/deployments
    - button "Star Deployments"
    - link "DaemonSets":
      - /url: /dashboard/clusters/c-smoke-1/daemonsets
    - button "Star DaemonSets"
    - link "StatefulSets":
      - /url: /dashboard/clusters/c-smoke-1/statefulsets
    - button "Star StatefulSets"
    - link "Jobs":
      - /url: /dashboard/clusters/c-smoke-1/jobs
    - button "Star Jobs"
    - link "CronJobs":
      - /url: /dashboard/clusters/c-smoke-1/cronjobs
    - button "Star CronJobs"
    - link "Pods":
      - /url: /dashboard/clusters/c-smoke-1/pods
    - button "Star Pods"
    - button "Service Discovery"
    - button "Storage"
    - button "Policy"
    - button "RBAC"
    - button "Cluster Management"
    - button "More Resources"
  - link "Documentation":
    - /url: /astronomer-docs/
  - paragraph: Astronomer 1.2.0
  - paragraph:
    - text: Built by
    - link "AlphaBravo":
      - /url: https://alphabravo.io
- banner:
  - button "Smoke East"
  - 'button "Project scope: All projects" [disabled]': All projects
  - 'button "Namespace scope: Namespaces" [disabled]': Namespaces
  - textbox "Global resource search":
    - /placeholder: Search resources...
  - text: / ⌘K
  - 'button "Open cluster shell for Smoke East (Ctrl+`)"': Shell
  - button "Kubeconfig"
  - button "Import"
  - 'button "Theme: light"'
  - button "Notifications"
  - button "User menu"
- main:
  - heading "Deployments" [level=1]
  - button "Create Deployment"
  - textbox "Search deployments..."
  - checkbox "Group namespaces (this page)"
  - text: Group namespaces (this page)
  - button "Columns"
  - region "Scrollable data table":
    - table:
      - rowgroup:
        - row "Select all rows on this page Sort by Name Sort by Namespace Ready Status Image Sort by Age":
          - columnheader "Select all rows on this page":
            - checkbox "Select all rows on this page"
          - columnheader "Sort by Name":
            - button "Sort by Name": Name
          - columnheader "Sort by Namespace":
            - button "Sort by Namespace": Namespace
          - columnheader "Ready"
          - columnheader "Status"
          - columnheader "Image"
          - columnheader "Sort by Age":
            - button "Sort by Age": Age
          - columnheader
      - rowgroup:
        - row "No deployments found Resources will appear here when they are available in this scope.":
          - cell "No deployments found Resources will appear here when they are available in this scope.":
            - paragraph: No deployments found
            - paragraph: Resources will appear here when they are available in this scope.
- region "Notifications alt+T"
- button "Open Tanstack query devtools":
  - img
```

# Test source

```ts
  32  | });
  33  | 
  34  | test('inspect delivery destination selection and page search', async ({ page }, info) => {
  35  |   await page.setViewportSize({ width: 1280, height: 900 });
  36  |   await page.goto('/dashboard/delivery/sources');
  37  |   await expect(page.getByTestId('app-shell')).toBeVisible();
  38  |   const selected = await page.locator('aside [aria-current="page"]').allTextContents();
  39  |   await page.keyboard.press('Control+k');
  40  |   await expect(page.getByPlaceholder(/Search pages|Search commands|Search anything|Type a command/i)).toBeVisible().catch(() => {});
  41  |   await page.screenshot({ path: info.outputPath('delivery-palette.png') });
  42  |   const text = await page.locator('body').innerText();
  43  |   fs.writeFileSync(info.outputPath('delivery-observation.json'), JSON.stringify({ selected, text }, null, 2));
  44  |   console.log('DELIVERY_SELECTED', JSON.stringify(selected));
  45  | });
  46  | 
  47  | test('capture representative page families from current source', async ({ page }, info) => {
  48  |   test.setTimeout(240000);
  49  |   await page.setViewportSize({ width: 1280, height: 900 });
  50  |   const base = `/dashboard/clusters/${SMOKE_CLUSTER_ID}`;
  51  |   const routes = ['/dashboard', '/dashboard/clusters', '/dashboard/projects', '/dashboard/rbac',
  52  |     '/dashboard/security', '/dashboard/agents', '/dashboard/alerting', '/dashboard/logging',
  53  |     '/dashboard/catalog', '/dashboard/settings', '/dashboard/settings/backup',
  54  |     '/dashboard/settings/operations', '/dashboard/cluster-templates',
  55  |     `${base}/apps`, `${base}/tools`, `${base}/workloads`, `${base}/custom-resources`,
  56  |     `${base}/metrics`, `${base}/logging`, `${base}/image-scans`, `${base}/snapshots`, `${base}/template`];
  57  |   const rows: unknown[] = [];
  58  |   for (const route of routes) {
  59  |     const errors: string[] = [];
  60  |     const onError = (error: Error) => errors.push(error.message);
  61  |     page.on('pageerror', onError);
  62  |     await page.goto(route);
  63  |     await expect(page.getByTestId('app-shell')).toBeVisible();
  64  |     await page.locator('main').waitFor();
  65  |     await page.waitForTimeout(300);
  66  |     const main = page.locator('main');
  67  |     rows.push({ route, headings: await main.locator('h1,h2').allTextContents(),
  68  |       alerts: await main.locator('[role="alert"]').allTextContents(),
  69  |       text: (await main.innerText()).slice(0, 7000), errors });
  70  |     page.off('pageerror', onError);
  71  |     await page.screenshot({ path: info.outputPath(`${route.replaceAll('/', '_')}.png`) });
  72  |   }
  73  |   fs.writeFileSync(info.outputPath('page-observations.json'), JSON.stringify(rows, null, 2));
  74  |   console.log('PAGE_FAMILIES', JSON.stringify(rows.map(({ route, headings, alerts, errors }: any) => ({ route, headings, alerts, errors }))));
  75  | });
  76  | 
  77  | test('reproduce custom-resource back destination', async ({ page }, info) => {
  78  |   const base = `/dashboard/clusters/${SMOKE_CLUSTER_ID}`;
  79  |   await page.route('**/k8s/apis/cert-manager.io/v1/namespaces/default/certificates/review-cert**', route => route.fulfill({ json: {
  80  |     apiVersion: 'cert-manager.io/v1', kind: 'Certificate',
  81  |     metadata: { name: 'review-cert', namespace: 'default', uid: 'review-cert-uid' },
  82  |     spec: { secretName: 'review-cert-tls' }, status: { conditions: [] },
  83  |   }}));
  84  |   await page.goto(`${base}/custom-resources/cert-manager.io/v1/certificates/default/review-cert`);
  85  |   await expect(page.getByRole('heading', { name: 'review-cert', exact: true })).toBeVisible();
  86  |   const back = page.getByRole('link', { name: 'Back', exact: true });
  87  |   const href = await back.getAttribute('href');
  88  |   await back.click();
  89  |   await expect(page.getByText(/Unknown resource type/i)).toBeVisible();
  90  |   fs.writeFileSync(info.outputPath('custom-back.json'), JSON.stringify({ href, resultingUrl: page.url() }, null, 2));
  91  |   await page.screenshot({ path: info.outputPath('custom-back.png') });
  92  |   console.log('CUSTOM_BACK', href, page.url());
  93  | });
  94  | 
  95  | test('reproduce namespace disagreement on custom resources', async ({ page }, info) => {
  96  |   await page.route('**/api/v1/clusters/*/namespaces/**', route => route.fulfill({ json: {
  97  |     data: ['team-a', 'team-b'].map(name => ({ name, cluster_id: SMOKE_CLUSTER_ID, status: 'Active', created_at: '2026-01-01T00:00:00Z' })),
  98  |     pagination: { limit: 200, offset: 0, total: 2, has_more: false, next_offset: null },
  99  |   }}));
  100 |   const requests: string[] = [];
  101 |   await page.route('**/k8s/apis/cert-manager.io/v1/certificates**', route => {
  102 |     requests.push(route.request().url());
  103 |     return route.fulfill({ json: { apiVersion: 'cert-manager.io/v1', kind: 'CertificateList', metadata: {},
  104 |       items: ['team-a', 'team-b'].map(namespace => ({ metadata: { name: `cert-${namespace}`, namespace } })),
  105 |     }});
  106 |   });
  107 |   await page.goto(`/dashboard/clusters/${SMOKE_CLUSTER_ID}/custom-resources/cert-manager.io/v1/certificates?namespaces=team-a`);
  108 |   await expect(page.getByRole('button', { name: 'Namespace scope: team-a', exact: true })).toBeVisible();
  109 |   await expect(page.getByRole('link', { name: 'cert-team-b', exact: true })).toBeVisible();
  110 |   fs.writeFileSync(info.outputPath('custom-scope.json'), JSON.stringify({ requests, url: page.url() }, null, 2));
  111 |   await page.screenshot({ path: info.outputPath('custom-scope.png') });
  112 |   console.log('CUSTOM_SCOPE_MISMATCH', JSON.stringify(requests));
  113 | });
  114 | 
  115 | test('reproduce filtering after workload pagination', async ({ page }, info) => {
  116 |   await page.route('**/api/v1/clusters/*/namespaces/**', route => route.fulfill({ json: {
  117 |     data: ['team-a', 'team-b'].map(name => ({ name, cluster_id: SMOKE_CLUSTER_ID, status: 'Active', created_at: '2026-01-01T00:00:00Z' })),
  118 |     pagination: { limit: 200, offset: 0, total: 2, has_more: false, next_offset: null },
  119 |   }}));
  120 |   const requests: string[] = [];
  121 |   await page.route(/\/api\/v1\/clusters\/[^/]+\/workloads\/?(?:\?.*)?$/, route => {
  122 |     requests.push(route.request().url());
  123 |     const u = new URL(route.request().url());
  124 |     const scoped = u.searchParams.get('namespace') === 'team-b';
  125 |     return route.fulfill({ json: { data: [{ id: 'workload-review', name: scoped ? 'wanted-app' : 'unrelated-app',
  126 |       namespace: scoped ? 'team-b' : 'team-a', clusterId: SMOKE_CLUSTER_ID, kind: 'Deployment',
  127 |       status: 'Running', replicas: 1, desiredReplicas: 1, createdAt: '2026-01-01T00:00:00Z' }],
  128 |       pagination: { limit: 20, offset: 0, total: scoped ? 1 : 21, has_more: !scoped, next_offset: scoped ? null : 20 },
  129 |     }});
  130 |   });
  131 |   await page.goto(`/dashboard/clusters/${SMOKE_CLUSTER_ID}/deployments?namespaces=team-b`);
> 132 |   await expect(page.getByRole('button', { name: 'Namespace scope: team-b', exact: true })).toBeVisible();
      |                                                                                            ^ Error: expect(locator).toBeVisible() failed
  133 |   await expect(page.getByText('No deployments found', { exact: true })).toBeVisible();
  134 |   expect(requests.length).toBeGreaterThan(0);
  135 |   expect(requests.every(url => !new URL(url).searchParams.has('namespace'))).toBeTruthy();
  136 |   fs.writeFileSync(info.outputPath('workload-scope.json'), JSON.stringify({ requests, text: await page.locator('main').innerText() }, null, 2));
  137 |   await page.screenshot({ path: info.outputPath('workload-scope.png') });
  138 |   console.log('WORKLOAD_SCOPE_MISMATCH', JSON.stringify(requests));
  139 | });
  140 | 
  141 | test('reproduce shared stacks double-active navigation', async ({ page }, info) => {
  142 |   await page.goto('/dashboard/monitoring/stacks');
  143 |   await expect(page.getByTestId('app-shell')).toBeVisible();
  144 |   const sidebar = page.locator('aside');
  145 |   await expect(sidebar.getByRole('link', { name: 'Shared stacks', exact: true })).toBeVisible();
  146 |   const links = await sidebar.locator('a[aria-current="page"]').evaluateAll(items => items.map(el => ({ text: el.textContent?.trim(), href: el.getAttribute('href'), class: el.className })));
  147 |   expect(links.some(link => link.text === 'Metrics')).toBeTruthy();
  148 |   expect(links.some(link => link.text === 'Shared stacks')).toBeTruthy();
  149 |   fs.writeFileSync(info.outputPath('active-links.json'), JSON.stringify(links, null, 2));
  150 |   console.log('DOUBLE_ACTIVE', JSON.stringify(links));
  151 | });
  152 | 
  153 | test('reproduce forbidden Velero status as not installed', async ({ page }, info) => {
  154 |   let statusRequests = 0;
  155 |   await page.route('**/api/v1/clusters/*/velero-status**', route => {
  156 |     statusRequests++;
  157 |     return route.fulfill({ status: 403, json: { error: { code: 'FORBIDDEN', message: 'Review fixture: access denied' } } });
  158 |   });
  159 |   await page.goto(`/dashboard/clusters/${SMOKE_CLUSTER_ID}/snapshots`);
  160 |   await expect(page.getByText('Velero is not installed', { exact: true })).toBeVisible();
  161 |   expect(statusRequests).toBeGreaterThan(0);
  162 |   await page.screenshot({ path: info.outputPath('velero-failed-read.png') });
  163 |   fs.writeFileSync(info.outputPath('velero-failed-read.json'), JSON.stringify({ text: await page.locator('main').innerText() }, null, 2));
  164 | });
  165 | 
```