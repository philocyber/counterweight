"use client";

import * as React from "react";

import {
  CircleCheckIcon,
  CircleHelpIcon,
  CircleXIcon,
  type LucideIcon,
  PlayIcon,
  TriangleAlertIcon,
} from "lucide-react";

import { ErrorState } from "@/components/error-state";
import { Alert, AlertDescription, AlertTitle } from "@/components/ui/alert";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/ui/card";
import { Skeleton } from "@/components/ui/skeleton";
import { Spinner } from "@/components/ui/spinner";
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/components/ui/table";
import { type I18n, type MessageKey, type TFunction, useI18n } from "@/i18n";
import { api } from "@/lib/api";
import { type AuthzMatrixColumn, authzMatrix, failedChecks } from "@/lib/authz";
import { type Tone, toneClasses } from "@/lib/status";
import type { AuthzCheckResult, AuthzReport, AuthzRun, AuthzRunSummary } from "@/lib/types";
import { cn } from "@/lib/utils";

// A result is always shown as an icon and a word, never by colour alone. A run that proves nothing
// (inconclusive or error) is a warning; only a violated policy is red.
const RESULT_META: Record<string, { label: MessageKey; tone: Tone; icon: LucideIcon; explain: MessageKey }> = {
  pass: { label: "authz.resultPass", tone: "green", icon: CircleCheckIcon, explain: "authz.explainPass" },
  fail: { label: "authz.resultFail", tone: "red", icon: CircleXIcon, explain: "authz.explainFail" },
  inconclusive: {
    label: "authz.resultInconclusive",
    tone: "amber",
    icon: CircleHelpIcon,
    explain: "authz.explainInconclusive",
  },
  error: { label: "authz.resultError", tone: "amber", icon: TriangleAlertIcon, explain: "authz.explainError" },
};

const KIND_LABELS: Record<string, MessageKey> = {
  access_granted: "authz.kindAccessGranted",
  data_exposed: "authz.kindDataExposed",
  access_denied: "authz.kindAccessDenied",
  content_missing: "authz.kindContentMissing",
  unexpected_redirect: "authz.kindUnexpectedRedirect",
  unexpected_status: "authz.kindUnexpectedStatus",
  server_error: "authz.kindServerError",
  request_failed: "authz.kindRequestFailed",
  no_baseline: "authz.kindNoBaseline",
  unproven_marker: "authz.kindUnprovenMarker",
  session_lost: "authz.kindSessionLost",
  truncated: "authz.kindTruncated",
};

// Past runs listed before the rest is asked for.
const RUNS_SHOWN = 5;

function ResultBadge({ result }: { result: string }) {
  "use no memo";
  const { t: swt } = useI18n();
  const meta = RESULT_META[result];
  if (!meta) return <Badge variant="outline">{result}</Badge>;
  const Icon = meta.icon;
  return (
    <span
      className={cn(
        "inline-flex items-center gap-1 whitespace-nowrap rounded-md border px-2 py-0.5 font-medium text-xs",
        toneClasses[meta.tone],
      )}
    >
      <Icon className="size-3" aria-hidden="true" />
      {swt(meta.label)}
    </span>
  );
}

function duration(ms: number, swt: TFunction, fmt: I18n["fmt"]): string {
  if (ms < 1000) return swt("authz.durationMs", { p0: fmt.number(ms) });
  return swt("authz.durationSeconds", { p0: fmt.number(ms / 1000, { maximumFractionDigits: 1 }) });
}

// What the policy expects from a check, for example "deny 401, 403".
function expectation(check: AuthzCheckResult, swt: TFunction): string {
  const statuses = (check.expected_status ?? []).join(", ");
  if (check.expect === "allow") return swt("authz.expectAllow", { p0: statuses });
  if (check.expect === "deny") return swt("authz.expectDeny", { p0: statuses });
  return `${check.expect} ${statuses}`.trim();
}

