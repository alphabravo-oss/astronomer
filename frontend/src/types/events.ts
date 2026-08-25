// --- Live Events (SSE) ---
//
// The backend pushes lifecycle events over `/api/v1/events/stream/`. Type
// names mirror the server-side constants in `internal/events/bus.go`. Page
// hooks should import the strongly-typed wrappers from `lib/live/envelope.ts`
// rather than handling raw frames here.

export type LiveEventType =
  | "cluster.connected"
  | "cluster.disconnected"
  | "cluster.heartbeat"
  | "cluster.metrics"
  | "cluster.status_changed"
  | "cluster.created"
  | "cluster.updated"
  | "cluster.deleted"
  | "cluster.k8s_changed"
  | "agent.reconnecting"
  | "agent.failed";

export interface LiveEvent<T = unknown> {
  id: number;
  type: LiveEventType | string;
  time: string;
  data?: T;
}

// --- Activity Feed ---

export interface ActivityEvent {
  id: string;
  type: "cluster" | "workload" | "deployment" | "rbac" | "system";
  action: string;
  message: string;
  user?: string;
  cluster?: string;
  namespace?: string;
  resource?: string;
  timestamp: string;
}
