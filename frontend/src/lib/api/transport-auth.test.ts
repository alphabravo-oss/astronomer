import axios, {
  AxiosError,
  AxiosHeaders,
  CanceledError,
  type AxiosAdapter,
} from "axios";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import api from "./transport";
import { logoutCurrentSession } from "./account-security";
import { refreshSession, waitForRefresh } from "./authentication-refresh";

const unauthorized: AxiosAdapter = async (config) => {
  throw new AxiosError("unauthorized", "ERR_BAD_REQUEST", config, undefined, {
    data: {},
    status: 401,
    statusText: "Unauthorized",
    headers: {},
    config,
  });
};
const success: AxiosAdapter = async (config) => ({
  data: "ok",
  status: 200,
  statusText: "OK",
  headers: {},
  config,
});
const flush = async () => {
  for (let i = 0; i < 20; i++) await Promise.resolve();
};

describe("shared authentication refresh lifetime", () => {
  beforeEach(() => {
    vi.useFakeTimers();
    window.history.replaceState(null, "", "/auth/login");
  });
  afterEach(() => {
    vi.restoreAllMocks();
    vi.useRealTimers();
  });

  it("releases every caller at the deadline even if refresh never settles, then allows recovery", async () => {
    const refresh = vi
      .spyOn(axios, "post")
      .mockImplementationOnce(() => new Promise(() => {}));
    const settled: unknown[] = [];
    const first = api
      .get("/first", { adapter: unauthorized })
      .catch((e) => settled.push(e));
    const second = api
      .get("/second", { adapter: unauthorized })
      .catch((e) => settled.push(e));
    await flush();
    expect(refresh).toHaveBeenCalledTimes(1);
    await vi.advanceTimersByTimeAsync(30_001);
    expect(settled).toHaveLength(2);
    expect(settled).toEqual([
      expect.objectContaining({ code: "ETIMEDOUT" }),
      expect.objectContaining({ code: "ETIMEDOUT" }),
    ]);
    await Promise.all([first, second]);
    refresh.mockResolvedValueOnce({ data: {} });
    const adapter: AxiosAdapter = async (config) =>
      config.headers.has("retried")
        ? success(config)
        : unauthorized({
            ...config,
            headers: new AxiosHeaders({ retried: "yes" }),
          });
    await expect(api.get("/recovery", { adapter })).resolves.toMatchObject({
      data: "ok",
    });
    expect(refresh).toHaveBeenCalledTimes(2);
  });

  it("cancels either waiter promptly without aborting the refresh required by another caller", async () => {
    let complete!: () => void;
    const refresh = vi.spyOn(axios, "post").mockImplementationOnce(
      () =>
        new Promise((resolve) => {
          complete = () => resolve({ data: {} });
        }),
    );
    const owner = new AbortController();
    const follower = new AbortController();
    const errors: unknown[] = [];
    const first = api
      .get("/first", { adapter: unauthorized, signal: owner.signal })
      .catch((e) => errors.push(e));
    const second = api
      .get("/second", { adapter: unauthorized, signal: follower.signal })
      .catch((e) => errors.push(e));
    const survivor = api
      .get("/survivor", { adapter: unauthorized })
      .catch((e) => e);
    await flush();
    owner.abort();
    follower.abort();
    await flush();
    expect(errors).toHaveLength(2);
    expect(errors.every(axios.isCancel)).toBe(true);
    expect(refresh.mock.calls[0][2]?.signal?.aborted).toBe(false);
    complete();
    await Promise.all([first, second, survivor]);
    expect(refresh).toHaveBeenCalledTimes(1);
  });

  it("does not refresh a retry 401 and can refresh again after a failed attempt", async () => {
    const refresh = vi
      .spyOn(axios, "post")
      .mockRejectedValueOnce(new Error("revoked"))
      .mockResolvedValue({ data: {} });
    await expect(api.get("/first", { adapter: unauthorized })).rejects.toThrow(
      "revoked",
    );
    await expect(
      api.get("/second", { adapter: unauthorized }),
    ).rejects.toMatchObject({ status: 401 });
    expect(refresh).toHaveBeenCalledTimes(2);
  });

  it("preserves cancellation identity instead of enriching it as offline", async () => {
    const cancel = new CanceledError("intentional abort");
    await expect(
      api.get("/read", {
        adapter: async () => {
          throw cancel;
        },
      }),
    ).rejects.toBe(cancel);
  });
});

