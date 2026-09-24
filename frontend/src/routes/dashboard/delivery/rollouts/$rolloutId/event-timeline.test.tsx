import {
  act,
  fireEvent,
  render,
  screen,
  waitFor,
} from "@testing-library/react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { RolloutEventTimeline } from "./-event-timeline";
import { listDeliveryRolloutEvents } from "@/lib/api/delivery-rollouts";
import { queryKeys } from "@/lib/query-keys";

vi.mock("@/lib/api/delivery-rollouts", () => ({
  listDeliveryRolloutEvents: vi.fn(),
}));
it("pages persisted rollout events and hides cached timeline data after denial", async () => {
  vi.mocked(listDeliveryRolloutEvents).mockImplementation(
    async (_, __, params) =>
      ({
        data: [
          {
            id: `event-${params?.offset || 0}`,
            eventType: params?.offset ? "late_transition" : "first_transition",
            occurredAt: "2026-09-22T00:00:00Z",
            toState: "ready",
          },
        ],
        pagination: {
          limit: 25,
          offset: params?.offset || 0,
          has_more: !params?.offset,
          next_offset: params?.offset ? null : 225,
        },
      }) as Awaited<ReturnType<typeof listDeliveryRolloutEvents>>,
  );
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false, gcTime: 0 } },
  });
  render(
    <QueryClientProvider client={client}>
      <RolloutEventTimeline
        projectId="project-a"
        rolloutId="rollout-a"
        allowed
      />
    </QueryClientProvider>,
  );
  await screen.findByText("first transition");
  fireEvent.click(
    screen.getByRole("button", { name: "Next rollout events page" }),
  );
  await screen.findByText("late transition");
  expect(listDeliveryRolloutEvents).toHaveBeenCalledWith(
    "project-a",
    "rollout-a",
    { limit: 25, offset: 225 },
    expect.any(AbortSignal),
  );
  vi.mocked(listDeliveryRolloutEvents).mockImplementation(
    async (_, __, params) => {
      if (params?.offset) throw { status: 403 };
      return {
        data: [],
        pagination: {
          limit: 25,
          offset: 0,
          has_more: false,
          next_offset: null,
        },
      };
    },
  );
  await act(async () => {
    await client.invalidateQueries({ queryKey: queryKeys.delivery.all });
  });
  await screen.findByText("Permission required");
  expect(screen.queryByText("late transition")).not.toBeInTheDocument();
  fireEvent.click(
    screen.getByRole("button", { name: "Previous rollout events page" }),
  );
  await waitFor(() =>
    expect(screen.queryByText("Permission required")).not.toBeInTheDocument(),
  );
});
