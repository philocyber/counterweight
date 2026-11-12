"use client";

import * as React from "react";

import Link from "next/link";

import { CheckIcon, CopyIcon, MinusIcon, RefreshCwIcon, ShieldAlertIcon, ShieldCheckIcon } from "lucide-react";
import { toast } from "sonner";

import { useConfirm } from "@/components/confirm-dialog";
import { ErrorState } from "@/components/error-state";
import { Alert, AlertDescription } from "@/components/ui/alert";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/ui/card";
import { Label } from "@/components/ui/label";
import { RadioGroup, RadioGroupItem } from "@/components/ui/radio-group";
import { Spinner } from "@/components/ui/spinner";
import { Switch } from "@/components/ui/switch";
import { usePolling } from "@/hooks/use-polling";
import { useI18n } from "@/i18n";
import { getIntlLocale } from "@/i18n/runtime";
import { api, isNotAvailable } from "@/lib/api";
import {
  EXECUTION_MODES,
  type ExecutionMode,
  familyLabel,
  hasModeInfo,
  isCommand,
  KNOWN_TOOL_FAMILIES,
  type ModeOption,
  modeOption,
  selectedMode,
  shortDigest,
} from "@/lib/execution-modes";
import type { ExecutionStatus } from "@/lib/types";
import { cn, copyText } from "@/lib/utils";

// Rows of "what is isolated right now": a check or a dash plus words, never colour alone.
function CapabilityRow({ on, children }: { on: boolean; children: React.ReactNode }) {
  const Icon = on ? CheckIcon : MinusIcon;
  return (
    <li className="flex items-start gap-2 text-sm">
      <Icon
        className={`mt-0.5 size-4 shrink-0 ${on ? "text-emerald-700 dark:text-emerald-400" : "text-muted-foreground"}`}
        aria-hidden="true"
      />
      <span className="min-w-0 break-words">{children}</span>
    </li>
  );
}

// A fixing step. A shell command goes in a copyable code block; anything else is a sentence.
function FixStep({ fix }: { fix: string }) {
  "use no memo";
  const { t: swt, locale: swLocale } = useI18n();
  const copy = async () => {
    if (await copyText(fix.trim())) toast.success(swt("execution.copied"));
    else toast.error(swt("execution.copyFailed"));
  };
  return (
    <div className="grid gap-1 text-xs">
      <span className="font-medium">{swt("execution.modeFixLabel")}</span>
      {isCommand(fix) ? (
        <div className="flex min-w-0 items-center gap-2 rounded-md border bg-muted px-2 py-1.5">
          <code className="min-w-0 flex-1 overflow-x-auto whitespace-nowrap font-mono">{fix.trim()}</code>
          <Button
            type="button"
            size="icon-sm"
            variant="ghost"
            aria-label={swt("execution.copyCommand")}
            onClick={() => void copy()}
          >
            <CopyIcon aria-hidden="true" />
          </Button>
        </div>
      ) : (
        <span className="break-words text-muted-foreground">{fix}</span>
      )}
    </div>
  );
}

// What container mode does not cover. The unavailable families come from the server when container mode is
// the selected one; otherwise the known families are listed.
function ContainerLimits({ status }: { status: ExecutionStatus }) {
  "use no memo";
  const { t: swt, locale: swLocale } = useI18n();
  const families = status.unavailable_tool_families?.length ? status.unavailable_tool_families : KNOWN_TOOL_FAMILIES;
  return (
    <div className="grid gap-1.5">
      <p className="font-medium text-xs">{swt("execution.containerLimitsTitle")}</p>
      <ul className="flex flex-col gap-1">
        <CapabilityRow on={false}>{swt("execution.limitNetwork")}</CapabilityRow>
        <CapabilityRow on={false}>{swt("execution.limitCapture")}</CapabilityRow>
        <CapabilityRow on={false}>
          {swt("execution.limitFamilies", { p0: families.map((f) => familyLabel(f, swt)).join(", ") })}
        </CapabilityRow>
        <CapabilityRow on={false}>{swt("execution.limitCompose")}</CapabilityRow>
        <CapabilityRow on={false}>{swt("execution.limitOperatorChat")}</CapabilityRow>
      </ul>
    </div>
  );
}

