"use client";

import * as React from "react";

import Link from "next/link";

import {
  CheckIcon,
  CircleAlertIcon,
  MinusIcon,
  PlayIcon,
  ShieldAlertIcon,
  ShieldCheckIcon,
  ShieldIcon,
} from "lucide-react";

import { Alert, AlertDescription, AlertTitle } from "@/components/ui/alert";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Spinner } from "@/components/ui/spinner";
import { Tooltip, TooltipContent, TooltipTrigger } from "@/components/ui/tooltip";
import { type TFunction, useI18n } from "@/i18n";
import { api } from "@/lib/api";
import { shortDigest } from "@/lib/execution-modes";
import type { TaskExecution } from "@/lib/types";
import { cn } from "@/lib/utils";

// What the recorded mode covers, in plain words. An assumed record is not evidence of any mode.
export function executionTip(execution: TaskExecution, t: TFunction, notRun = false): string {
  if (execution.assumed) return t(notRun ? "execution.tipNotRun" : "execution.tipAssumed");
  const parts: string[] = [];
  // A source review records confined-files even when the global mode is container: it ran no commands.
  if (execution.mode === "confined-files" && execution.source_only) parts.push(t("execution.tipSourceOnly"));
  else if (execution.mode === "confined-files") parts.push(t("execution.tipConfined"));
  else if (execution.mode === "host") parts.push(t("execution.tipHost"));
  else if (execution.mode === "container") parts.push(t("execution.tipContainer"));
  else parts.push(t("execution.tipUnknownMode"));
  if (execution.mixed) parts.push(t("execution.tipMixed"));
  return parts.join(" ");
}

// A task whose status means it has not started, and which has no execution record, simply has not run;
// an assumed record on any other task predates recording.
export function hasNotRun(execution: TaskExecution, status?: string): boolean {
  return execution.assumed && (status === "created" || status === "queued");
}

export function executionLabel(execution: TaskExecution, t: TFunction, notRun = false): string {
  if (execution.assumed) return t(notRun ? "execution.badgeNotRun" : "execution.badgeNotRecorded");
  if (execution.mixed) return t("execution.badgeMixed");
  if (execution.mode === "confined-files") return t("execution.badgeConfined");
  if (execution.mode === "host") return t("execution.badgeHost");
  if (execution.mode === "container") return t("execution.badgeContainer");
  return t("execution.badgeUnknownMode", { p0: execution.mode });
}

// Compact badge for the task header and the overview facts. Renders nothing when the server does not
// report execution, so an older backend shows no indicator.
export function ExecutionBadge({
  execution,
  status,
  className,
}: {
  execution?: TaskExecution;
  status?: string;
  className?: string;
}) {
  "use no memo";
  const { t: swt, locale: swLocale } = useI18n();
  if (!execution) return null;
  const notRun = hasNotRun(execution, status);
  const label = executionLabel(execution, swt, notRun);
  const tip = executionTip(execution, swt, notRun);
  const confined =
    !execution.assumed && !execution.mixed && (execution.mode === "confined-files" || execution.mode === "container");
  const Icon = execution.assumed ? ShieldIcon : confined ? ShieldCheckIcon : ShieldAlertIcon;
  return (
    <Tooltip>
      <TooltipTrigger asChild>
        <Badge
          variant="outline"
          tabIndex={0}
          aria-label={`${label}. ${tip}`}
          className={cn("shrink-0", !confined && !execution.assumed && "text-amber-700 dark:text-amber-400", className)}
        >
          <Icon aria-hidden="true" />
          {label}
        </Badge>
      </TooltipTrigger>
      <TooltipContent className="max-w-xs">{tip}</TooltipContent>
    </Tooltip>
  );
}

