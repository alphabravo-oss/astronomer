# Instructions

- Following Playwright test failed.
- Explain why, be concise, respect Playwright best practices.
- Provide a snippet of code with the fix, if possible.

# Test info

- Name: modal.spec.ts >> closing a pending dialog preserves the durable save request
- Location: tests/browser-components/modal.spec.ts:40:1

# Error details

```
Test timeout of 30000ms exceeded.
```

```
Error: locator.click: Test timeout of 30000ms exceeded.
Call log:
  - waiting for getByRole('button', { name: 'Cancel', exact: true })
    - locator resolved to <button type="button" data-state="closed" class="inline-flex shrink-0 items-center justify-center gap-2 whitespace-nowrap rounded-md font-medium transition-colors disabled:cursor-not-allowed disabled:opacity-50 border border-border bg-background text-foreground hover:bg-accent h-(--control-h) px-4 text-body">Cancel</button>
  - attempting click action
    - waiting for element to be visible, enabled and stable
    - element is not stable
  - retrying click action
    - waiting for element to be visible, enabled and stable
  - element was detached from the DOM, retrying

```

# Page snapshot

```yaml
- paragraph [ref=e3]: Saved and closed
```

# Test source

```ts
  1  | import { expect, test } from "@playwright/test";
  2  | 
  3  | test("Enter submits once; server error persists, focuses, and clears after a successful retry", async ({
  4  |   page,
  5  | }) => {
  6  |   let submissions = 0;
  7  |   await page.route("**/siem-forwarders/**", async (route) => {
  8  |     if (route.request().method() !== "POST")
  9  |       return route.fulfill({ json: { data: [] } });
  10 |     submissions++;
  11 |     // Make the pending window observable and exercise repeat Enter.
  12 |     await new Promise((resolve) => setTimeout(resolve, 200));
  13 |     await route.fulfill(
  14 |       submissions === 1
  15 |         ? {
  16 |             status: 400,
  17 |             json: { error: { message: "Endpoint refused connection" } },
  18 |           }
  19 |         : { json: { data: { id: "forwarder-1" } } },
  20 |     );
  21 |   });
  22 |   await page.goto("/?modal");
  23 |   await page.getByLabel("Name", { exact: true }).fill("corp");
  24 |   const endpoint = page.getByLabel("Endpoint", { exact: true });
  25 |   await endpoint.fill("siem.example:6514");
  26 |   await endpoint.press("Enter");
  27 |   await endpoint.press("Enter");
  28 |   const summary = page.getByRole("alert");
  29 |   await expect(summary).toContainText("Endpoint refused connection");
  30 |   await expect(summary).toBeFocused();
  31 |   expect(submissions).toBe(1);
  32 |   await page.getByLabel("Name", { exact: true }).fill("corrected");
  33 |   await expect(summary).toBeVisible();
  34 |   await endpoint.press("Enter");
  35 |   await expect(page.getByText("Saved and closed")).toBeVisible();
  36 |   expect(submissions).toBe(2);
  37 |   expect(await page.pageErrors()).toEqual([]);
  38 | });
  39 | 
  40 | test("closing a pending dialog preserves the durable save request", async ({
  41 |   page,
  42 | }) => {
  43 |   let submissions = 0;
  44 |   await page.route("**/siem-forwarders/**", async (route) => {
  45 |     if (route.request().method() !== "POST")
  46 |       return route.fulfill({ json: { data: [] } });
  47 |     submissions++;
  48 |     await new Promise((resolve) => setTimeout(resolve, 250));
  49 |     await route.fulfill({ json: { data: { id: "forwarder-1" } } });
  50 |   });
  51 |   await page.goto("/?modal");
  52 |   await page.getByLabel("Name", { exact: true }).fill("corp");
  53 |   await page.getByLabel("Endpoint", { exact: true }).fill("siem.example:6514");
  54 |   const committed = page.waitForResponse(
  55 |     (response) =>
  56 |       response.url().includes("siem-forwarders") &&
  57 |       response.request().method() === "POST",
  58 |   );
  59 |   await page.getByLabel("Endpoint", { exact: true }).press("Enter");
  60 |   await expect(
  61 |     page.getByRole("button", { name: "Create Forwarder", exact: true }),
  62 |   ).toBeDisabled();
> 63 |   await page.getByRole("button", { name: "Cancel", exact: true }).click();
     |                                                                   ^ Error: locator.click: Test timeout of 30000ms exceeded.
  64 |   await expect(page.getByText("Saved and closed")).toBeVisible();
  65 |   expect((await committed).ok()).toBe(true);
  66 |   expect(submissions).toBe(1);
  67 |   expect(await page.pageErrors()).toEqual([]);
  68 | });
  69 | 
```