// One radio card per mode: what it does, its real status and, when it cannot be chosen, why and how to fix it.
function ModeCard({
  option,
  current,
  disabled,
  status,
}: {
  option: ModeOption;
  current: boolean;
  disabled: boolean;
  status: ExecutionStatus;
}) {
  "use no memo";
  const { t: swt, locale: swLocale } = useI18n();
  const { mode, state } = option;
  const id = `execution-mode-${mode}`;
  const title =
    mode === "off"
      ? swt("execution.modeOffTitle")
      : mode === "confined-files"
        ? swt("execution.modeConfinedTitle")
        : swt("execution.modeContainerTitle");
  const body =
    mode === "off"
      ? swt("execution.modeOffBody")
      : mode === "confined-files"
        ? swt("execution.modeConfinedBody")
        : swt("execution.modeContainerBody");
  const stateText =
    state === "ready"
      ? swt("execution.modeStateReady")
      : state === "failed"
        ? swt("execution.modeStateFailed")
        : swt("execution.modeStateNotAvailable");
  const image = option.image ?? (mode === "container" ? status.image : undefined);
  const digest = option.imageDigest ?? (mode === "container" ? status.image_digest : undefined);
  return (
    <div className={cn("rounded-lg border p-3", current && "border-primary bg-primary/5")}>
      <div className="flex items-start gap-3">
        <RadioGroupItem
          id={id}
          value={mode}
          className="mt-0.5"
          disabled={disabled || (!current && state !== "ready")}
        />
        <div className="grid min-w-0 flex-1 gap-1.5">
          <div className="flex flex-wrap items-center gap-2">
            <Label htmlFor={id} className="text-sm">
              {title}
            </Label>
            {mode === "confined-files" && <Badge variant="secondary">{swt("execution.modeDefaultBadge")}</Badge>}
            <Badge variant={state === "failed" ? "destructive" : "outline"}>
              {state === "ready" && <CheckIcon aria-hidden="true" />}
              {stateText}
            </Badge>
          </div>
          <p className="text-muted-foreground text-xs">{body}</p>
          {state !== "ready" && option.reason && <p className="break-words text-xs">{option.reason}</p>}
          {state !== "ready" && option.fix && <FixStep fix={option.fix} />}
          {mode === "container" && state === "ready" && image && (
            <p className="break-all text-muted-foreground text-xs">
              {swt("execution.imageRef", { p0: image })}
              {digest && (
                <>
                  <br />
                  {swt("execution.imageDigest", { p0: shortDigest(digest) })}
                </>
              )}
            </p>
          )}
          {mode === "container" && <ContainerLimits status={status} />}
        </div>
      </div>
    </div>
  );
}

