import api from "./transport";
it("serializes OpenAPI exploded arrays as repeated keys rather than bracketed names", () => {
  const uri = api.getUri({
    url: "/clusters/c/workloads/",
    params: { namespaces: ["team-a", "team-b"], namespace: undefined },
  });
  const query = new URL(uri, "https://example.test").searchParams;
  expect(query.getAll("namespaces")).toEqual(["team-a", "team-b"]);
  expect(query.has("namespaces[]")).toBe(false);
  expect(query.has("namespace")).toBe(false);
});
