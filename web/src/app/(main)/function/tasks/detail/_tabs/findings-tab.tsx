"use client";
import * as React from "react";

import Link from "next/link";

import { ArrowDownIcon, ArrowUpIcon, ArrowUpRightIcon, ChevronRightIcon } from "lucide-react";
import { toast } from "sonner";

import { StatusBadge } from "@/components/status-badge";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card";
import { Select, SelectContent, SelectGroup, SelectItem, SelectTrigger } from "@/components/ui/select";
import { Tooltip, TooltipContent, TooltipTrigger } from "@/components/ui/tooltip";
import { pollWhileVisible } from "@/hooks/use-polling";
import { useI18n } from "@/i18n";
import { getIntlLocale, translate as swt } from "@/i18n/runtime";
import { api } from "@/lib/api";
import { useStoredSortPreference } from "@/lib/sort-preference";
import { statusMeta } from "@/lib/status";
import type { Finding, FindingStatus, FindingsPage } from "@/lib/types";
import { cn } from "@/lib/utils";

import { JiraFindingControl, type JiraState } from "./jira-export";

type FindingSortField = "time";

const FINDING_SORT_FIELDS: readonly FindingSortField[] = ["time"];
const FINDING_SORT_PREFERENCE_KEY = "counterweight_task_findings_sort";

function findingLabel(finding: Finding): string {
  if (finding.name?.trim()) return finding.name;
  if (finding.vulnclass?.trim()) return finding.vulnclass;
  return swt("interface.m0326");
}

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

function Row({
  f,
  contextTaskId,
  onStatus,
  jira,
}: {
  f: Finding;
  contextTaskId: string;
  onStatus: (f: Finding, next: FindingStatus) => void;
  jira?: JiraState;
}) {
  "use no memo";
  const { t: swt, locale: swLocale } = useI18n();

  const [open, setOpen] = React.useState(false);
  return (
    <div className="border-b last:border-b-0">
      {/* Below xl the title keeps a full line and the status, date and actions wrap under it. */}
      <div className="flex w-full flex-wrap items-center gap-x-3 gap-y-2 px-4 py-3 text-sm hover:bg-accent/40">
        <button
          type="button"
          onClick={() => setOpen((o) => !o)}
          className="flex min-w-0 flex-1 basis-full items-center gap-3 text-left xl:basis-0"
        >
          <ChevronRightIcon
            className={cn("size-4 shrink-0 text-muted-foreground transition-transform", open && "rotate-90")}
          />
          <StatusBadge domain="severity" value={f.severity} dot />
          <div className="flex min-w-0 flex-1 flex-col gap-1">
            <div className="flex min-w-0 items-center gap-2">
              <span className="truncate font-medium">{findingLabel(f)}</span>
              {f.inherited && f.source_task_id && (
                <Badge variant="outline" className="shrink-0">
                  {swt("interface.m0570")}
                  {f.source_task_id} {swt("interface.m0357")}
                </Badge>
              )}
              {f.in_change === false && (
                <Tooltip>
                  <TooltipTrigger asChild>
                    <Badge variant="outline" className="shrink-0 text-muted-foreground">
                      {swt("prreview.outsideChange")}
                    </Badge>
                  </TooltipTrigger>
                  <TooltipContent>{swt("prreview.outsideChangeTip")}</TooltipContent>
                </Tooltip>
              )}
            </div>
            <span className="truncate text-xs text-muted-foreground">{f.summary}</span>
          </div>
        </button>
        <div className="flex min-w-0 flex-wrap items-center gap-x-3 gap-y-2 pl-7 xl:contents">
          <Badge variant="outline">
            {swt("interface.m0327")} {f.traffic_count ?? 0} {swt("interface.m0328")}
          </Badge>
          {f.assets && f.assets.length > 0 && (
            <div className="hidden shrink-0 flex-wrap justify-end gap-1 sm:flex">
              {f.assets.slice(0, 2).map((a) => (
                <code
                  key={a.id}
                  className="max-w-[10rem] truncate rounded bg-muted px-1.5 py-0.5 font-mono text-xs"
                  title={`${a.type} · ${a.label}`}
                >
                  {a.label}
                </code>
              ))}
              {f.assets.length > 2 && <span className="text-xs text-muted-foreground">+{f.assets.length - 2}</span>}
            </div>
          )}
          {f.finding_id && !f.inherited ? (
            <Select value={f.status} onValueChange={(v) => onStatus(f, v as FindingStatus)}>
              <SelectTrigger size="sm" className="h-7 w-28 shrink-0 border-none px-1 shadow-none focus-visible:ring-0">
                <StatusBadge domain="finding" value={f.status} dot />
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
          ) : (
            <StatusBadge domain="finding" value={f.status} dot />
          )}
          <span className="hidden shrink-0 text-xs text-muted-foreground md:block">
            {new Date(f.ts).toLocaleString(getIntlLocale())}
          </span>
          {jira && f.finding_id && !f.inherited && (
            <JiraFindingControl
              compact
              findingId={Number(f.finding_id)}
              links={jira.links}
              onExport={jira.exportFinding}
            />
          )}
          {f.finding_id && (
            <Link
              href={
                f.inherited
                  ? `/function/findings/detail?id=${f.finding_id}&context_task=${contextTaskId}`
                  : `/function/findings/detail?id=${f.finding_id}`
              }
              className="text-muted-foreground hover:text-primary inline-flex shrink-0 items-center gap-0.5 text-xs"
              title={swt("interface.m0591")}
            >
              {swt("interface.m0592")}
              <ArrowUpRightIcon className="size-3" />
            </Link>
          )}
        </div>
      </div>
      {open && (
        <div className="bg-muted/30 px-4 pb-4 pl-11">
          <div className="mb-1 text-xs font-medium text-muted-foreground">{swt("interface.m0362")}</div>
          <pre className="overflow-auto rounded-md border bg-background p-3 font-mono text-xs whitespace-pre-wrap">
            {f.evidence}
          </pre>
        </div>
      )}
    </div>
  );
}

