"use client";
import * as React from "react";

import Link from "next/link";
import { useSearchParams } from "next/navigation";

import { ArrowLeftIcon, ArrowUpRightIcon, ShieldAlertIcon } from "lucide-react";
import { toast } from "sonner";

import { CopyButton } from "@/components/copy-button";
import { ErrorState } from "@/components/error-state";
import { FindingRetestPanel } from "@/components/finding-retest-panel";
import { FindingTrafficPanel } from "@/components/finding-traffic-panel";
import { Markdown } from "@/components/markdown";
import { StatusBadge } from "@/components/status-badge";
import { Alert, AlertDescription } from "@/components/ui/alert";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card";
import { Select, SelectContent, SelectGroup, SelectItem, SelectTrigger } from "@/components/ui/select";
import { Separator } from "@/components/ui/separator";
import { Tabs, TabsContent, TabsList, TabsTrigger } from "@/components/ui/tabs";
import { useI18n } from "@/i18n";
import { getIntlLocale, translate as swt } from "@/i18n/runtime";
import { api } from "@/lib/api";
import { isNumericId } from "@/lib/path-segment";
import { statusMeta } from "@/lib/status";
import type { Finding, FindingStatus, Severity } from "@/lib/types";

import { JiraFindingControl, useJiraLinks } from "../../tasks/detail/_tabs/jira-export";
import { FindingLineageView } from "./lineage";

const SEVERITIES: Severity[] = ["critical", "high", "medium", "low"];
const FINDING_STATUSES: FindingStatus[] = [
  "pending",
  "in_progress",
  "confirmed",
  "resolved",
  "fixed",
  "false_positive",
  "ignored",
  "duplicate",
  "risk_accepted",
];

function fmtTime(ts: string) {
  return new Date(ts).toLocaleString(getIntlLocale());
}

// FieldRow is one label/value line in the right-hand status panel.
function FieldRow({ label, children }: { label: string; children: React.ReactNode }) {
  "use no memo";
  const { t: swt, locale: swLocale } = useI18n();

  return (
    <div className="flex items-start justify-between gap-3 py-2.5">
      <span className="shrink-0 pt-0.5 text-xs text-muted-foreground">{label}</span>
      <div className="flex min-w-0 flex-col items-end gap-1 text-right text-sm">{children}</div>
    </div>
  );
}

// The API client does not expose the HTTP status on thrown errors, so a missing finding is recognised by
// the backend's 404 message or by the status-based fallback message ("GET /path: 404").
function isNotFoundError(e: Error) {
  return /^finding not found$/i.test(e.message.trim()) || /:\s*404$/.test(e.message.trim());
}

