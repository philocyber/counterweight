"use client";

import * as React from "react";

import Link from "next/link";

import { CheckIcon, ChevronRightIcon, ExternalLinkIcon, InfoIcon, ScanSearchIcon } from "lucide-react";

import { EmptyState } from "@/components/console/empty-state";
import { ErrorState } from "@/components/error-state";
import { Alert, AlertDescription } from "@/components/ui/alert";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/ui/card";
import { usePolling } from "@/hooks/use-polling";
import { type MessageKey, type TFunction, useI18n } from "@/i18n";
import { api, isNotAvailable } from "@/lib/api";
import type { ScanLead, ScannerStatus, ScanRun, Task, TaskScan } from "@/lib/types";
import { cn } from "@/lib/utils";

const PAGE = 50;

const STATE_LABELS: Record<string, { label: MessageKey; hint: MessageKey }> = {
  new: { label: "scan.stateNew", hint: "scan.stateNewHint" },
  delivered: { label: "scan.stateDelivered", hint: "scan.stateDeliveredHint" },
  confirmed: { label: "scan.stateConfirmed", hint: "scan.stateConfirmedHint" },
  dismissed: { label: "scan.stateDismissed", hint: "scan.stateDismissedHint" },
};

const RUN_LABELS: Record<string, MessageKey> = {
  running: "scan.runRunning",
  ok: "scan.runOk",
  unavailable: "scan.runUnavailable",
  failed: "scan.runFailed",
};

function scannerPurpose(scanner: ScannerStatus, t: TFunction): string {
  if (scanner.key === "osv-scanner") return t("scan.osvPurpose");
  if (scanner.key === "gitleaks") return t("scan.gitleaksPurpose");
  return scanner.purpose;
}

// A lead's state in words, with what happened to it. Leads are deliberately low key: they are pointers, not findings.
function LeadState({ lead }: { lead: ScanLead }) {
  "use no memo";
  const { t: swt, locale: swLocale } = useI18n();
  const meta = STATE_LABELS[lead.state] ?? STATE_LABELS.new;
  return (
    <div className="flex min-w-0 flex-col items-start gap-1 sm:items-end">
      <Badge variant="outline" title={swt(meta.hint)}>
        {lead.state === "confirmed" && <CheckIcon aria-hidden="true" />}
        {swt(meta.label)}
      </Badge>
      {lead.state === "confirmed" && lead.finding_id ? (
        <Link
          href={`/function/findings/detail?id=${lead.finding_id}`}
          className="font-medium text-primary text-xs hover:underline"
        >
          {swt("scan.viewFinding")}
        </Link>
      ) : null}
      {lead.state === "confirmed" && lead.resolved_by === "existing_finding" && (
        <span className="text-muted-foreground text-xs">{swt("scan.resolvedExisting")}</span>
      )}
      {lead.state === "confirmed" && lead.resolved_by === "finding_match" && (
        <span className="text-muted-foreground text-xs">{swt("scan.resolvedMatch")}</span>
      )}
      {lead.state === "dismissed" && (
        <span className="break-words text-muted-foreground text-xs sm:text-right">
          {lead.resolution ? swt("scan.dismissedReason", { p0: lead.resolution }) : swt("scan.dismissedNoReason")}
        </span>
      )}
    </div>
  );
}

function ScannerRow({ scanner, run }: { scanner: ScannerStatus; run?: ScanRun }) {
  "use no memo";
  const { t: swt, locale: swLocale } = useI18n();
  const problem = run && (run.status === "failed" || run.status === "unavailable") ? run.reason : undefined;
  return (
    <div className="grid gap-1 border-t pt-3 first:border-t-0 first:pt-0">
      <div className="flex flex-wrap items-center gap-2">
        <span className="font-medium text-sm">{scanner.key}</span>
        <span className="text-muted-foreground text-xs">{scannerPurpose(scanner, swt)}</span>
        <Badge variant={scanner.available ? "outline" : "secondary"}>
          {scanner.available ? swt("scan.availableLabel") : swt("scan.unavailableLabel")}
        </Badge>
        <Badge variant="outline">{run ? swt(RUN_LABELS[run.status] ?? "scan.runOk") : swt("scan.runNone")}</Badge>
      </div>
      {run && (
        <p className="text-muted-foreground text-xs">
          {swt("scan.runCounts", { p0: run.leads_found, p1: run.leads_stored })}
          {run.tool_version ? ` · ${swt("scan.runVersion", { p0: run.tool_version })}` : ""}
        </p>
      )}
      {(problem || (!scanner.available && scanner.reason)) && (
        <p className="break-words text-xs">{problem || scanner.reason}</p>
      )}
      {!scanner.available && scanner.step && (
        <p className="break-words text-amber-700 text-xs dark:text-amber-400">
          {swt("scan.nextStep", { p0: scanner.step })}
        </p>
      )}
    </div>
  );
}