// The execution isolation control. It sits above the settings sections so it is always in view, and renders
// nothing on a server that predates the feature.
export function ExecutionIsolationCard() {
  "use no memo";
  const { t: swt, locale: swLocale } = useI18n();
  const [confirm, confirmDialog] = useConfirm();

  const [status, setStatus] = React.useState<ExecutionStatus | null>(null);
  const [unavailable, setUnavailable] = React.useState(false);
  const [loadError, setLoadError] = React.useState<string | null>(null);
  const [saving, setSaving] = React.useState(false);
  const [checking, setChecking] = React.useState(false);
  const [modeError, setModeError] = React.useState<string | null>(null);

  const load = React.useCallback(
    () =>
      api
        .executionStatus()
        .then((s) => {
          setStatus(s);
          setUnavailable(false);
          setLoadError(null);
        })
        .catch((e: Error) => {
          if (isNotAvailable(e)) setUnavailable(true);
          else setLoadError(e.message);
        }),
    [],
  );
  usePolling(load, 30_000);

  const changeSetting = async (next: boolean) => {
    if (!next) {
      const ok = await confirm({
        title: swt("execution.confirmOffTitle"),
        description: swt("execution.confirmOffBody"),
        confirmLabel: swt("execution.confirmOffAction"),
        destructive: true,
      });
      if (!ok) return;
    }
    setSaving(true);
    try {
      await api.setSettings({ isolation: next });
      await load();
    } catch (e) {
      toast.error(swt("states.settingSaveFailed", { p0: swt("execution.cardTitle"), p1: (e as Error).message }));
    } finally {
      setSaving(false);
    }
  };

  const changeMode = async (next: ExecutionMode) => {
    if (next === "off") {
      const ok = await confirm({
        title: swt("execution.confirmOffTitle"),
        description: swt("execution.confirmOffBody"),
        confirmLabel: swt("execution.confirmOffAction"),
        destructive: true,
      });
      if (!ok) return;
    }
    setSaving(true);
    setModeError(null);
    try {
      await api.setSettings({ isolation_mode: next });
    } catch (e) {
      // The server refuses a mode whose backend is not ready (409) and says why; the selection stays as it was.
      setModeError((e as Error).message);
    } finally {
      await load();
      setSaving(false);
    }
  };

  const recheck = async () => {
    setChecking(true);
    try {
      const next = await api.recheckExecution();
      setStatus(next);
      setLoadError(null);
      if (next.status === "ready") toast.success(swt("execution.recheckReady"));
      else if (next.status === "off") toast.info(swt("execution.recheckOff"));
      else toast.error(swt("execution.recheckFailed"));
    } catch (e) {
      toast.error(swt("execution.recheckError", { p0: (e as Error).message }));
    } finally {
      setChecking(false);
    }
  };

  if (unavailable) return null;
  if (loadError !== null && !status) {
    return (
      <Card id="execution" className="scroll-mt-20 border-primary/40 py-0">
        <ErrorState title={swt("execution.loadFailed")} detail={loadError} onRetry={() => void load()} />
      </Card>
    );
  }
  if (!status) return null;

  const modes = hasModeInfo(status);
  const current = selectedMode(status);
  const ready = status.status === "ready";
  const failed = status.status === "failed";
  const statusText = ready
    ? swt("execution.statusReady")
    : failed
      ? swt("execution.statusFailed")
      : status.status === "off"
        ? swt("execution.statusOff")
        : swt("execution.statusUnknown");
  const caps = status.capabilities;
  const skipped = (status.skipped ?? []).map((item) => (typeof item === "string" ? item : JSON.stringify(item)));
  const lastTest = status.last_self_test ? new Date(status.last_self_test) : null;
  const HeaderIcon = ready ? ShieldCheckIcon : ShieldAlertIcon;

  return (
    <Card id="execution" className="scroll-mt-20 gap-0 border-primary/40 py-0">
      {confirmDialog}
      <CardHeader className="border-b bg-primary/5 py-4">
        <CardTitle className="flex flex-wrap items-center gap-2 text-base">
          <HeaderIcon className="size-4" aria-hidden="true" />
          {swt("execution.cardTitle")}
          <Badge variant="outline">{swt("execution.cardBadge")}</Badge>
        </CardTitle>
        <CardDescription>{swt("execution.cardDescription")}</CardDescription>
      </CardHeader>
      <CardContent className="grid gap-5 py-4 lg:grid-cols-2">
        <div className="flex min-w-0 flex-col gap-4">
          {modes ? (
            <div className="flex flex-col gap-2">
              <RadioGroup
                aria-label={swt("execution.modeGroupLabel")}
                value={current}
                onValueChange={(v) => void changeMode(v as ExecutionMode)}
              >
                {EXECUTION_MODES.map((mode) => (
                  <ModeCard
                    key={mode}
                    option={modeOption(status, mode)}
                    current={mode === current}
                    disabled={saving}
                    status={status}
                  />
                ))}
              </RadioGroup>
              {modeError && (
                <Alert variant="destructive" role="alert">
                  <AlertDescription className="break-words">
                    {swt("execution.modeChangeFailed", { p0: modeError })}
                  </AlertDescription>
                </Alert>
              )}
              <p className="text-muted-foreground text-xs">{swt("execution.modeApplyHint")}</p>
              {status.tasks_on_earlier_mode?.length ? (
                <Alert role="status">
                  <AlertDescription className="break-words">
                    {swt("execution.earlierModeTasks", { p0: status.tasks_on_earlier_mode.join(", ") })}
                  </AlertDescription>
                </Alert>
              ) : null}
            </div>
          ) : (
            <div className="flex items-start justify-between gap-4">
              <div className="grid min-w-0 gap-0.5">
                <Label htmlFor="execution-isolation" className="text-sm">
                  {swt("execution.switchLabel")}
                </Label>
                <p className="text-muted-foreground text-xs">{swt("execution.switchHint")}</p>
              </div>
              <Switch
                id="execution-isolation"
                checked={status.setting}
                disabled={saving}
                onCheckedChange={(v) => void changeSetting(v)}
              />
            </div>
          )}

          <div className="flex flex-wrap items-center gap-2">
            <span className="text-muted-foreground text-sm">{swt("execution.statusLabel")}</span>
            <Badge variant={failed ? "destructive" : "outline"}>
              {ready && <CheckIcon aria-hidden="true" />}
              {statusText}
            </Badge>
          </div>
          {status.reason && (
            <Alert variant={failed ? "destructive" : "default"}>
              <AlertDescription>
                <span className="font-medium">{swt("execution.reasonLabel")}:</span> {status.reason}
              </AlertDescription>
            </Alert>
          )}
        </div>

        <div className="flex min-w-0 flex-col gap-3">
          <p className="font-medium text-sm">{swt("execution.isolatedNow")}</p>
          <ul className="flex flex-col gap-1.5">
            <CapabilityRow on={!!caps?.files_confined}>
              {caps?.files_confined ? swt("execution.filesOn") : swt("execution.filesOff")}
            </CapabilityRow>
            <CapabilityRow on={!!caps?.processes_confined}>
              {caps?.processes_confined ? swt("execution.processesOn") : swt("execution.processesOff")}
            </CapabilityRow>
            {caps?.credentials_isolated !== undefined && (
              <CapabilityRow on={caps.credentials_isolated}>
                {caps.credentials_isolated ? swt("execution.credentialsOn") : swt("execution.credentialsOff")}
              </CapabilityRow>
            )}
            <CapabilityRow on={!!caps?.network_confined}>
              {caps?.network_confined ? swt("execution.networkOn") : swt("execution.networkOff")}
            </CapabilityRow>
          </ul>
          {status.description && <p className="text-muted-foreground text-xs">{status.description}</p>}
          {skipped.length > 0 && (
            <p className="text-amber-700 text-xs dark:text-amber-400">
              {swt("execution.skippedProbes", { p0: skipped.join(", ") })}
            </p>
          )}
          <div className="flex flex-wrap items-center gap-3">
            <Button type="button" size="sm" variant="outline" onClick={() => void recheck()} disabled={checking}>
              {checking ? <Spinner data-icon="inline-start" /> : <RefreshCwIcon data-icon="inline-start" />}
              {checking ? swt("execution.rechecking") : swt("execution.recheck")}
            </Button>
            <span className="text-muted-foreground text-xs">
              {lastTest && !Number.isNaN(lastTest.getTime())
                ? swt("execution.lastSelfTest", { p0: lastTest.toLocaleString(getIntlLocale()) })
                : swt("execution.neverTested")}
            </span>
          </div>
          <Link href="/docs/#safety" className="w-fit font-medium text-primary text-xs hover:underline">
            {swt("execution.docsLink")}
          </Link>
        </div>
      </CardContent>
    </Card>
  );
}
