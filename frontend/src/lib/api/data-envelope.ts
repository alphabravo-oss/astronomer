/** Return a generated API data envelope's payload or fail with operation context. */
export function requireEnvelopeData<T>(
  envelope: { data?: T },
  operation: string,
): T {
  if (envelope.data === undefined) {
    throw new Error(`${operation} returned an empty data envelope`);
  }
  return envelope.data;
}
