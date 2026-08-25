import { afterEach, describe, expect, it, vi } from "vitest";
import * as generated from "@/lib/api/generated/client";
import { createHelmRepository } from "@/lib/api/catalog";

vi.mock("@/lib/api/generated/client", async (importOriginal) => {
  const actual = await importOriginal<
    typeof import("@/lib/api/generated/client")
  >();
  return { ...actual, postCatalogRepositories: vi.fn() };
});

const repositoryWire = {
  id: "1fa85f64-5717-4562-b3fc-2c963f66afa6",
  name: "private",
  url: "https://charts.example.com",
  repo_type: "helm",
  description: "",
  is_default: false,
  auth_type: "basic",
  auth_config: {},
  enabled: true,
  last_synced_at: null,
  last_sync_attempted_at: null,
  last_sync_error: "",
  created_by_id: null,
  created_at: "2026-08-23T00:00:00Z",
  updated_at: "2026-08-23T00:00:00Z",
  owner_project_id: null,
  chart_count: 0,
};

describe("createHelmRepository generated request contract", () => {
  afterEach(() => vi.clearAllMocks());

  function mockCreate() {
    vi.mocked(generated.postCatalogRepositories).mockResolvedValueOnce({
      data: repositoryWire,
    });
  }

  const bodyOf = () =>
    vi.mocked(generated.postCatalogRepositories).mock.calls[0][0].body;

  it("nests credentials in auth_config and declares basic auth", async () => {
    mockCreate();
    await createHelmRepository({
      name: "private",
      url: "https://charts.example.com",
      repoType: "helm",
      username: "deploy",
      password: "s3cret",
    });

    expect(bodyOf()).toEqual(
      expect.objectContaining({
        auth_type: "basic",
        auth_config: { username: "deploy", password: "s3cret" },
      }),
    );
    expect(bodyOf()).not.toHaveProperty("username");
    expect(bodyOf()).not.toHaveProperty("password");
  });

  it("serializes repository type and enabled state in wire casing", async () => {
    mockCreate();
    await createHelmRepository({
      name: "oci-repo",
      url: "oci://registry.example.com",
      repoType: "oci",
    });

    expect(bodyOf()).toEqual(
      expect.objectContaining({ repo_type: "oci", enabled: true }),
    );
    expect(bodyOf()).not.toHaveProperty("repoType");
  });

  it("makes anonymous authentication explicit", async () => {
    mockCreate();
    await createHelmRepository({
      name: "public",
      url: "https://charts.example.com",
      repoType: "helm",
    });

    expect(bodyOf()).toEqual(
      expect.objectContaining({ auth_type: "none", auth_config: {} }),
    );
  });
});
