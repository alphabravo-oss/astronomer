import { beforeEach, describe, expect, it, vi } from "vitest";

const { request } = vi.hoisted(() => ({ request: vi.fn() }));
vi.mock("@/lib/api/transport", () => ({ default: { request } }));

import {
  deletePlatformSetting,
  getPlatformSetting,
  listPlatformSettings,
  putPlatformSetting,
  savePlatformSettingsBatch,
} from "./platform-settings";

const SETTING = {
  key: "token.default_ttl_min",
  value: 60,
  description: "Default token lifetime",
  type: "int",
  default: 60,
  updated_by: "10000000-0000-0000-0000-000000000001",
  updated_at: "2026-08-23T00:00:00Z",
  is_default: false,
} as const;

function lastRequest() {
  return request.mock.calls.at(-1)?.[0];
}

beforeEach(() => {
  request.mockReset();
  request.mockImplementation(async (config) => {
    if (config.url === "/api/v1/admin/settings" && config.method === "GET") {
      return { data: { data: [SETTING] } };
    }
    if (config.url === "/api/v1/admin/settings" && config.method === "PUT") {
      return { data: { data: [SETTING] } };
    }
    return { data: { data: SETTING } };
  });
});

describe("generated platform settings boundary", () => {
  it("maps exact registry metadata and fails closed on malformed lists", async () => {
    await expect(listPlatformSettings()).resolves.toEqual([
      {
        key: SETTING.key,
        value: 60,
        description: SETTING.description,
        type: "int",
        defaultValue: 60,
        updatedBy: SETTING.updated_by,
        updatedAt: SETTING.updated_at,
        isDefault: false,
      },
    ]);
    request.mockResolvedValueOnce({ data: {} });
    await expect(listPlatformSettings()).rejects.toThrow(
      "listPlatformSettings returned no data payload",
    );
  });

  it("uses one atomic request for a sparse multi-setting form save", async () => {
    const updates = {
      "banner.global_color": "critical",
      "token.default_ttl_min": 120,
    };
    await savePlatformSettingsBatch(updates);
    expect(lastRequest()).toEqual(
      expect.objectContaining({
        method: "PUT",
        url: "/api/v1/admin/settings",
        data: { updates },
      }),
    );
  });

  it("preserves exact dotted keys for get, put, and reset", async () => {
    await getPlatformSetting(SETTING.key);
    expect(lastRequest()?.url).toBe(
      "/api/v1/admin/settings/token.default_ttl_min",
    );
    await putPlatformSetting(SETTING.key, 120);
    expect(lastRequest()?.data).toEqual({ value: 120 });
    await deletePlatformSetting(SETTING.key);
    expect(lastRequest()?.method).toBe("DELETE");
  });
});
