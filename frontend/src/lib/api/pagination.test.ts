import { pageCountLabel, pageTableCount } from "./pagination";

const pagination = { limit: 25, offset: 0, has_more: true, next_offset: 25 };
it("separates an unknown total from the navigation lower bound", () => {
  const page = { data: Array.from({ length: 25 }, (_, i) => i), pagination };
  expect(pageTableCount(page)).toEqual({
    rowCount: 26,
    rowCountIsLowerBound: true,
  });
  expect(pageCountLabel(page)).toBe("At least 26");
});
it("uses an explicit total without a lower-bound label", () => {
  const page = { data: [1], pagination: { ...pagination, total: 200 } };
  expect(pageTableCount(page)).toEqual({
    rowCount: 200,
    rowCountIsLowerBound: false,
  });
  expect(pageCountLabel(page)).toBe("200");
});
it("does not infer a total from an empty page beyond the current end", () => {
  const page = {
    data: [],
    pagination: {
      ...pagination,
      offset: 225,
      has_more: false,
      next_offset: null,
    },
  };
  expect(pageTableCount(page).rowCountIsUnknown).toBe(true);
  expect(pageCountLabel(page)).toBe("Unknown");
});
it("infers the terminal count and leaves unavailable metrics unknown", () => {
  const page = {
    data: [1],
    pagination: {
      ...pagination,
      offset: 225,
      has_more: false,
      next_offset: null,
    },
  };
  expect(pageCountLabel(page)).toBe("226");
  expect(pageCountLabel(undefined)).toBe("—");
  expect(pageTableCount(undefined)).toEqual({
    rowCount: 0,
    rowCountIsLowerBound: false,
  });
});