function SecretsCard({ leads }: { leads: ScanLead[] }) {
  "use no memo";
  const { t: swt, locale: swLocale } = useI18n();
  const [shown, setShown] = React.useState(PAGE);
  return (
    <Card>
      <CardHeader>
        <CardTitle className="text-base">{swt("scan.secretsTitle")}</CardTitle>
        <CardDescription>{swt("scan.secretsHint")}</CardDescription>
      </CardHeader>
      <CardContent>
        {leads.length === 0 ? (
          <p className="text-muted-foreground text-sm">{swt("scan.secretsEmpty")}</p>
        ) : (
          <ul className="divide-y">
            {leads.slice(0, shown).map((lead) => (
              <li key={lead.id} className="grid gap-2 py-2 sm:grid-cols-[minmax(0,1fr)_auto] sm:items-start">
                <div className="min-w-0">
                  <p className="break-all font-mono text-sm" title={lead.file}>
                    {lead.file}
                    {lead.line ? <span className="text-muted-foreground">:{lead.line}</span> : null}
                  </p>
                  <p className="break-words text-muted-foreground text-xs">
                    {swt("scan.colRule")}: {lead.rule_id}
                    {lead.title && lead.title !== lead.rule_id ? ` · ${lead.title}` : ""}
                  </p>
                </div>
                <LeadState lead={lead} />
              </li>
            ))}
          </ul>
        )}
        {leads.length > shown && (
          <Button type="button" variant="outline" size="sm" className="mt-3" onClick={() => setShown(shown + PAGE)}>
            {swt("scan.showMore", { p0: leads.length - shown })}
          </Button>
        )}
      </CardContent>
    </Card>
  );
}

interface AdvisoryGroup {
  key: string;
  package: string;
  version: string;
  file?: string;
  leads: ScanLead[];
}

function groupAdvisories(leads: ScanLead[]): AdvisoryGroup[] {
  const groups = new Map<string, AdvisoryGroup>();
  for (const lead of leads) {
    const key = `${lead.package ?? ""}\u0000${lead.version ?? ""}`;
    const group = groups.get(key) ?? {
      key,
      package: lead.package || lead.rule_id,
      version: lead.version ?? "",
      file: lead.file,
      leads: [],
    };
    group.leads.push(lead);
    groups.set(key, group);
  }
  return [...groups.values()];
}

function AdvisoryRow({ group }: { group: AdvisoryGroup }) {
  "use no memo";
  const { t: swt, locale: swLocale } = useI18n();
  const [open, setOpen] = React.useState(false);
  const fixed = [...new Set(group.leads.map((lead) => lead.fixed_version).filter((v): v is string => !!v))];
  // Advisories of one package are delivered and resolved together, so the first lead speaks for the group.
  const lead = group.leads.find((l) => l.state === "confirmed" || l.state === "dismissed") ?? group.leads[0];
  return (
    <li className="grid gap-2 py-2 sm:grid-cols-[minmax(0,1fr)_auto] sm:items-start">
      <div className="min-w-0">
        <p className="break-all font-mono text-sm">
          {swt("scan.packageVersion", { p0: group.package, p1: group.version })}
        </p>
        <p className="text-muted-foreground text-xs">
          {group.leads.length === 1
            ? swt("scan.advisoryCountOne")
            : swt("scan.advisoryCount", { p0: group.leads.length })}
          {" · "}
          {fixed.length > 0 ? swt("scan.fixedIn", { p0: fixed.join(", ") }) : swt("scan.noFixedVersion")}
        </p>
        {group.file && (
          <p className="break-all text-muted-foreground text-xs">{swt("scan.lockfile", { p0: group.file })}</p>
        )}
        <button
          type="button"
          className="mt-1 inline-flex items-center gap-1 font-medium text-primary text-xs hover:underline"
          aria-expanded={open}
          onClick={() => setOpen((v) => !v)}
        >
          <ChevronRightIcon className={cn("size-3 transition-transform", open && "rotate-90")} aria-hidden="true" />
          {open ? swt("scan.hideAdvisories") : swt("scan.showAdvisories")}
        </button>
        {open && (
          <ul className="mt-2 flex flex-col gap-1.5">
            {group.leads.map((l) => (
              <li key={l.id} className="min-w-0 text-xs">
                <a
                  href={`https://osv.dev/vulnerability/${encodeURIComponent(l.rule_id)}`}
                  target="_blank"
                  rel="noopener noreferrer"
                  className="inline-flex items-center gap-1 font-mono font-medium text-primary hover:underline"
                >
                  {l.rule_id}
                  <ExternalLinkIcon className="size-3" aria-hidden="true" />
                </a>
                {l.aliases ? <span className="text-muted-foreground"> ({l.aliases})</span> : null}
                {l.title ? <span className="break-words text-muted-foreground"> {l.title}</span> : null}
              </li>
            ))}
          </ul>
        )}
      </div>
      <LeadState lead={lead} />
    </li>
  );
}