function RegisteredFindings({ taskId, jira }: { taskId: string; jira: JiraState }) {
  const [page, setPage] = React.useState(1);
  const [data, setData] = React.useState<FindingsPage | null>(null);
  const [error, setError] = React.useState(false);
  const [revision, refresh] = React.useReducer((n: number) => n + 1, 0);
  React.useEffect(() => {
    let live = true;
    let timer: ReturnType<typeof setTimeout>;
    setData(null);
    async function load() {
      try {
        const result = await api.findingsPage({ task: taskId, page, pageSize: 25, sort: "severity" });
        if (live) {
          setData(result);
          setError(false);
        }
      } catch {
        if (live) setError(true);
      } finally {
        if (live) timer = setTimeout(load, 5000);
      }
    }
    void load();
    return () => {
      live = false;
      clearTimeout(timer);
    };
  }, [taskId, page, revision]);
  async function onStatus(finding: Finding, next: FindingStatus) {
    try {
      await api.setFindingStatus(finding.finding_id || finding.id, next);
      refresh();
    } catch (err) {
      toast.error(`Finding status could not be updated: ${(err as Error).message}`);
    }
  }
  return (
    <Card className="overflow-hidden py-0">
      <CardHeader className="gap-1 border-b py-4">
        <CardTitle className="flex items-center gap-2 text-sm">
          Registered findings <Badge variant="secondary">{data?.total ?? "—"}</Badge>
        </CardTitle>
        <p className="text-xs leading-relaxed text-muted-foreground">
          All review statuses for this task, including pending and standalone findings. This is the register used by
          task context and the report.
        </p>
      </CardHeader>
      <CardContent className="px-0">
        {error && (
          <div role="alert" className="flex items-center justify-between gap-3 p-4 text-sm">
            <span>Registered findings could not be refreshed.</span>
            <Button size="sm" variant="outline" onClick={refresh}>
              Retry
            </Button>
          </div>
        )}
        {!data && !error && (
          <p role="status" className="p-6 text-sm text-muted-foreground">
            Loading registered findings…
          </p>
        )}
        {data?.items.map((finding) => (
          <Row
            key={finding.id}
            f={{ ...finding, finding_id: finding.finding_id || finding.id }}
            contextTaskId={taskId}
            onStatus={onStatus}
            jira={jira}
          />
        ))}
        {data?.total === 0 && (
          <p className="p-6 text-sm text-muted-foreground">
            No findings registered for this task. Review Results ledger for recorded conclusions and incomplete
            attempts.
          </p>
        )}
        {data && data.total > 25 && (
          <div className="flex items-center justify-between border-t px-4 py-3 text-xs text-muted-foreground">
            <span>
              Page {page} of {Math.ceil(data.total / 25)}
            </span>
            <div className="flex gap-2">
              <Button size="sm" variant="outline" disabled={page === 1} onClick={() => setPage((n) => n - 1)}>
                Previous
              </Button>
              <Button
                size="sm"
                variant="outline"
                disabled={page * 25 >= data.total}
                onClick={() => setPage((n) => n + 1)}
              >
                Next
              </Button>
            </div>
          </div>
        )}
      </CardContent>
    </Card>
  );
}

