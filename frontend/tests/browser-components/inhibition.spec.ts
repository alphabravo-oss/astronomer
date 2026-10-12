import { expect, test } from "@playwright/test";

const inhibition = {
  id: "11111111-1111-4111-8111-111111111111",
  name: "Suppress node alerts",
  source_matchers: [
    { label: "alertname", value: "ClusterDown", is_regex: false },
  ],
  target_matchers: [{ label: "severity", value: "warn.*", is_regex: true }],
  equal_labels: ["cluster"],
  enabled: true,
  created_at: "2026-07-01T00:00:00Z",
  updated_at: "2026-07-01T00:00:00Z",
};

test("inhibition validation fields remain safe and focusable across edits and Enter retries", async ({
  page,
}) => {
  let submissions = 0;
  await page.route("**/admin/alerting/inhibitions/**", async (route) => {
    if (route.request().method() === "GET")
      return route.fulfill({ json: { data: [inhibition] } });
    submissions++;
    await new Promise((resolve) => setTimeout(resolve, 200));
    return route.fulfill(
      submissions === 1
        ? {
            status: 422,
            json: {
              error: {
                code: "validation_error",
                message: "private-fixture",
                fields: [
                  {
                    field: "source_matchers",
                    rule: "invalid_regex",
                    message: "private-fixture",
                  },
                  { field: "password", message: "private-fixture" },
                ],
              },
            },
          }
        : { json: { data: inhibition } },
    );
  });
  await page.goto("/?inhibition");
  await page
    .getByRole("button", { name: "Edit inhibition", exact: true })
    .click();
  const name = page.getByLabel("Name", { exact: true });
  await name.press("Enter");
  await name.press("Enter");
  const summary = page.getByRole("alert");
  await expect(summary).toContainText(
    "Source matchers contain an invalid regular expression.",
  );
  await expect(summary).toBeFocused();
  await expect(summary).not.toContainText("private-fixture");
  expect(submissions).toBe(1);
  await summary
    .getByRole("button", {
      name: "Source matchers contain an invalid regular expression.",
    })
    .click();
  await expect(
    page.getByRole("textbox", { name: "Source matchers label" }),
  ).toBeFocused();
  await name.fill("Corrected inhibition");
  await expect(summary).toBeVisible();
  await name.press("Enter");
  await expect(page.getByRole("dialog")).not.toBeVisible();
  expect(submissions).toBe(2);
  expect(await page.pageErrors()).toEqual([]);
});
