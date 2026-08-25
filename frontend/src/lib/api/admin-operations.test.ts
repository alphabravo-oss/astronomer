import type { MockedFunction } from "vitest";
import {
  getAdminQueues,
  getAdminQueuesByQueueDlq,
} from "@/lib/api/generated/client";
import { listDLQ, listQueues } from "./admin-operations";

vi.mock("@/lib/api/generated/client", () => ({
  getAdminQueues: vi.fn(),
  getAdminQueuesByQueueDlq: vi.fn(),
}));

const mockedQueues = getAdminQueues as MockedFunction<typeof getAdminQueues>;
const mockedDLQ = getAdminQueuesByQueueDlq as MockedFunction<
  typeof getAdminQueuesByQueueDlq
>;

describe("admin operations API client", () => {
  beforeEach(() => {
    vi.clearAllMocks();
  });

  it("maps the generated queue operation response", async () => {
    mockedQueues.mockResolvedValueOnce({
      data: [
        {
          name: "default",
          size: 1,
          active: 0,
          pending: 1,
          scheduled: 0,
          retry: 0,
          archived: 0,
          completed: 0,
          paused: false,
          as_of: "2026-06-15T00:00:00Z",
        },
      ],
    } as never);

    await expect(listQueues()).resolves.toEqual([
      expect.objectContaining({ name: "default", pending: 1 }),
    ]);
  });

  it("forwards query cancellation to the generated operation", async () => {
    mockedQueues.mockResolvedValueOnce({
      data: [
        {
          name: "tunnel",
          size: 0,
          active: 0,
          pending: 0,
          scheduled: 0,
          retry: 0,
          archived: 0,
          completed: 0,
          paused: false,
          as_of: "2026-06-15T00:00:00Z",
        },
      ],
    } as never);

    const signal = new AbortController().signal;
    await expect(listQueues(signal)).resolves.toEqual([
      expect.objectContaining({ name: "tunnel" }),
    ]);
    expect(mockedQueues).toHaveBeenCalledWith({ signal });
  });

  it("maps the generated DLQ operation response", async () => {
    mockedDLQ.mockResolvedValueOnce({
      data: {
        queue: "default",
        dlq: [],
        count: 0,
      },
    } as never);

    await expect(listDLQ("default")).resolves.toEqual({
      queue: "default",
      dlq: [],
      count: 0,
    });
  });
});
