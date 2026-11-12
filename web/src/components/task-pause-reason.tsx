"use client";

import { useI18n } from "@/i18n";
import { pauseKind } from "@/lib/task-holds";
import type { Task } from "@/lib/types";
import { cn } from "@/lib/utils";

// Says in words why the platform paused a task (budget reached, isolation unavailable). Renders nothing for
// any other task, including one that waits for a tool permission.
export function TaskPauseReason({ task, className }: { task: Task; className?: string }) {
  "use no memo";
  const { t: swt } = useI18n();
  const kind = pauseKind(task);
  if (!kind) return null;
  return (
    <p className={cn("text-amber-700 text-xs dark:text-amber-400", className)}>
      {kind === "budget" ? swt("budget.pausedTitle") : swt("execution.pausedShort")}
    </p>
  );
}
