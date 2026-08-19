// Map reconciler statuses onto StatusBadge / statusToneByKey.
// `completed` is a first-class success tone, so it stays as-is.
// Raw `running` is success-green in the palette — remap to `progressing`
// so in-flight ops keep an info/progress tone. `failed`/`superseded` → error.
export function mapLoggingOperationStatus(s: string): string {
  switch (s) {
    case 'completed':
      return 'completed';
    case 'running':
      return 'progressing';
    case 'pending':
      return 'pending';
    case 'failed':
    case 'superseded':
      return 'error';
    default:
      return 'unknown';
  }
}

export function truncate(s: string, max: number): string {
  if (s.length <= max) return s;
  return s.slice(0, max - 1) + '…';
}