it("logout settles old refresh waiters and late completion cannot clear a new refresh owner", async () => {
  let completeOld!: () => void;
  let completeNew!: () => void;
  const refresh = vi
    .spyOn(axios, "post")
    .mockImplementationOnce(
      () =>
        new Promise((resolve) => {
          completeOld = () => resolve({ data: {} });
        }),
    )
    .mockImplementationOnce(
      () =>
        new Promise((resolve) => {
          completeNew = () => resolve({ data: {} });
        }),
    );
  const old = api
    .get("/old", { adapter: unauthorized })
    .catch((error: unknown) => error);
  await flush();
  const previousAdapter = api.defaults.adapter;
  api.defaults.adapter = async (config) => ({
    data: { revoked: true },
    status: 200,
    statusText: "OK",
    headers: {},
    config,
  });
  try {
    await logoutCurrentSession();
  } finally {
    api.defaults.adapter = previousAdapter;
  }
  expect(axios.isCancel(await old)).toBe(true);
  const recovered = api
    .get("/new", { adapter: unauthorized })
    .catch((error: unknown) => error);
  await flush();
  completeOld();
  await flush();
  const follower = api
    .get("/follower", { adapter: unauthorized })
    .catch((error: unknown) => error);
  await flush();
  expect(refresh).toHaveBeenCalledTimes(2);
  completeNew();
  await Promise.all([recovered, follower]);
  refresh.mockRestore();
});

it("retains credential cookies and CSRF headers for refresh", async () => {
  document.cookie = "astronomer_csrf=fixture-csrf; path=/";
  const refresh = vi.spyOn(axios, "post").mockResolvedValue({ data: {} });
  await expect(
    api.get("/csrf", { adapter: unauthorized }),
  ).rejects.toMatchObject({ status: 401 });
  expect(refresh).toHaveBeenCalledWith(
    expect.stringContaining("/auth/refresh/"),
    {},
    expect.objectContaining({
      withCredentials: true,
      timeout: 30000,
      headers: expect.objectContaining({ "X-CSRF-Token": "fixture-csrf" }),
    }),
  );
  document.cookie = "astronomer_csrf=; max-age=0; path=/";
  refresh.mockRestore();
});

it("observes shared failure when its only waiter is already aborted", async () => {
  vi.useFakeTimers();
  const refresh = vi
    .spyOn(axios, "post")
    .mockImplementationOnce(() => new Promise(() => {}));
  const controller = new AbortController();
  controller.abort();
  const owned = refreshSession("/auth/refresh/", {}, vi.fn());
  await expect(waitForRefresh(owned, controller.signal)).rejects.toSatisfy(
    axios.isCancel,
  );
  // No live waiter remains to attach a rejection handler; the owner must still observe failure.
  await vi.advanceTimersByTimeAsync(30_001);
  refresh.mockResolvedValueOnce({ data: {} });
  await expect(
    refreshSession("/auth/refresh/", {}, vi.fn()),
  ).resolves.toBeUndefined();
  expect(refresh).toHaveBeenCalledTimes(2);
  refresh.mockRestore();
  vi.useRealTimers();
});
it("keeps a failing shared refresh observed after every waiter aborts", async () => {
  vi.useFakeTimers();
  const refresh = vi
    .spyOn(axios, "post")
    .mockImplementationOnce(() => new Promise(() => {}));
  const controller = new AbortController();
  const result = api
    .get("/all-aborted", { adapter: unauthorized, signal: controller.signal })
    .catch((error: unknown) => error);
  await flush();
  controller.abort();
  expect(axios.isCancel(await result)).toBe(true);
  await vi.advanceTimersByTimeAsync(30_001);
  expect(refresh).toHaveBeenCalledTimes(1);
  refresh.mockRestore();
  vi.useRealTimers();
});