function RoleHeading({ column }: { column: AuthzMatrixColumn }) {
  "use no memo";
  const { t: swt } = useI18n();
  return (
    <TableHead className="h-auto py-2 align-bottom">
      <span className="block">{column.role}</span>
      {column.role !== column.key && (
        <span className="block font-mono font-normal text-[11px] text-muted-foreground">{column.key}</span>
      )}
      {column.authenticated && (
        <span className="flex items-center gap-1 font-normal text-[11px] text-muted-foreground">
          {column.identityVerified ? (
            <CircleCheckIcon className="size-3" aria-hidden="true" />
          ) : (
            <TriangleAlertIcon className="size-3 text-amber-600 dark:text-amber-400" aria-hidden="true" />
          )}
          {column.identityVerified ? swt("authz.identityVerified") : swt("authz.identityNotVerified")}
        </span>
      )}
    </TableHead>
  );
}

function MatrixCell({ check }: { check: AuthzCheckResult | null }) {
  "use no memo";
  const { t: swt } = useI18n();
  if (!check) return <span className="text-muted-foreground text-xs">{swt("authz.notSent")}</span>;
  return (
    <div className="flex flex-col items-start gap-1">
      <span className="font-mono text-xs">{expectation(check, swt)}</span>
      <ResultBadge result={check.result} />
      {check.result !== "pass" && (
        <span className="text-muted-foreground text-xs">
          {check.status ? swt("authz.gotStatus", { p0: check.status }) : swt("authz.noStatus")}
        </span>
      )}
    </div>
  );
}

// Cases by roles. The Table component scrolls horizontally inside its own container.
function Matrix({ report }: { report: AuthzReport }) {
  "use no memo";
  const { t: swt } = useI18n();
  const { columns, rows } = React.useMemo(() => authzMatrix(report), [report]);
  if (rows.length === 0) return <p className="text-muted-foreground text-sm">{swt("authz.matrixEmpty")}</p>;
  return (
    <div className="min-w-0 overflow-hidden rounded-lg border">
      <Table>
        <TableHeader>
          <TableRow>
            <TableHead className="h-auto py-2 align-bottom">{swt("authz.colCase")}</TableHead>
            {columns.map((column) => (
              <RoleHeading key={column.key} column={column} />
            ))}
          </TableRow>
        </TableHeader>
        <TableBody>
          {rows.map((row) => (
            <TableRow key={row.id}>
              <TableCell className="align-top">
                <span className="block font-medium font-mono text-xs">{row.id}</span>
                <span
                  className="block max-w-72 truncate font-mono text-muted-foreground text-xs"
                  title={`${row.method} ${row.path}`}
                >
                  {row.method} {row.path}
                </span>
                {row.write && (
                  <Badge variant="outline" className="mt-1">
                    {swt("authz.writeBadge")}
                  </Badge>
                )}
              </TableCell>
              {row.cells.map((check, index) => (
                <TableCell key={columns[index].key} className="align-top">
                  <MatrixCell check={check} />
                </TableCell>
              ))}
            </TableRow>
          ))}
        </TableBody>
      </Table>
    </div>
  );
}

// Every check that did not pass, with what the server says about it. The detail and the response
// excerpt come from the server and the target; they are shown as plain text.
function Problems({ report }: { report: AuthzReport }) {
  "use no memo";
  const { t: swt } = useI18n();
  const problems = React.useMemo(() => failedChecks(report), [report]);
  if (problems.length === 0) return <p className="text-muted-foreground text-sm">{swt("authz.problemsEmpty")}</p>;
  return (
    <ul className="divide-y rounded-lg border">
      {problems.map(({ caseId, method, path, persona, role, check }) => (
        <li key={`${caseId}\u0000${persona}`} className="flex min-w-0 flex-col gap-1.5 px-3 py-3">
          <div className="flex min-w-0 flex-wrap items-center gap-2">
            <ResultBadge result={check.result} />
            {check.kind && (
              <Badge variant="outline" title={check.kind}>
                {KIND_LABELS[check.kind] ? swt(KIND_LABELS[check.kind]) : check.kind}
              </Badge>
            )}
            <span className="break-all font-medium font-mono text-sm">{caseId}</span>
            <span className="break-all font-mono text-muted-foreground text-xs">
              {method} {path}
            </span>
          </div>
          <dl className="flex flex-wrap gap-x-4 gap-y-0.5 text-xs">
            <div className="flex min-w-0 gap-1">
              <dt className="text-muted-foreground">{swt("authz.problemRole")}:</dt>
              <dd className="break-words">
                {role}
                {role !== persona && <span className="font-mono text-muted-foreground"> ({persona})</span>}
              </dd>
            </div>
            <div className="flex gap-1">
              <dt className="text-muted-foreground">{swt("authz.problemExpected")}:</dt>
              <dd>{expectation(check, swt)}</dd>
            </div>
            <div className="flex gap-1">
              <dt className="text-muted-foreground">{swt("authz.problemReceived")}:</dt>
              <dd>{check.status || swt("authz.noStatus")}</dd>
            </div>
          </dl>
          {check.detail && <p className="break-words text-sm">{check.detail}</p>}
          {check.excerpt && (
            <details className="min-w-0 text-xs">
              <summary className="w-fit cursor-pointer font-medium text-primary hover:underline">
                {swt("authz.showExcerpt")}
              </summary>
              <p className="mt-1 text-muted-foreground">{swt("authz.excerptNote")}</p>
              <pre className="mt-1 max-h-60 overflow-auto whitespace-pre-wrap break-words rounded-lg bg-muted/60 p-3 font-mono text-xs [overflow-wrap:anywhere]">
                {check.excerpt}
              </pre>
            </details>
          )}
        </li>
      ))}
    </ul>
  );
}

