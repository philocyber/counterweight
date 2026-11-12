"use client";

import * as React from "react";

import { CheckCircle2Icon, DownloadIcon, SearchIcon, TriangleAlertIcon } from "lucide-react";
import { toast } from "sonner";

import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/ui/card";
import { Progress } from "@/components/ui/progress";
import { api } from "@/lib/api";
import { TicketEventSource } from "@/lib/event-stream";
import type { ReconToolProgress, ReconToolStatus } from "@/lib/types";

// Companion to System → Settings' update card, at smaller scale: installs one
// of four optional ProjectDiscovery recon binaries (subfinder/httpx/naabu/dnsx)
// that the recon-planning skill treats as "use if present, fall back to Bash
// otherwise". Counterweight never requires these — this card only removes the manual
// "find a release, verify it, put it on PATH" step for an operator who wants them.

type InstallState = {
  busy: boolean;
  progress: ReconToolProgress | null;
};

export function ReconToolsCard() {
  "use no memo";
  const [tools, setTools] = React.useState<ReconToolStatus[] | null>(null);
  const [loading, setLoading] = React.useState(true);
  const [installs, setInstalls] = React.useState<Record<string, InstallState>>({});
  const streams = React.useRef<Record<string, TicketEventSource>>({});

  const reload = React.useCallback(() => {
    setLoading(true);
    api
      .listReconTools()
      .then((r) => setTools(r.tools))
      .catch(() => setTools(null))
      .finally(() => setLoading(false));
  }, []);

  React.useEffect(() => {
    reload();
  }, [reload]);

  React.useEffect(() => {
    const open = streams.current;
    return () => {
      for (const es of Object.values(open)) es.close();
    };
  }, []);

  const openStream = React.useCallback(
    (key: string) => {
      streams.current[key]?.close();
      const es = new TicketEventSource(`/api/recontools/stream?tool=${encodeURIComponent(key)}`);
      streams.current[key] = es;
      es.onmessage = (ev) => {
        let p: ReconToolProgress;
        try {
          p = JSON.parse(ev.data) as ReconToolProgress;
        } catch {
          return;
        }
        setInstalls((m) => ({ ...m, [key]: { busy: p.phase !== "done" && p.phase !== "failed", progress: p } }));
        if (p.phase === "done") {
          es.close();
          toast.success(`${key} installed${p.version ? ` (${p.version})` : ""}`);
          reload();
        } else if (p.phase === "failed") {
          es.close();
          toast.error(`${key} install failed: ${p.error || p.message}`);
        }
      };
      es.onerror = () => es.close();
    },
    [reload],
  );

  const install = (key: string) => {
    setInstalls((m) => ({ ...m, [key]: { busy: true, progress: null } }));
    openStream(key);
    api.installReconTool(key).catch((e) => {
      streams.current[key]?.close();
      setInstalls((m) => ({ ...m, [key]: { busy: false, progress: null } }));
      toast.error(`Could not start install: ${(e as Error).message}`);
    });
  };

  return (
    <Card className="mb-4 break-inside-avoid md:mb-6">
      <CardHeader>
        <CardTitle className="flex items-center gap-2 text-base">
          <SearchIcon className="size-4" />
          Recon tools
        </CardTitle>
        <CardDescription>
          Optional ProjectDiscovery CLIs (subfinder, httpx, naabu, dnsx) the recon-planning skill uses when present and
          falls back to Bash without. Installing one here also registers it as a usable tool below, bound to no agent by
          default — bind it from the System tab like any other tool.
        </CardDescription>
      </CardHeader>
      <CardContent className="space-y-3">
        {loading && <p className="text-muted-foreground text-xs">Checking installed tools…</p>}
        {!loading && !tools && (
          <p className="flex items-start gap-2 rounded-md border border-destructive/40 bg-destructive/10 p-2 text-xs text-destructive">
            <TriangleAlertIcon className="mt-0.5 size-3.5 shrink-0" />
            Could not read recon tool status.
          </p>
        )}
        {tools?.map((t) => {
          const state = installs[t.key];
          const busy = state?.busy ?? false;
          const phase = state?.progress?.phase;
          const downloading = busy && phase === "downloading";
          const pct = downloading ? Math.max(state?.progress?.percent ?? 0, 0) : 100;
          return (
            <div key={t.key} className="rounded-md border p-3">
              <div className="flex flex-wrap items-center gap-2">
                <span className="font-mono text-sm font-medium">{t.key}</span>
                {t.installed ? (
                  <Badge variant="secondary" className="gap-1">
                    <CheckCircle2Icon className="size-3" />
                    {t.managed ? "Installed" : "Found on PATH"}
                    {t.version ? ` · ${t.version}` : ""}
                  </Badge>
                ) : (
                  <Badge variant="outline">Not installed</Badge>
                )}
                <a
                  href={`https://github.com/${t.repo}`}
                  target="_blank"
                  rel="noreferrer"
                  className="text-muted-foreground text-xs underline-offset-4 hover:underline"
                >
                  {t.repo}
                </a>
                <div className="ml-auto">
                  <Button
                    size="sm"
                    variant={t.installed ? "outline" : "default"}
                    onClick={() => install(t.key)}
                    disabled={busy}
                  >
                    <DownloadIcon className="size-4" />
                    {t.installed ? "Reinstall" : "Install"}
                  </Button>
                </div>
              </div>
              <p className="text-muted-foreground mt-1.5 text-xs">{t.description}</p>
              {busy && (
                <div className="mt-2 space-y-1">
                  <Progress value={pct} className={downloading ? undefined : "animate-pulse"} />
                  <p className="text-muted-foreground text-xs">{state?.progress?.message ?? "Preparing…"}</p>
                </div>
              )}
              {!busy && phase === "failed" && (
                <p className="mt-2 flex items-start gap-2 rounded-md border border-destructive/40 bg-destructive/10 p-2 text-xs text-destructive">
                  <TriangleAlertIcon className="mt-0.5 size-3.5 shrink-0" />
                  {state?.progress?.error || state?.progress?.message}
                </p>
              )}
            </div>
          );
        })}
      </CardContent>
    </Card>
  );
}
