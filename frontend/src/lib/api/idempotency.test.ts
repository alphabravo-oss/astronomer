import { createIdempotencyKey } from "./idempotency";

const UUID_V4 =
  /^[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$/i;

it("creates unique RFC 4122 version-4 idempotency keys", () => {
  const keys = Array.from({ length: 20 }, () => createIdempotencyKey());
  expect(new Set(keys)).toHaveLength(keys.length);
  for (const key of keys) expect(key).toMatch(UUID_V4);
});
