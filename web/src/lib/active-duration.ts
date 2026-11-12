export interface PermissionHold {
  started_at: string;
  ended_at?: string | null;
}

// Subtract the union of recorded permission holds, clipped to this run's span.
// An open hold also excludes any shutdown/drain activity emitted after the stop.
export function activeDurationMs(start: number, end: number, holds: PermissionHold[] = []): number {
  if (!Number.isFinite(start) || !Number.isFinite(end) || end <= start) return 0;
  const intervals = holds.flatMap((hold) => {
    const from = Math.max(start, Date.parse(hold.started_at));
    const to = Math.min(end, hold.ended_at ? Date.parse(hold.ended_at) : end);
    return Number.isFinite(from) && Number.isFinite(to) && to > from ? [[from, to]] : [];
  }).sort((a, b) => a[0] - b[0]);
  let waited = 0;
  let through = start;
  for (const [from, to] of intervals) {
    waited += Math.max(0, to - Math.max(from, through));
    through = Math.max(through, to);
  }
  return Math.max(0, end - start - waited);
}
