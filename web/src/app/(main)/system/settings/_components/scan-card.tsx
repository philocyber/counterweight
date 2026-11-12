"use client";

import * as React from "react";

import { CheckIcon, MinusIcon, ScanSearchIcon } from "lucide-react";
import { toast } from "sonner";

import { ErrorState } from "@/components/error-state";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/ui/card";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { Switch } from "@/components/ui/switch";
import { type MessageKey, useI18n } from "@/i18n";
import { api, isNotAvailable } from "@/lib/api";
import type { ScanSettings } from "@/lib/types";

const SOURCE_LABELS: Record<string, MessageKey> = {
  configured: "scan.sourceConfigured",
  managed: "scan.sourceManaged",
  path: "scan.sourcePath",
};

// Settings for the supplementary scanners of code reviews. Renders nothing on a server without them.
export function ScanSettingsCard() {
  "use no memo";
  const { t: swt, locale: swLocale } = useI18n();

  const [settings, setSettings] = React.useState<ScanSettings | null>(null);
  const [unavailable, setUnavailable] = React.useState(false);
  const [loadError, setLoadError] = React.useState<string | null>(null);
  const [paths, setPaths] = React.useState<Record<string, string>>({});
  const [saving, setSaving] = React.useState(false);

  const apply = React.useCallback((s: ScanSettings) => {
    setSettings(s);
    setPaths({ ...(s.paths ?? {}) });
  }, []);

  const load = React.useCallback(() => {
    setLoadError(null);
    api
      .scanSettings()
      .then(apply)
      .catch((e: Error) => {
        if (isNotAvailable(e)) setUnavailable(true);
        else setLoadError(e.message);
      });
  }, [apply]);

  React.useEffect(() => {
    load();
  }, [load]);

  const save = (patch: { enabled?: boolean; paths?: Record<string, string | null> }, quiet = false) => {
    setSaving(true);
    api
      .setScanSettings(patch)
      .then((s) => {
        apply(s);
        if (!quiet) toast.success(swt("scan.pathSaved"));
      })
      .catch((e: Error) => toast.error(swt("scan.settingsSaveFailed", { p0: e.message })))
      .finally(() => setSaving(false));
  };

  if (unavailable) return null;
  if (loadError !== null && !settings) {
    return (
      <Card className="py-0">
        <ErrorState title={swt("scan.settingsLoadFailed")} detail={loadError} onRetry={load} />
      </Card>
    );
  }
  if (!settings) return null;

  const changedPaths: Record<string, string> = {};
  for (const scanner of settings.scanners) {
    const next = (paths[scanner.key] ?? "").trim();
    if (next !== (settings.paths?.[scanner.key] ?? "")) changedPaths[scanner.key] = next;
  }
  const dirty = Object.keys(changedPaths).length > 0;

  return (
    <Card id="scanners" className="scroll-mt-20">
      <CardHeader>
        <CardTitle className="flex items-center gap-2 text-base">
          <ScanSearchIcon className="size-4" aria-hidden="true" />
          {swt("scan.cardTitle")}
        </CardTitle>
        <CardDescription>{swt("scan.cardDescription")}</CardDescription>
      </CardHeader>
      <CardContent className="flex flex-col gap-5">
        <div className="flex items-start justify-between gap-4">
          <div className="grid min-w-0 gap-0.5">
            <Label htmlFor="scan-enabled" className="text-sm">
              {swt("scan.switchLabel")}
            </Label>
            <p className="text-muted-foreground text-xs">{swt("scan.switchHint")}</p>
          </div>
          <Switch
            id="scan-enabled"
            checked={settings.enabled}
            disabled={saving}
            onCheckedChange={(v) => save({ enabled: v }, true)}
          />
        </div>
        {settings.note && <p className="text-muted-foreground text-xs">{settings.note}</p>}

        {settings.scanners.map((scanner) => {
          const sourceKey = scanner.source ? SOURCE_LABELS[scanner.source] : undefined;
          return (
            <div key={scanner.key} className="grid gap-2 border-t pt-4">
              <div className="flex flex-wrap items-center gap-2">
                <span className="font-medium text-sm">{scanner.key}</span>
                <Badge variant={scanner.available ? "outline" : "secondary"}>
                  {scanner.available ? <CheckIcon aria-hidden="true" /> : <MinusIcon aria-hidden="true" />}
                  {scanner.available ? swt("scan.availableLabel") : swt("scan.unavailableLabel")}
                </Badge>
                {scanner.available && sourceKey && (
                  <span className="text-muted-foreground text-xs">{swt(sourceKey)}</span>
                )}
              </div>
              {scanner.purpose && <p className="text-muted-foreground text-xs">{scanner.purpose}</p>}
              {!scanner.available && scanner.reason && <p className="text-xs">{scanner.reason}</p>}
              {!scanner.available && scanner.step && (
                <p className="break-words text-amber-700 text-xs dark:text-amber-400">
                  {swt("scan.nextStep", { p0: scanner.step })}
                </p>
              )}
              <Label htmlFor={`scan-path-${scanner.key}`} className="font-normal text-muted-foreground text-sm">
                {swt("scan.pathLabel", { p0: scanner.key })}
              </Label>
              <Input
                id={`scan-path-${scanner.key}`}
                autoComplete="off"
                className="font-mono text-sm"
                placeholder={swt("scan.pathPlaceholder")}
                value={paths[scanner.key] ?? ""}
                disabled={saving}
                onChange={(e) => setPaths((current) => ({ ...current, [scanner.key]: e.target.value }))}
              />
            </div>
          );
        })}
        <div className="flex flex-wrap items-center justify-between gap-3">
          <p className="min-w-0 text-muted-foreground text-xs">{swt("scan.pathHint")}</p>
          <Button type="button" disabled={saving || !dirty} onClick={() => save({ paths: changedPaths })}>
            {swt("scan.pathSave")}
          </Button>
        </div>
      </CardContent>
    </Card>
  );
}