// What the latest recorded run confined, as yes/no facts. Not shown for an assumed record (nothing was
// recorded) or a mixed one (the capabilities describe only the latest run).
function CapabilityFacts({ execution }: { execution: TaskExecution }) {
  "use no memo";
  const { t: swt, locale: swLocale } = useI18n();
  const caps = execution.capabilities;
  const facts: [string, boolean][] = [
    [swt("execution.taskFactFiles"), !!caps?.files_confined],
    [swt("execution.taskFactCommands"), execution.mode === "container" && !!caps?.processes_confined],
    [swt("execution.taskFactCredentials"), !!caps?.credentials_isolated],
    [swt("execution.taskFactNetwork"), !!caps?.network_confined],
  ];
  return (
    <ul className="flex flex-col gap-0.5">
      {facts.map(([label, on]) => (
        <li key={label} className="flex items-start gap-2 text-xs">
          {on ? (
            <CheckIcon className="mt-0.5 size-3.5 shrink-0 text-emerald-700 dark:text-emerald-400" aria-hidden="true" />
          ) : (
            <MinusIcon className="mt-0.5 size-3.5 shrink-0 text-muted-foreground" aria-hidden="true" />
          )}
          <span className="min-w-0 break-words">
            {label}: {on ? swt("execution.yes") : swt("execution.no")}
          </span>
        </li>
      ))}
    </ul>
  );
}

// Facts row of the overview tab: the badge plus the sentence the badge's tooltip carries.
export function ExecutionFact({ execution, status }: { execution?: TaskExecution; status?: string }) {
  "use no memo";
  const { t: swt, locale: swLocale } = useI18n();
  if (!execution) return null;
  const recorded = !execution.assumed && !execution.mixed;
  return (
    <div className="min-w-0">
      <div className="text-xs text-muted-foreground">{swt("execution.factLabel")}</div>
      <div className="mt-1 flex flex-col items-start gap-1">
        <ExecutionBadge execution={execution} status={status} />
        <p className="text-muted-foreground text-xs">{executionTip(execution, swt, hasNotRun(execution, status))}</p>
        {recorded && <CapabilityFacts execution={execution} />}
        {recorded && execution.mode === "container" && execution.image_digest && (
          <p className="break-all text-muted-foreground text-xs">
            {swt("execution.imageDigest", { p0: shortDigest(execution.image_digest) })}
          </p>
        )}
      </div>
    </div>
  );
}

// Shown on a task that was paused because file isolation could not be established. The task stays paused
// until the cause is fixed and the user resumes it; there is no fallback to running unconfined.
export function ExecutionPausedAlert({
  taskId,
  reason,
  onResumed,
}: {
  taskId: string;
  reason?: string;
  onResumed: () => void;
}) {
  "use no memo";
  const { t: swt, locale: swLocale } = useI18n();
  const [resuming, setResuming] = React.useState(false);
  const [error, setError] = React.useState("");

  const resume = async () => {
    setResuming(true);
    setError("");
    try {
      await api.controlTask(taskId, "resume");
      onResumed();
    } catch (e) {
      setError((e as Error).message);
    } finally {
      setResuming(false);
    }
  };

  return (
    <Alert className="border-amber-500/40 bg-amber-500/10" role="alert">
      <CircleAlertIcon />
      <AlertTitle>{swt("execution.pausedTitle")}</AlertTitle>
      <AlertDescription className="space-y-2">
        <p className="break-words">{reason || swt("execution.pausedFallback")}</p>
        <p>{swt("execution.pausedHint")}</p>
        <div className="flex flex-wrap items-center gap-2">
          <Button size="sm" variant="outline" asChild>
            <Link href="/system/settings/#execution">{swt("execution.openSettings")}</Link>
          </Button>
          <Button size="sm" onClick={() => void resume()} disabled={resuming}>
            {resuming ? <Spinner data-icon="inline-start" /> : <PlayIcon data-icon="inline-start" />}
            {swt("execution.resume")}
          </Button>
        </div>
        {error && <p className="break-words text-destructive">{swt("execution.resumeFailed", { p0: error })}</p>}
      </AlertDescription>
    </Alert>
  );
}
