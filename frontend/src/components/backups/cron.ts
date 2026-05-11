/**
 * Hand-rolled cron-to-human helper for the backup wizard preview.
 *
 * `cronstrue` is the obvious dependency, but we don't have it in
 * package.json — and the schedule preview only needs to handle the typical
 * 5-field forms `(min hour dom month dow)` produced by our preset list and
 * by hand-typed expressions an admin is likely to enter. For anything more
 * exotic the helper falls back to the raw expression so the user still
 * sees what they typed.
 *
 * Supported syntax (per field):
 *   - `*`        any
 *   - `N`        literal (0-23 / 0-59 / 1-31 / 1-12 / 0-6)
 *   - `N,M,O`    list
 *   - `*\/N`     step (also `A-B/N`)
 *   - `A-B`      range
 *
 * Anything not matching falls through to the raw cron string.
 */

const DAYS = [
  'Sunday',
  'Monday',
  'Tuesday',
  'Wednesday',
  'Thursday',
  'Friday',
  'Saturday',
];

const MONTHS = [
  'January',
  'February',
  'March',
  'April',
  'May',
  'June',
  'July',
  'August',
  'September',
  'October',
  'November',
  'December',
];

function pad(n: number): string {
  return n.toString().padStart(2, '0');
}

/** Format an hour:minute pair for the description. 24h since the rest of
 *  Astronomer admin output uses UTC and 24h time. */
function fmtTime(hour: number, minute: number): string {
  return `${pad(hour)}:${pad(minute)}`;
}

/** Returns true when the field is the wildcard `*`. */
function isAny(field: string): boolean {
  return field === '*';
}

/** Returns the step `N` from `*\/N` or `null` when not a step expression. */
function stepValue(field: string): number | null {
  const m = field.match(/^\*\/(\d+)$/);
  return m ? parseInt(m[1], 10) : null;
}

/** Single literal (e.g. "5" / "23") or null. */
function literalValue(field: string): number | null {
  return /^\d+$/.test(field) ? parseInt(field, 10) : null;
}

/** Render a human-readable description. Returns the original cron string on
 *  unsupported syntax so the preview is never empty. */
export function cronToHuman(expr: string): string {
  const trimmed = (expr ?? '').trim();
  if (!trimmed) return '';
  // Recognised aliases.
  switch (trimmed.toLowerCase()) {
    case '@hourly':
      return 'Every hour';
    case '@daily':
    case '@midnight':
      return 'Daily at 00:00 UTC';
    case '@weekly':
      return 'Every Sunday at 00:00 UTC';
    case '@monthly':
      return 'On the 1st of each month at 00:00 UTC';
    case '@yearly':
    case '@annually':
      return 'Once a year on January 1st at 00:00 UTC';
  }

  const parts = trimmed.split(/\s+/);
  if (parts.length !== 5) return trimmed;
  const [minF, hourF, domF, monthF, dowF] = parts;

  const minLit = literalValue(minF);
  const hourLit = literalValue(hourF);
  const minStep = stepValue(minF);
  const hourStep = stepValue(hourF);

  const monthLit = literalValue(monthF);
  const domLit = literalValue(domF);
  const dowLit = literalValue(dowF);

  // Daily-ish patterns (every day at HH:MM).
  if (
    isAny(domF) &&
    isAny(monthF) &&
    isAny(dowF) &&
    minLit !== null &&
    hourLit !== null
  ) {
    return `Daily at ${fmtTime(hourLit, minLit)} UTC`;
  }

  // Weekly: every <day> at HH:MM.
  if (
    isAny(domF) &&
    isAny(monthF) &&
    dowLit !== null &&
    minLit !== null &&
    hourLit !== null
  ) {
    return `Every ${DAYS[dowLit % 7]} at ${fmtTime(hourLit, minLit)} UTC`;
  }

  // Monthly: on the Nth at HH:MM.
  if (
    isAny(monthF) &&
    isAny(dowF) &&
    domLit !== null &&
    minLit !== null &&
    hourLit !== null
  ) {
    return `On day ${domLit} of every month at ${fmtTime(hourLit, minLit)} UTC`;
  }

  // Yearly.
  if (
    monthLit !== null &&
    domLit !== null &&
    minLit !== null &&
    hourLit !== null &&
    isAny(dowF)
  ) {
    return `On ${MONTHS[(monthLit - 1) % 12]} ${domLit} at ${fmtTime(hourLit, minLit)} UTC`;
  }

  // Every N hours.
  if (hourStep !== null && minLit !== null && isAny(domF) && isAny(monthF) && isAny(dowF)) {
    if (minLit === 0) return `Every ${hourStep} hour${hourStep === 1 ? '' : 's'}`;
    return `Every ${hourStep} hour${hourStep === 1 ? '' : 's'} at :${pad(minLit)}`;
  }

  // Every N minutes.
  if (minStep !== null && isAny(hourF) && isAny(domF) && isAny(monthF) && isAny(dowF)) {
    return `Every ${minStep} minute${minStep === 1 ? '' : 's'}`;
  }

  // Every minute.
  if (isAny(minF) && isAny(hourF) && isAny(domF) && isAny(monthF) && isAny(dowF)) {
    return 'Every minute';
  }

  // Hourly at minute N.
  if (minLit !== null && isAny(hourF) && isAny(domF) && isAny(monthF) && isAny(dowF)) {
    return minLit === 0
      ? 'Every hour on the hour'
      : `Every hour at :${pad(minLit)}`;
  }

  return trimmed;
}

/** Lightweight syntactic check for the wizard. Returns true for anything
 *  the helper above can describe — we deliberately err on the lenient
 *  side so unusual but valid Velero expressions still go through. */
export function isPlausibleCron(expr: string): boolean {
  const trimmed = (expr ?? '').trim();
  if (!trimmed) return false;
  if (trimmed.startsWith('@')) {
    return ['@hourly', '@daily', '@midnight', '@weekly', '@monthly', '@yearly', '@annually'].includes(
      trimmed.toLowerCase(),
    );
  }
  const parts = trimmed.split(/\s+/);
  if (parts.length !== 5) return false;
  return parts.every((p) =>
    /^(\*|\d+(-\d+)?(\/\d+)?|\d+(,\d+)+|\*\/\d+)$/.test(p),
  );
}

export const CRON_PRESETS: { label: string; value: string }[] = [
  { label: 'Every hour', value: '0 * * * *' },
  { label: 'Every 6 hours', value: '0 */6 * * *' },
  { label: 'Daily at 02:00', value: '0 2 * * *' },
  { label: 'Daily at midnight', value: '0 0 * * *' },
  { label: 'Weekly (Sunday 02:00)', value: '0 2 * * 0' },
  { label: 'Monthly (1st at 02:00)', value: '0 2 1 * *' },
];
