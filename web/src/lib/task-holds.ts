import type { Task } from "./types";

type Hold = NonNullable<Task["approval_state"]>["holds"][number];

// Holds the platform imposes itself, as opposed to a pending tool permission. They are classified by
// outcome only; the reason text is never parsed.
export type PlatformHoldKind = "budget" | "execution";

export function platformHoldKind(hold: Pick<Hold, "outcome">): PlatformHoldKind | null {
  return hold.outcome === "budget" || hold.outcome === "execution" ? hold.outcome : null;
}

type WithHolds = { approval_state?: { holds: Hold[] } } | null | undefined;

function openHolds(task: WithHolds): Hold[] {
  return task?.approval_state?.holds.filter((hold) => !hold.ended_at) ?? [];
}

// approvalHold is the open hold that waits for a person to review a tool permission, if any.
export function approvalHold(task: WithHolds): Hold | undefined {
  return openHolds(task).find((hold) => platformHoldKind(hold) === null);
}

// platformHold is the open budget or isolation hold, if any.
export function platformHold(task: WithHolds): (Hold & { kind: PlatformHoldKind }) | undefined {
  for (const hold of openHolds(task)) {
    const kind = platformHoldKind(hold);
    if (kind) return { ...hold, kind };
  }
  return undefined;
}

// pauseKind says why a task is paused by the platform. A task that also waits for a permission counts as
// waiting for approval, so this is null for it.
export function pauseKind(task: WithHolds): PlatformHoldKind | null {
  return approvalHold(task) ? null : (platformHold(task)?.kind ?? null);
}