function AdvisoriesCard({ leads }: { leads: ScanLead[] }) {
  "use no memo";
  const { t: swt, locale: swLocale } = useI18n();
  const [shown, setShown] = React.useState(PAGE);
  const groups = React.useMemo(() => groupAdvisories(leads), [leads]);
  return (
    <Card>
      <CardHeader>
        <CardTitle className="text-base">{swt("scan.advisoriesTitle")}</CardTitle>
        <CardDescription>{swt("scan.advisoriesHint")}</CardDescription>
      </CardHeader>
      <CardContent>
        {groups.length === 0 ? (
          <p className="text-muted-foreground text-sm">{swt("scan.advisoriesEmpty")}</p>
        ) : (
          <ul className="divide-y">
            {groups.slice(0, shown).map((group) => (
              <AdvisoryRow key={group.key} group={group} />
            ))}
          </ul>
        )}
        {groups.length > shown && (
          <Button type="button" variant="outline" size="sm" className="mt-3" onClick={() => setShown(shown + PAGE)}>
            {swt("scan.showMore", { p0: groups.length - shown })}
          </Button>
        )}
      </CardContent>
    </Card>
  );
}

// Leads from the supplementary scanners of a code review. They are pointers for the review agents, never findings.
export function ScanTab({ taskId, task }: { taskId: string; task: Task }) {
  "use no memo";
  const { t: swt, locale: swLocale } = useI18n();
  const [data, setData] = React.useState<TaskScan | null>(null);
  const [unavailable, setUnavailable] = React.useState(false);
  const [error, setError] = React.useState<string | null>(null);

  const load = React.useCallback(
    () =>
      api
        .taskScan(taskId)
        .then((scan) => {
          setData(scan);
          setUnavailable(false);
          setError(null);
        })
        .catch((e: Error) => {
          if (isNotAvailable(e)) setUnavailable(true);
          else setError(e.message);
        }),
    [taskId],
  );

  // Poll while a scanner runs or the task is still active; the endpoint also reconciles leads with later findings.
  const taskActive = !["done", "failed", "timeout"].includes(task.status);
  const running = !!data?.runs?.some((run) => run.status === "running");
  usePolling(load, 10_000, [load], !unavailable && (data === null || running || taskActive));

  if (unavailable) return <EmptyState icon={ScanSearchIcon} title={swt("scan.notAvailable")} />;
  if (error !== null && !data) {
    return (
      <Card className="py-0">
        <ErrorState title={swt("scan.loadFailed")} detail={error} onRetry={() => void load()} />
      </Card>
    );
  }
  if (!data) return null;
  if (!data.applicable) return <EmptyState icon={ScanSearchIcon} title={swt("scan.notApplicable")} />;

  const runs = data.runs ?? [];
  const leads = data.leads ?? [];
  const secrets = leads.filter((lead) => lead.kind === "secret");
  const advisories = leads.filter((lead) => lead.kind === "advisory");
  const firstStep = (data.scanners ?? []).find((scanner) => !scanner.available && scanner.step)?.step;
  const pending = data.counts?.new ?? 0;

  return (
    <div className="flex flex-col gap-4">
      <Alert>
        <InfoIcon />
        <AlertDescription>{swt("scan.notice")}</AlertDescription>
      </Alert>
      {!data.enabled && <p className="text-muted-foreground text-sm">{swt("scan.disabledNote")}</p>}

      {runs.length === 0 && leads.length === 0 ? (
        <Card className="py-0">
          <EmptyState
            icon={ScanSearchIcon}
            title={swt("scan.emptyTitle")}
            hint={
              <>
                {swt("scan.emptyHint")}
                {firstStep && (
                  <span className="mt-1 block break-words">{swt("scan.emptyHintInstall", { p0: firstStep })}</span>
                )}
                <Link
                  href="/system/settings/#integrations"
                  className="mt-1 block font-medium text-primary hover:underline"
                >
                  {swt("scan.openSettings")}
                </Link>
              </>
            }
          />
        </Card>
      ) : (
        <>
          <Card>
            <CardHeader>
              <CardTitle className="text-base">{swt("scan.scannersTitle")}</CardTitle>
              <CardDescription>
                {data.note}
                {pending > 0 && ` ${swt("scan.leadsPending", { p0: pending })}`}
              </CardDescription>
            </CardHeader>
            <CardContent className="flex flex-col gap-3">
              {(data.scanners ?? []).map((scanner) => (
                <ScannerRow key={scanner.key} scanner={scanner} run={runs.find((run) => run.scanner === scanner.key)} />
              ))}
            </CardContent>
          </Card>
          <SecretsCard leads={secrets} />
          <AdvisoriesCard leads={advisories} />
        </>
      )}
    </div>
  );
}