function RunDetail({ run }: { run: AuthzRun }) {
  "use no memo";
  const { t: swt, fmt } = useI18n();
  const report = run.report;
  const counts = report.counts;
  const errors = report.errors ?? [];
  const meta = RESULT_META[report.result];
  return (
    <div className="flex min-w-0 flex-col gap-3">
      <div className="flex flex-col gap-1">
        <div className="flex flex-wrap items-center gap-2">
          <h3 className="font-medium text-sm">
            {swt("authz.runSummaryTitle", { p0: fmt.dateTime(report.started_at) })}
          </h3>
          <ResultBadge result={report.result} />
        </div>
        <p className="text-sm">
          {swt("authz.summary", {
            p0: counts.cases,
            p1: counts.checks,
            p2: counts.passed,
            p3: counts.failed,
            p4: counts.inconclusive,
          })}
          {meta ? ` ${swt(meta.explain)}` : ""}
        </p>
        <p className="break-all text-muted-foreground text-xs">
          {[
            report.target ? swt("authz.runTarget", { p0: report.target }) : "",
            swt("authz.runTook", { p0: duration(report.duration_ms, swt, fmt) }),
          ]
            .filter(Boolean)
            .join(" · ")}
        </p>
      </div>
      {errors.length > 0 && (
        <Alert variant="destructive">
          <TriangleAlertIcon />
          <AlertTitle>{swt("authz.runErrorsTitle")}</AlertTitle>
          <AlertDescription>
            <ul className="list-disc pl-4">
              {errors.map((message, index) => (
                // biome-ignore lint/suspicious/noArrayIndexKey: server sentences are not unique and never reorder
                <li key={index}>{message}</li>
              ))}
            </ul>
          </AlertDescription>
        </Alert>
      )}
      <h4 className="font-medium text-sm">{swt("authz.matrixTitle")}</h4>
      <Matrix report={report} />
      <h4 className="font-medium text-sm">{swt("authz.problemsTitle")}</h4>
      <Problems report={report} />
    </div>
  );
}

