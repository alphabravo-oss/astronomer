import { expect, test } from "@playwright/test";
import { load } from "js-yaml";

test("widgets recover from an empty scope and share one refresh", async ({
  page,
}) => {
  await page.clock.install();
  await page.goto("/?reliability");
  await expect(page.getByLabel("Requests", { exact: true })).toHaveText("1");
  await page.getByRole("button", { name: "Scope A" }).click();
  await expect(page.getByText("Scope a widget 1")).toBeVisible();
  await expect(page.getByLabel("Requests", { exact: true })).toHaveText("2");
  await page.clock.fastForward(5001);
  await expect(page.getByLabel("Requests", { exact: true })).toHaveText("3");
  await page.getByRole("button", { name: "Scope B" }).click();
  await expect(page.getByText("Scope b widget 1")).toBeVisible();
  await expect(page.getByText("Scope a widget 1")).toHaveCount(0);
  await expect(page.getByLabel("Requests", { exact: true })).toHaveText("4");
});

test("literal annotation survives Form to YAML to Form and submission", async ({
  page,
}) => {
  await page.goto("/?reliability");
  await page.getByText("Chart configuration — 1").click();
  await page
    .getByRole("textbox", { name: "Literal annotation", exact: true })
    .fill("edited");
  await page.getByRole("button", { name: "Edit YAML" }).click();
  const yaml = page.getByRole("textbox", { name: "Values YAML" });
  const value = await yaml.inputValue();
  expect(load(value)).toEqual({
    annotations: {
      "example.com/key": "edited",
      example: { "com/key": "nested" },
    },
    custom: { untouched: true },
    hosts: ["one", "two"],
  });
  await yaml.fill(value.replace("edited", "yaml-edit"));
  await page.getByRole("button", { name: "Use form" }).click();
  await page.getByText("Chart configuration — 1").click();
  await expect(
    page.getByRole("textbox", { name: "Literal annotation", exact: true }),
  ).toHaveValue("yaml-edit");
  await expect(
    page.getByRole("textbox", { name: "Nested annotation", exact: true }),
  ).toHaveValue("nested");
  await page.getByRole("button", { name: "Submit values" }).click();
  expect(
    load((await page.getByLabel("Submitted values").textContent()) ?? ""),
  ).toEqual({
    annotations: {
      "example.com/key": "yaml-edit",
      example: { "com/key": "nested" },
    },
    custom: { untouched: true },
    hosts: ["one", "two"],
  });
});
