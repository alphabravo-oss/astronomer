import { renderHook, act } from "@testing-library/react";
import { useOperationIntent } from "./use-operation-intent";
it("reuses uncertain request identity, but changed payload or accepted intent gets a new key", () => {
  const { result, rerender } = renderHook(useOperationIntent);
  const first = result.current.keyFor({ id: "release", version: "1" });
  rerender();
  expect(result.current.keyFor({ id: "release", version: "1" })).toBe(first);
  const changed = result.current.keyFor({ id: "release", version: "2" });
  expect(changed).not.toBe(first);
  act(() => result.current.complete());
  expect(result.current.keyFor({ id: "release", version: "2" })).not.toBe(
    changed,
  );
});