// The Run button, the past runs and the report of one run (the latest unless another is chosen).
export function AuthzResults({
  taskId,
  runs,
  running,
  starting,
  blockedReason,
  onRun,
}: {
  taskId: string;
  runs: AuthzRunSummary[];
  running: boolean;
  starting: boolean;
  // Why a run cannot start now, or null when it can.
  blockedReason: string | null;
  onRun: () => void;
}) {
  "use no memo";
  const { t: swt, fmt } = useI18n();
  const [selectedId, setSelectedId] = React.useState<number | null>(null);
  const [detail, setDetail] = React.useState<AuthzRun | null>(null);
  const [detailError, setDetailError] = React.useState<string | null>(null);
  const [attempt, setAttempt] = React.useState(0);
  const [allRuns, setAllRuns] = React.useState(false);

  const shownId = runs.some((run) => run.id === selectedId) ? selectedId : (runs[0]?.id ?? null);

  React.useEffect(() => {
    if (shownId === null) return;
    let cancelled = false;
    void attempt; // Retry after a failed load.
    setDetailError(null);
    api
      .authzRun(taskId, shownId)
      .then((run) => {
        if (!cancelled) setDetail(run);
      })
      .catch((e: Error) => {
        if (!cancelled) setDetailError(e.message);
      });
    return () => {
      cancelled = true;
    };
  }, [taskId, shownId, attempt]);

  const shown = detail && detail.id === shownId ? detail : null;
  let detailView: React.ReactNode = null;
  if (shown) {
    detailView = <RunDetail run={shown} />;
  } else if (shownId !== null && detailError !== null) {
    detailView = (
      <ErrorState
        title={swt("authz.runLoadFailed")}
        detail={detailError}
        onRetry={() => setAttempt((value) => value + 1)}
      />
    );
  } else if (shownId !== null) {
    detailView = <Skeleton className="h-24 w-full" />;
  }

  return (
    <Card className="min-w-0">
      <CardHeader>
        <CardTitle className="text-base">{swt("authz.resultsTitle")}</CardTitle>
        <CardDescription>{swt("authz.resultsDescription")}</CardDescription>
      </CardHeader>
      <CardContent className="flex min-w-0 flex-col gap-4">
        <div className="flex flex-wrap items-center gap-3">
          <Button
            type="button"
            size="sm"
            disabled={running || starting || blockedReason !== null}
            onClick={() => {
              // The run that is about to start becomes the latest, which is the one shown by default.
              setSelectedId(null);
              onRun();
            }}
          >
            {running || starting ? <Spinner data-icon="inline-start" /> : <PlayIcon data-icon="inline-start" />}
            {running ? swt("authz.running") : swt("authz.run")}
          </Button>
          <p className="min-w-0 text-muted-foreground text-xs" aria-live="polite">
            {running ? swt("authz.runningNote") : blockedReason}
          </p>
        </div>

        <div className="flex min-w-0 flex-col gap-2">
          <h3 className="font-medium text-sm">{swt("authz.runsTitle")}</h3>
          {runs.length === 0 ? (
            <p className="text-muted-foreground text-sm">{swt("authz.runsEmpty")}</p>
          ) : (
            <>
              <div className="min-w-0 overflow-hidden rounded-lg border">
                <Table>
                  <TableHeader>
                    <TableRow>
                      <TableHead>{swt("authz.colStarted")}</TableHead>
                      <TableHead>{swt("authz.colResult")}</TableHead>
                      <TableHead>{swt("authz.colChecks")}</TableHead>
                      <TableHead>{swt("authz.colTarget")}</TableHead>
                      <TableHead>{swt("authz.colDuration")}</TableHead>
                      <TableHead>
                        <span className="sr-only">{swt("authz.colActions")}</span>
                      </TableHead>
                    </TableRow>
                  </TableHeader>
                  <TableBody>
                    {(allRuns ? runs : runs.slice(0, RUNS_SHOWN)).map((run) => {
                      const started = fmt.dateTime(run.started_at);
                      const isShown = run.id === shownId;
                      return (
                        <TableRow key={run.id} data-state={isShown ? "selected" : undefined}>
                          <TableCell>{started}</TableCell>
                          <TableCell>
                            <ResultBadge result={run.result} />
                          </TableCell>
                          <TableCell>
                            {swt("authz.runCounts", {
                              p0: run.counts.passed,
                              p1: run.counts.failed,
                              p2: run.counts.inconclusive,
                            })}
                          </TableCell>
                          <TableCell className="max-w-64 truncate" title={run.target}>
                            {run.target || "-"}
                          </TableCell>
                          <TableCell>{duration(run.duration_ms, swt, fmt)}</TableCell>
                          <TableCell className="text-right">
                            <Button
                              type="button"
                              size="xs"
                              variant={isShown ? "secondary" : "outline"}
                              aria-pressed={isShown}
                              aria-label={swt("authz.viewRunAria", { p0: started })}
                              onClick={() => setSelectedId(run.id)}
                            >
                              {isShown ? swt("authz.shownRun") : swt("authz.viewRun")}
                            </Button>
                          </TableCell>
                        </TableRow>
                      );
                    })}
                  </TableBody>
                </Table>
              </div>
              {!allRuns && runs.length > RUNS_SHOWN && (
                <div>
                  <Button type="button" size="sm" variant="outline" onClick={() => setAllRuns(true)}>
                    {swt("authz.showAllRuns", { p0: runs.length })}
                  </Button>
                </div>
              )}
            </>
          )}
        </div>

        {detailView}
      </CardContent>
    </Card>
  );
}