function FindingDetailInner() {
  "use no memo";
  const { t: swt, locale: swLocale } = useI18n();

  const searchParams = useSearchParams();
  const id = searchParams.get("id") ?? "";
  const contextTaskId = searchParams.get("context_task") ?? "";
  // Everything loaded is tagged with the route it belongs to; data for any other route is treated as absent,
  // so navigating between findings shows the loading state at once and a late response can never be displayed.
  const routeKey = `${id}|${contextTaskId}`;
  const [data, setData] = React.useState<{ key: string; finding: Finding } | null>(null);
  // Outcome of the last request for a route. error is set only for failures other than "no such finding"
  // (network, server error).
  const [outcome, setOutcome] = React.useState<{ key: string; error: string | null } | null>(null);
  const finding = data?.key === routeKey ? data.finding : null;
  const loaded = outcome?.key === routeKey;
  const loadError = outcome?.key === routeKey ? outcome.error : null;
  const dataRef = React.useRef(data);
  dataRef.current = data;
  const loadSeq = React.useRef(0);
  const [tab, setTab] = React.useState("overview");
  const jira = useJiraLinks(finding?.task_id ? String(finding.task_id) : "");

  const load = React.useCallback(() => {
    const key = `${id}|${contextTaskId}`;
    const seq = ++loadSeq.current;
    if (!id || !isNumericId(id)) {
      setOutcome({ key, error: null });
      return;
    }
    api
      .getFinding(id, contextTaskId || undefined)
      .then((f) => {
        if (seq !== loadSeq.current) return;
        setData({ key, finding: f });
        setOutcome({ key, error: null });
      })
      .catch((e: Error) => {
        if (seq !== loadSeq.current) return;
        if (dataRef.current?.key === key) {
          // A failed refresh keeps the finding already on screen.
          toast.error(e.message);
          return;
        }
        setData(null);
        setOutcome({ key, error: isNotFoundError(e) ? null : e.message });
      });
  }, [contextTaskId, id]);
  React.useEffect(() => {
    load();
  }, [load]);
  // Callbacks from child panels may fire after navigation; they always reload the current route.
  const loadRef = React.useRef(load);
  loadRef.current = load;

  // Applies a change only while the finding of that route is still the one displayed.
  const patchFinding = React.useCallback((key: string, update: (cur: Finding) => Finding) => {
    setData((cur) => (cur && cur.key === key ? { key, finding: update(cur.finding) } : cur));
  }, []);

  const changeSeverity = React.useCallback(
    async (next: Severity) => {
      if (!finding || finding.inherited || next === finding.severity) return;
      const prev = finding.severity;
      patchFinding(routeKey, (cur) => ({ ...cur, severity: next }));
      try {
        const updated = await api.setFindingSeverity(id, next);
        patchFinding(routeKey, () => updated);
        toast.success(swt("interface.m0351", { p0: statusMeta("severity", next).label }));
      } catch (e) {
        patchFinding(routeKey, (cur) => ({ ...cur, severity: prev }));
        toast.error(swt("interface.m0352") + (e as Error).message);
      }
    },
    [swLocale, finding, id, patchFinding, routeKey],
  );

  const changeStatus = React.useCallback(
    async (next: FindingStatus) => {
      if (!finding || finding.inherited || next === finding.status) return;
      const prev = finding.status;
      patchFinding(routeKey, (cur) => ({ ...cur, status: next }));
      try {
        const updated = await api.setFindingStatus(id, next);
        patchFinding(routeKey, () => updated);
        toast.success(swt("interface.m0353", { p0: statusMeta("finding", next).label }));
      } catch (e) {
        patchFinding(routeKey, (cur) => ({ ...cur, status: prev }));
        toast.error(swt("interface.m0352") + (e as Error).message);
      }
    },
    [swLocale, finding, id, patchFinding, routeKey],
  );

  if (!finding && loaded && loadError !== null) {
    return (
      <div className="flex flex-1 items-center justify-center">
        <ErrorState
          title={swt("states.findingLoadFailed")}
          detail={loadError}
          onRetry={() => {
            setOutcome(null);
            load();
          }}
          actions={
            <Button asChild size="sm" variant="ghost">
              <Link href="/function/findings">
                <ArrowLeftIcon /> {swt("interface.m0355")}
              </Link>
            </Button>
          }
        />
      </div>
    );
  }

  if (!finding) {
    return (
      <div className="flex flex-1 flex-col items-center justify-center gap-3 p-10 text-center">
        <p className="text-muted-foreground">{loaded ? swt("interface.m0354", { p0: id }) : swt("interface.m0260")}</p>
        {loaded && (
          <Button asChild variant="outline">
            <Link href="/function/findings">
              <ArrowLeftIcon /> {swt("interface.m0355")}
            </Link>
          </Button>
        )}
      </div>
    );
  }

  const title = finding.name || finding.vulnclass || swt("interface.m0326");

  return (
    <Tabs value={tab} onValueChange={setTab} className="flex min-h-0 min-w-0 flex-1 flex-col gap-0">
      {/* Sticky header */}
      <header className="sticky top-(--header-height) z-10 flex flex-col gap-2 border-b bg-card px-4 py-3 lg:px-6">
        <div className="flex flex-wrap items-center gap-2">
          <Button asChild variant="ghost" size="icon" className="-ml-1 size-7">
            <Link href="/function/findings">
              <ArrowLeftIcon />
            </Link>
          </Button>
          <ShieldAlertIcon className="size-4 text-muted-foreground" />
          <h1 className="max-w-md truncate text-sm font-semibold" title={title}>
            {title}
          </h1>
          <code className="rounded bg-muted px-1.5 py-0.5 font-mono text-xs text-muted-foreground">#{finding.id}</code>
          <Separator orientation="vertical" className="mx-1 h-4" />
          <StatusBadge domain="severity" value={finding.severity} dot />
          <StatusBadge domain="finding" value={finding.status} dot />
          {finding.inherited && finding.source_task_id && (
            <Badge variant="outline">
              {swt("interface.m0356")}
              {finding.source_task_id} {swt("interface.m0357")}
            </Badge>
          )}
          {finding.task_id && !finding.inherited && (
            <div className="ml-auto">
              <JiraFindingControl findingId={Number(finding.id)} links={jira.links} onExport={jira.exportFinding} />
            </div>
          )}
        </div>
        <TabsList>
          <TabsTrigger value="overview">{swt("interface.m0358")}</TabsTrigger>
          <TabsTrigger value="lineage">{swt("interface.m0359")}</TabsTrigger>
        </TabsList>
      </header>

      {/* Tab content */}
      <div className="flex-1 p-4 lg:p-6">
        {/* Overview: summary/evidence on the left, status on the right. */}
        <TabsContent value="overview" className="mt-0">
          <div className="grid gap-4 lg:grid-cols-3">
            {/* Left column. */}
            <div className="flex flex-col gap-4 lg:col-span-2">
              <Card>
                <CardHeader>
                  <CardTitle className="text-sm">{swt("interface.m0360")}</CardTitle>
                </CardHeader>
                <CardContent>
                  <p className="text-sm leading-relaxed whitespace-pre-wrap">
                    {finding.summary || swt("interface.m0361")}
                  </p>
                </CardContent>
              </Card>
              <FindingRetestPanel
                key={`retest-${id}`}
                findingId={id}
                readOnly={finding.inherited}
                onCompleted={() => loadRef.current()}
              />
              <Card>
                <CardHeader>
                  <CardTitle className="text-sm">{swt("interface.m0362")}</CardTitle>
                </CardHeader>
                <CardContent>
                  {finding.evidence ? (
                    <pre className="max-h-[46vh] overflow-auto rounded-md bg-muted px-3 py-2 font-mono text-xs whitespace-pre-wrap">
                      {finding.evidence}
                    </pre>
                  ) : (
                    <p className="text-sm text-muted-foreground">{swt("interface.m0363")}</p>
                  )}
                </CardContent>
              </Card>
              <FindingTrafficPanel
                key={`traffic-${id}`}
                findingId={id}
                contextTask={contextTaskId || undefined}
                readOnly={finding.inherited}
                onChanged={load}
              />
              {/* Markdown report below evidence. */}
              <Card>
                <CardHeader className="flex-row items-center justify-between">
                  <CardTitle className="text-sm">{swt("interface.m0344")}</CardTitle>
                  {finding.report && <CopyButton text={finding.report} successMessage={swt("interface.m0345")} />}
                </CardHeader>
                <CardContent>
                  {finding.report_stale ? (
                    <Alert>
                      <AlertDescription>{swt("interface.m0364")}</AlertDescription>
                    </Alert>
                  ) : null}
                  {finding.report ? (
                    <Markdown text={finding.report} />
                  ) : (
                    <p className="text-sm text-muted-foreground">{swt("interface.m0347")}</p>
                  )}
                </CardContent>
              </Card>
            </div>

            {/* Right status column. */}
            <Card className="h-fit lg:sticky lg:top-24">
              <CardHeader>
                <CardTitle className="text-sm">{swt("interface.m0191")}</CardTitle>
              </CardHeader>
              <CardContent className="divide-y">
                {/* Vulnerability ID. */}
                <FieldRow label={swt("interface.m0365")}>
                  <code className="rounded bg-muted px-1.5 py-0.5 font-mono text-xs text-muted-foreground">
                    #{finding.id}
                  </code>
                </FieldRow>

                {/* Severity. */}
                <FieldRow label={swt("interface.m0340")}>
                  {finding.inherited ? (
                    <StatusBadge domain="severity" value={finding.severity} dot />
                  ) : (
                    <Select value={finding.severity} onValueChange={(v) => changeSeverity(v as Severity)}>
                      <SelectTrigger size="sm" className="h-7 w-auto border-none px-1 shadow-none focus-visible:ring-0">
                        <StatusBadge domain="severity" value={finding.severity} dot />
                      </SelectTrigger>
                      <SelectContent position="popper" align="end">
                        <SelectGroup>
                          {SEVERITIES.map((sv) => (
                            <SelectItem key={sv} value={sv}>
                              {statusMeta("severity", sv).label}
                            </SelectItem>
                          ))}
                        </SelectGroup>
                      </SelectContent>
                    </Select>
                  )}
                </FieldRow>

                {/* Resolution status. */}
                <FieldRow label={swt("interface.m0366")}>
                  {finding.inherited ? (
                    <StatusBadge domain="finding" value={finding.status} dot />
                  ) : (
                    <Select value={finding.status} onValueChange={(v) => changeStatus(v as FindingStatus)}>
                      <SelectTrigger size="sm" className="h-7 w-auto border-none px-1 shadow-none focus-visible:ring-0">
                        <StatusBadge domain="finding" value={finding.status} dot />
                      </SelectTrigger>
                      <SelectContent position="popper" align="end">
                        <SelectGroup>
                          {FINDING_STATUSES.map((st) => (
                            <SelectItem key={st} value={st}>
                              {statusMeta("finding", st).label}
                            </SelectItem>
                          ))}
                        </SelectGroup>
                      </SelectContent>
                    </Select>
                  )}
                </FieldRow>

                {/* Vulnerability type. */}
                <FieldRow label={swt("interface.m0367")}>
                  {finding.vulnclass ? (
                    <code className="rounded bg-muted px-1.5 py-0.5 font-mono text-xs">{finding.vulnclass}</code>
                  ) : (
                    <span className="text-muted-foreground">—</span>
                  )}
                </FieldRow>

                {/* Affected assets. */}
                <FieldRow label={swt("interface.m0368")}>
                  {finding.assets && finding.assets.length > 0 ? (
                    <div className="flex flex-wrap justify-end gap-1">
                      {finding.assets.map((a) => (
                        <code
                          key={a.id}
                          className="max-w-[16rem] truncate rounded bg-muted px-1.5 py-0.5 font-mono text-xs"
                          title={`${a.type} · ${a.label}`}
                        >
                          {a.label}
                        </code>
                      ))}
                    </div>
                  ) : (
                    <span className="text-muted-foreground">—</span>
                  )}
                </FieldRow>

                {/* Parent task. */}
                <FieldRow label={swt("interface.m0323")}>
                  {finding.task_id ? (
                    <Link
                      href={`/function/tasks/detail?id=${finding.task_id}`}
                      className="inline-flex max-w-[16rem] items-center gap-1 text-primary hover:underline"
                      title={finding.task_description}
                    >
                      <span className="truncate">{finding.task_description || `#${finding.task_id}`}</span>
                      <ArrowUpRightIcon className="size-3 shrink-0" />
                    </Link>
                  ) : (
                    <span className="text-muted-foreground">{swt("interface.m0369")}</span>
                  )}
                </FieldRow>

                {/* Discovery time. */}
                <FieldRow label={swt("interface.m0370")}>
                  <span className="tabular-nums">{fmtTime(finding.ts)}</span>
                </FieldRow>
              </CardContent>
            </Card>
          </div>
        </TabsContent>

        {/* Lineage from the task's initial node to this vulnerability. */}
        <TabsContent value="lineage" className="mt-0">
          <FindingLineageView findingId={finding.id} />
        </TabsContent>
      </div>
    </Tabs>
  );
}

// useSearchParams must sit under a Suspense boundary for static export.
export default function FindingDetailPage() {
  "use no memo";
  const { t: swt, locale: swLocale } = useI18n();

  return (
    <React.Suspense fallback={null}>
      <FindingDetailInner />
    </React.Suspense>
  );
}
