/**
 * Secret round-trip helpers for the form kit (P5.1).
 *
 * Two wire variants exist:
 *  - Marker variant (connectors, credentials): secrets come back as `""` with
 *    a sibling `__<name>_set: true` flag. The transport preserves that exact
 *    marker and submit helpers strip it from request bodies.
 *  - Sentinel variant (smtp): the stored password reads back as the literal
 *    `__redacted__` sentinel, stripped in the mutation (lib/api/settings.ts).
 *
 * D14 verification result (see secrets.test.tsx): TanStack Form v1 resets
 * `meta.isDirty` to false on `form.reset(newInitial)` — it does NOT survive.
 * That is exactly today's `touchedSecrets` semantics (connector-form clears
 * the touched map in the same effect that resets config when a fresh
 * `initial` arrives), so `stripUntouchedSecrets` keys off `meta.isDirty`
 * directly and no internal touched map is needed. A map that survived reset
 * would be destructive: after save → refetch → reset, a resubmit would send
 * the reset (empty) secret value and blank the stored ciphertext.
 */

/** True when the server holds a non-empty stored secret for `name`. */
export function isStoredSecret(
  config: Record<string, unknown> | undefined | null,
  name: string,
): boolean {
  if (!config) return false;
  return Boolean(config[`__${name}_set`]);
}

/** Minimal structural slice of a TanStack form the strip helper needs.
 *  (Method syntax keeps typed form APIs assignable via bivariance.) */
export interface SecretFormLike {
  getFieldMeta(field: never): { isDirty: boolean } | undefined;
}

/**
 * Build a submit body from `value`, dropping:
 *  - secret keys the user did not touch this session (per-field
 *    `meta.isDirty`), so the backend's preserve-on-empty merge keeps the
 *    existing ciphertext;
 *  - the echoed `__<name>_set` stored-secret markers, so they cannot persist
 *    as garbage config keys.
 *
 * `fieldPrefix` maps `value`'s keys onto form field names when the secrets
 * live in a nested object (e.g. `'config.'` when the form data is
 * `{ config: {...} }`).
 */
export function stripUntouchedSecrets(
  value: Record<string, unknown>,
  form: SecretFormLike,
  secretKeys: readonly string[],
  fieldPrefix = "",
): Record<string, unknown> {
  const secrets = new Set(secretKeys);
  const cleaned: Record<string, unknown> = {};
  for (const [k, v] of Object.entries(value)) {
    if (k.startsWith("__") && k.endsWith("_set")) continue;
    if (
      secrets.has(k) &&
      !form.getFieldMeta(`${fieldPrefix}${k}` as never)?.isDirty
    )
      continue;
    cleaned[k] = v;
  }
  return cleaned;
}
