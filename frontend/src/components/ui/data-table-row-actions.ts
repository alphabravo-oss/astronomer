import type React from "react";

const rowActionSelector = [
  "a[href]",
  "button",
  "input",
  "select",
  "textarea",
  '[role="button"]',
  '[role="menuitem"]',
  "[data-row-click-ignore]",
].join(",");

export function eventStartedInRowAction(
  event: React.MouseEvent<HTMLElement>,
): boolean {
  return (
    event.target instanceof Element &&
    event.target.closest(rowActionSelector) !== null
  );
}
