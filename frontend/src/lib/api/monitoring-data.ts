export function requireMonitoringData<T>(
  response: { data?: T },
  operation: string,
): T {
  if (response.data === undefined) {
    throw new Error(`${operation} returned no data payload`);
  }
  return response.data;
}
