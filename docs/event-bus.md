# Event bus delivery contract

The in-process event bus serves local SSE consumers and local webhook/SIEM
taps. With Redis configured, it also relays events to sibling server replicas
so their SSE clients see the same lifecycle stream.

`Bus.Publish` is deliberately latency-isolated from Redis: it broadcasts to
local subscribers, then attempts one nonblocking handoff to a bounded per-pod
relay queue. One worker serializes and publishes queued events. It retries a
failed Redis publish a bounded number of times and preserves queue order while
doing so; it never creates one goroutine per event.

Because serialization happens asynchronously, publishers own a simple data
contract: the `Data` value and maps/slices reachable from it must be treated as
immutable after `Publish` returns. Local subscribers and the Redis worker
observe the same event value. Callers that need to reuse a mutable buffer must
publish a caller-owned snapshot.

This path is best-effort and lossy by design. A full queue drops only the Redis
copy after local delivery has occurred. A prolonged Redis outage can therefore
leave remote SSE clients without some transient events; clients must continue
to treat SSE as an invalidation hint and re-fetch authoritative API state.
Queue size, drops, latency, health, and last success are exposed through the
metrics documented in [metrics-v1.md](metrics-v1.md). Configure capacity with
`EVENT_RELAY_QUEUE_CAPACITY` or Helm `server.eventRelayQueueCapacity`; the
default is 1024 and the hard maximum is 65536.

Redis echoes from the publishing pod are suppressed by origin. Events received
from another pod are marked `Remote=true`: SSE consumers deliver them, while
webhook and SIEM taps skip them so external integrations receive only the
origin pod's delivery. Shutdown stops new relay handoffs and drains for a
bounded interval; remaining events are dropped and counted.
