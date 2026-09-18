import { act, renderHook } from "@testing-library/react";
import { createBrowserState } from "./browser-state";

interface CounterState extends Record<string, unknown> {
  count: number;
  label: string;
}

describe("createBrowserState", () => {
  beforeEach(() => window.localStorage.clear());

  it("publishes synchronous updates to React subscribers", () => {
    const useCounter = createBrowserState<CounterState>({
      count: 0,
      label: "initial",
    });
    const { result } = renderHook(() => useCounter((state) => state.count));

    act(() => useCounter.setState((state) => ({ count: state.count + 1 })));

    expect(result.current).toBe(1);
    expect(useCounter.getState().label).toBe("initial");
  });

  it("hydrates and persists only the declared browser-state fields", () => {
    window.localStorage.setItem(
      "counter",
      JSON.stringify({ state: { count: 4 }, version: 1 }),
    );
    const useCounter = createBrowserState<CounterState>(
      { count: 0, label: "initial" },
      {
        storageKey: "counter",
        version: 1,
        persist: (state) => ({ count: state.count }),
      },
    );

    expect(useCounter.getState()).toEqual({ count: 4, label: "initial" });
    useCounter.setState({ count: 5, label: "changed" });

    expect(JSON.parse(window.localStorage.getItem("counter")!)).toEqual({
      state: { count: 5 },
      version: 1,
    });
  });

  it("discards a version mismatch when no migration is supplied", () => {
    window.localStorage.setItem(
      "counter",
      JSON.stringify({ state: { count: 99 }, version: 0 }),
    );

    const useCounter = createBrowserState<CounterState>(
      { count: 1, label: "initial" },
      { storageKey: "counter", version: 1 },
    );

    expect(useCounter.getState()).toEqual({ count: 1, label: "initial" });
  });
});