export function FindingsTab({ taskId, jira }: { taskId: string; jira: JiraState }) {
  "use no memo";
  const { t: swt, locale: swLocale } = useI18n();

  const [findings, setFindings] = React.useState<Finding[]>([]);
  const [sortPreference, setSortPreference] = useStoredSortPreference(
    FINDING_SORT_PREFERENCE_KEY,
    FINDING_SORT_FIELDS,
    "time",
    "desc",
  );

  React.useEffect(() => {
    let active = true;
    const load = () => {
      api
        .findings(taskId)
        .then((fs) => {
          if (active) setFindings(fs);
        })
        .catch(() => {
          // Keep the last successful snapshot during transient refresh failures.
        });
    };
    load();
    const t = pollWhileVisible(load, 3000);
    return () => {
      active = false;
      t();
    };
  }, [taskId]);

  const onStatus = React.useCallback(
    async (f: Finding, next: FindingStatus) => {
      if (f.inherited || !f.finding_id || next === f.status) return;
      const prev = f.status;
      setFindings((cur) => cur.map((x) => (x.id === f.id ? { ...x, status: next } : x)));
      try {
        await api.setFindingStatus(f.finding_id, next);
        toast.success(swt("interface.m0375", { p0: statusMeta("finding", next).label }));
      } catch (e) {
        setFindings((cur) => cur.map((x) => (x.id === f.id ? { ...x, status: prev } : x)));
        toast.error(swt("interface.m0352") + (e as Error).message);
      }
    },
    [swLocale],
  );

  const items = findings
    .filter((f) => f.task_id === taskId || f.inherited)
    .sort((a, b) => {
      const compared = Date.parse(a.ts) - Date.parse(b.ts);
      if (compared !== 0) return sortPreference.direction === "asc" ? compared : -compared;
      return b.id.localeCompare(a.id, undefined, { numeric: true });
    });

  return (
    <div className="space-y-5">
      <RegisteredFindings key={taskId} taskId={taskId} jira={jira} />
      <section className="space-y-2">
        <h2 className="text-sm font-semibold">Exploration findings</h2>
        <p className="text-xs leading-relaxed text-muted-foreground">
          The original exploration view, including read-only findings inherited from source tasks.
        </p>
        <Card className="overflow-hidden py-0">
          <CardContent className="px-0">
            <div className="flex items-center border-b px-4 py-2 text-xs text-muted-foreground">
              <span className="min-w-0 flex-1">{swt("interface.m0526")}</span>
              <button
                type="button"
                className="inline-flex items-center gap-1 outline-none focus-visible:underline"
                aria-label={swt("interface.m0593", {
                  p0: sortPreference.direction === "asc" ? swt("interface.m0594") : swt("interface.m0595"),
                })}
                onClick={() =>
                  setSortPreference((current) => ({
                    field: "time",
                    direction: current.direction === "asc" ? "desc" : "asc",
                  }))
                }
              >
                <span>{swt("interface.m0370")}</span>
                {sortPreference.direction === "asc" ? (
                  <ArrowUpIcon className="size-3.5" />
                ) : (
                  <ArrowDownIcon className="size-3.5" />
                )}
              </button>
            </div>
            {items.map((f) => (
              <Row key={f.id} f={f} contextTaskId={taskId} onStatus={onStatus} />
            ))}
            {items.length === 0 && (
              <p className="px-4 py-8 text-center text-sm text-muted-foreground">{swt("interface.m0596")}</p>
            )}
          </CardContent>
        </Card>
      </section>
    </div>
  );
}
