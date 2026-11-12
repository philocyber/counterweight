"use client";

import * as React from "react";

import { toast } from "sonner";

import { Label } from "@/components/ui/label";
import { Switch } from "@/components/ui/switch";
import { api } from "@/lib/api";
import type { TaskAutomation } from "@/lib/types";

// Per-task automation of code review steps. Both switches start off: the operator
// decides which findings get verified and which get a fix.
export function useTaskAutomation(taskId: string) {
  const [automation, setAutomation] = React.useState<TaskAutomation | null>(null);
  React.useEffect(() => {
    let disposed = false;
    api
      .taskAutomation(taskId)
      .then((a) => {
        if (!disposed) setAutomation(a);
      })
      .catch(() => {
        if (!disposed) setAutomation(null);
      });
    return () => {
      disposed = true;
    };
  }, [taskId]);
  const update = React.useCallback(
    async (patch: Partial<Pick<TaskAutomation, "verify" | "mitigate">>) => {
      try {
        setAutomation(await api.setTaskAutomation(taskId, patch));
      } catch (e) {
        toast.error((e as Error).message);
      }
    },
    [taskId],
  );
  return { automation, update };
}

export function TaskAutomationSwitch({
  id,
  checked,
  label,
  description,
  onChange,
}: {
  id: string;
  checked: boolean;
  label: string;
  description: string;
  onChange: (checked: boolean) => void;
}) {
  return (
    <div className="flex items-start justify-between gap-4 rounded-lg border p-3">
      <div className="flex min-w-0 flex-col gap-1">
        <Label htmlFor={id}>{label}</Label>
        <p className="text-muted-foreground text-xs">{description}</p>
      </div>
      <Switch id={id} checked={checked} onCheckedChange={onChange} />
    </div>
  );
}
