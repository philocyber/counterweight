"use client";

import * as React from "react";

import Link from "next/link";

import {
  CircleCheckIcon,
  ExternalLinkIcon,
  GitPullRequestArrowIcon,
  RefreshCwIcon,
  ScanSearchIcon,
  WrenchIcon,
} from "lucide-react";
import { toast } from "sonner";

import { GitHubTokenCard } from "@/components/github-token-card";
import { Markdown } from "@/components/markdown";
import { StatusBadge } from "@/components/status-badge";
import { TaskAutomationSwitch, useTaskAutomation } from "@/components/task-automation-switch";
import { Alert, AlertDescription } from "@/components/ui/alert";
import {
  AlertDialog,
  AlertDialogAction,
  AlertDialogCancel,
  AlertDialogContent,
  AlertDialogDescription,
  AlertDialogFooter,
  AlertDialogHeader,
  AlertDialogTitle,
  AlertDialogTrigger,
} from "@/components/ui/alert-dialog";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/ui/card";
import { Empty, EmptyDescription, EmptyHeader, EmptyTitle } from "@/components/ui/empty";
import { Separator } from "@/components/ui/separator";
import { Skeleton } from "@/components/ui/skeleton";
import { useI18n } from "@/i18n";
import { translate as swt } from "@/i18n/runtime";
import { api } from "@/lib/api";
import { toneClasses } from "@/lib/status";
import type {
  FindingMitigation,
  FindingRetest,
  MitigationFinding,
  MitigationState,
  TaskMitigations,
} from "@/lib/types";

const stateLabels: Record<MitigationState, () => string> = {
  drafting: () => swt("interface.mitigationStateDrafting"),
  in_review: () => swt("interface.mitigationStateInReview"),
  ready: () => swt("interface.mitigationStateReady"),
  pr_open: () => swt("interface.mitigationStatePrOpen"),
  merged: () => swt("interface.mitigationStateMerged"),
  pr_closed: () => swt("interface.mitigationStatePrClosed"),
  blocked: () => swt("interface.mitigationStateBlocked"),
  cannot_fix: () => swt("interface.mitigationStateCannotFix"),
  failed: () => swt("interface.mitigationStateFailed"),
  review_failed: () => swt("interface.mitigationStateReviewFailed"),
  changes_needed: () => swt("interface.mitigationStateChangesNeeded"),
  rejected: () => swt("interface.mitigationStateRejected"),
};

const activeStates = new Set<MitigationState>(["drafting", "in_review"]);
const publishedStates = new Set<MitigationState>(["pr_open", "merged"]);

function stateVariant(state: MitigationState): "default" | "secondary" | "destructive" | "outline" {
  if (state === "ready" || state === "merged") return "default";
  if (state === "blocked" || state === "failed" || state === "rejected" || state === "review_failed") {
    return "destructive";
  }
  return activeStates.has(state) ? "outline" : "secondary";
}

function findingLabel(finding: MitigationFinding) {
  return finding.name?.trim() || finding.vulnclass.trim() || swt("interface.m0326");
}

// A merged fix whose post-merge re-check found the vulnerability gone.
function validatedAfterMerge(finding: MitigationFinding) {
  return finding.mitigations.some(
    (m) => m.state === "merged" && m.recheck?.status === "completed" && m.recheck.verdict === "rejected",
  );
}

// Any finding can be fixed; an unverified one asks first (see the warning dialog).
function canFix(finding: MitigationFinding) {
  const latest = finding.mitigations[0]?.state;
  return !(latest && (activeStates.has(latest) || publishedStates.has(latest)));
}

function verified(finding: MitigationFinding) {
  return finding.verification === "confirmed" && finding.status === "confirmed";
}

// What a post-merge re-check concluded, in the operator's terms.
function recheckLabel(r: FindingRetest): {
  text: string;
  variant: "default" | "secondary" | "destructive" | "outline";
} {
  if (r.status === "pending" || r.status === "running")
    return { text: swt("interface.recheckRunning"), variant: "outline" };
  if (r.status !== "completed") return { text: swt("interface.recheckFailed"), variant: "destructive" };
  if (r.verdict === "rejected") return { text: swt("interface.recheckFixed"), variant: "default" };
  if (r.verdict === "confirmed") return { text: swt("interface.recheckStillPresent"), variant: "destructive" };
  return { text: swt("interface.recheckInconclusive"), variant: "secondary" };
}

function DiffView({ diff }: { diff: string }) {
  return (
    <pre className="max-h-[28rem] overflow-auto rounded-md border bg-muted/30 p-3 font-mono text-xs leading-5">
      {diff.split("\n").map((line, i) => {
        const tone =
          line.startsWith("+") && !line.startsWith("+++")
            ? "text-emerald-700 dark:text-emerald-400"
            : line.startsWith("-") && !line.startsWith("---")
              ? "text-red-700 dark:text-red-400"
              : line.startsWith("@@") || line.startsWith("diff --git")
                ? "text-muted-foreground"
                : "";
        // biome-ignore lint/suspicious/noArrayIndexKey: diff lines are static and ordered.
        return (
          <div key={i} className={`whitespace-pre ${tone}`}>
            {line || " "}
          </div>
        );
      })}
    </pre>
  );
}

function AttemptCard({
  attempt,
  repository,
  githubReady,
  onChanged,
}: {
  attempt: FindingMitigation;
  repository: string;
  githubReady: boolean;
  onChanged: () => void;
}) {
  "use no memo";
  const { t: swt } = useI18n();
  const [busy, setBusy] = React.useState(false);
  const run = attempt.run;

  const publish = async () => {
    setBusy(true);
    try {
      const r = await api.publishMitigation(run.id);
      toast.success(swt("interface.mitigationPublished"), {
        action: { label: swt("interface.mitigationViewPR"), onClick: () => window.open(r.pr_url, "_blank") },
      });
      onChanged();
    } catch (e) {
      toast.error((e as Error).message);
    } finally {
      setBusy(false);
    }
  };

  const recheck = async () => {
    setBusy(true);
    try {
      await api.recheckMitigation(run.id);
      toast.success(swt("interface.recheckStarted"));
      onChanged();
    } catch (e) {
      toast.error((e as Error).message);
    } finally {
      setBusy(false);
    }
  };
  const recheckActive = attempt.recheck?.status === "pending" || attempt.recheck?.status === "running";

  const refreshPR = async () => {
    setBusy(true);
    try {
      await api.refreshMitigation(run.id);
      onChanged();
    } catch (e) {
      toast.error((e as Error).message);
    } finally {
      setBusy(false);
    }
  };

  return (
    <Card>
      <CardHeader className="gap-2">
        <div className="flex flex-wrap items-center gap-2">
          <Badge variant={stateVariant(attempt.state)}>{stateLabels[attempt.state]()}</Badge>
          <span className="text-muted-foreground text-xs">{swt("interface.mitigationAttempt", { p0: run.id })}</span>
          {attempt.branch ? (
            <span className="font-mono text-muted-foreground text-xs">
              {swt("interface.mitigationBranch", { p0: attempt.branch })}
            </span>
          ) : null}
          <div className="ml-auto flex flex-wrap gap-2">
            {run.conversation_id != null ? (
              <Button asChild variant="ghost" size="sm">
                <Link href={`/chat?c=${run.conversation_id}`}>{swt("interface.mitigationViewSession")}</Link>
              </Button>
            ) : null}
            {attempt.pr_url ? (
              <Button asChild variant="outline" size="sm">
                <a href={attempt.pr_url} target="_blank" rel="noreferrer">
                  {swt("interface.mitigationViewPR")}
                  <ExternalLinkIcon data-icon="inline-end" />
                </a>
              </Button>
            ) : null}
            {attempt.state === "pr_open" ? (
              <Button variant="ghost" size="sm" disabled={busy} onClick={() => void refreshPR()}>
                <RefreshCwIcon data-icon="inline-start" />
                {swt("interface.mitigationRefreshPR")}
              </Button>
            ) : null}
            {attempt.state === "ready" ? (
              <AlertDialog>
                <AlertDialogTrigger asChild>
                  <Button size="sm" disabled={busy || !githubReady}>
                    <GitPullRequestArrowIcon data-icon="inline-start" />
                    {swt("interface.mitigationOpenPR")}
                  </Button>
                </AlertDialogTrigger>
                <AlertDialogContent>
                  <AlertDialogHeader>
                    <AlertDialogTitle>{swt("interface.mitigationOpenPR")}</AlertDialogTitle>
                    <AlertDialogDescription>
                      {swt("interface.mitigationPublishConfirm", { p0: repository })}
                    </AlertDialogDescription>
                  </AlertDialogHeader>
                  <AlertDialogFooter>
                    <AlertDialogCancel>{swt("interface.m0063")}</AlertDialogCancel>
                    <AlertDialogAction onClick={() => void publish()}>
                      {swt("interface.mitigationOpenPR")}
                    </AlertDialogAction>
                  </AlertDialogFooter>
                </AlertDialogContent>
              </AlertDialog>
            ) : null}
            {attempt.state === "merged" && !recheckActive ? (
              <Button
                size="sm"
                variant={attempt.recheck ? "outline" : "default"}
                disabled={busy}
                onClick={() => void recheck()}
              >
                <ScanSearchIcon data-icon="inline-start" />
                {attempt.recheck ? swt("interface.recheckAgain") : swt("interface.recheckStart")}
              </Button>
            ) : null}
          </div>
        </div>
        {attempt.blocked ? (
          <Alert variant="destructive">
            <AlertDescription>{swt("interface.mitigationBlocked", { p0: attempt.blocked })}</AlertDescription>
          </Alert>
        ) : null}
        {run.error ? <p className="text-destructive text-sm">{run.error}</p> : null}
      </CardHeader>
      <CardContent className="flex flex-col gap-4">
        {run.summary ? (
          <div className="flex flex-col gap-1">
            <h3 className="font-medium text-sm">{swt("interface.mitigationFix")}</h3>
            <p className="text-sm">{run.summary}</p>
            {run.evidence ? <Markdown text={run.evidence} /> : null}
          </div>
        ) : null}
        {attempt.diff ? (
          <div className="flex flex-col gap-1">
            <h3 className="font-medium text-sm">{swt("interface.mitigationDiff")}</h3>
            <DiffView diff={attempt.diff} />
          </div>
        ) : null}
        {attempt.review ? (
          <div className="flex flex-col gap-1">
            <div className="flex flex-wrap items-center gap-2">
              <h3 className="font-medium text-sm">{swt("interface.mitigationReview")}</h3>
              {attempt.review.verdict ? <Badge variant="outline">{attempt.review.verdict}</Badge> : null}
              {attempt.review.conversation_id != null ? (
                <Button asChild variant="ghost" size="sm">
                  <Link href={`/chat?c=${attempt.review.conversation_id}`}>
                    {swt("interface.mitigationViewSession")}
                  </Link>
                </Button>
              ) : null}
            </div>
            {attempt.review.summary ? <p className="text-sm">{attempt.review.summary}</p> : null}
            {attempt.review.evidence ? <Markdown text={attempt.review.evidence} /> : null}
            {attempt.review.error ? <p className="text-destructive text-sm">{attempt.review.error}</p> : null}
          </div>
        ) : null}
        {attempt.state === "merged" ? (
          <div className="flex flex-col gap-2 rounded-lg border p-3">
            <div className="flex flex-wrap items-center gap-2">
              <h3 className="font-medium text-sm">{swt("interface.recheckTitle")}</h3>
              {attempt.recheck ? (
                <Badge variant={recheckLabel(attempt.recheck).variant}>{recheckLabel(attempt.recheck).text}</Badge>
              ) : null}
              <div className="ml-auto flex flex-wrap gap-2">
                {attempt.recheck?.conversation_id != null ? (
                  <Button asChild variant="ghost" size="sm">
                    <Link href={`/chat?c=${attempt.recheck.conversation_id}`}>
                      {swt("interface.mitigationViewSession")}
                    </Link>
                  </Button>
                ) : null}
              </div>
            </div>
            {attempt.recheck ? null : <p className="text-muted-foreground text-sm">{swt("interface.recheckHint")}</p>}
            {attempt.recheck?.summary ? <p className="text-sm">{attempt.recheck.summary}</p> : null}
            {attempt.recheck?.evidence ? <Markdown text={attempt.recheck.evidence} /> : null}
            {attempt.recheck?.error ? <p className="text-destructive text-sm">{attempt.recheck.error}</p> : null}
          </div>
        ) : null}
      </CardContent>
    </Card>
  );
}

export function MitigationTab({ taskId }: { taskId: string }) {
  "use no memo";
  const { t: swt } = useI18n();
  const [data, setData] = React.useState<TaskMitigations | null>(null);
  const [selectedId, setSelectedId] = React.useState("");
  const [error, setError] = React.useState("");
  const [starting, setStarting] = React.useState(false);
  const [warnFor, setWarnFor] = React.useState<MitigationFinding | null>(null);
  const { automation, update } = useTaskAutomation(taskId);
  const [version, setVersion] = React.useState(0);
  const refresh = React.useCallback(() => setVersion((v) => v + 1), []);

  React.useEffect(() => {
    let disposed = false;
    let timer: ReturnType<typeof setTimeout>;
    async function load() {
      try {
        const result = await api.taskMitigations(taskId);
        if (disposed) return;
        setData(result);
        setSelectedId((current) =>
          result.findings.some((f) => f.id === current) ? current : (result.findings[0]?.id ?? ""),
        );
        setError("");
      } catch (e) {
        if (!disposed) setError((e as Error).message);
      } finally {
        if (!disposed) timer = setTimeout(() => void load(), 3000);
      }
    }
    void version;
    void load();
    return () => {
      disposed = true;
      clearTimeout(timer);
    };
  }, [taskId, version]);

  const findings = data?.findings ?? [];
  const selected = findings.find((f) => f.id === selectedId);
  const github = data?.github;
  const githubReady = Boolean(github?.configured && !github.expired);

  const verifyFirst = async (finding: MitigationFinding) => {
    try {
      await api.verifyFinding(finding.id);
      toast.success(swt("interface.verifyFindingStarted"));
    } catch (e) {
      toast.error((e as Error).message);
    }
  };

  const start = async (finding: MitigationFinding, unverifiedOk = false) => {
    if (!unverifiedOk && !verified(finding)) {
      setWarnFor(finding);
      return;
    }
    setStarting(true);
    try {
      await api.startMitigation(finding.id, unverifiedOk);
      toast.success(swt("interface.mitigationStarted"));
      refresh();
    } catch (e) {
      toast.error((e as Error).message);
    } finally {
      setStarting(false);
    }
  };

  if (data && !data.code_review) {
    return (
      <Empty>
        <EmptyHeader>
          <EmptyTitle>{swt("interface.mitigationTab")}</EmptyTitle>
          <EmptyDescription>{swt("interface.mitigationNotCodeReview")}</EmptyDescription>
        </EmptyHeader>
      </Empty>
    );
  }

  return (
    <div className="flex flex-col gap-4">
      {error ? (
        <Alert variant="destructive">
          <AlertDescription>{error}</AlertDescription>
        </Alert>
      ) : null}
      {automation?.code_review ? (
        <TaskAutomationSwitch
          id="task-automation-mitigate"
          checked={automation.mitigate}
          label={swt("interface.automationMitigateLabel")}
          description={swt("interface.automationMitigateDescription")}
          onChange={(mitigate) => void update({ mitigate })}
        />
      ) : null}
      <AlertDialog open={warnFor !== null} onOpenChange={(open) => (open ? null : setWarnFor(null))}>
        <AlertDialogContent>
          <AlertDialogHeader>
            <AlertDialogTitle>{swt("interface.unverifiedFixTitle")}</AlertDialogTitle>
            <AlertDialogDescription>
              {warnFor?.verification === "rejected"
                ? swt("interface.unverifiedFixRejected")
                : swt("interface.unverifiedFixDescription")}
            </AlertDialogDescription>
          </AlertDialogHeader>
          <AlertDialogFooter>
            <AlertDialogCancel>{swt("interface.m0063")}</AlertDialogCancel>
            <Button
              variant="outline"
              onClick={() => {
                const finding = warnFor;
                setWarnFor(null);
                if (finding) void start(finding, true);
              }}
            >
              {swt("interface.unverifiedFixAnyway")}
            </Button>
            <AlertDialogAction
              onClick={() => {
                if (warnFor) void verifyFirst(warnFor);
              }}
            >
              {swt("interface.unverifiedFixVerifyFirst")}
            </AlertDialogAction>
          </AlertDialogFooter>
        </AlertDialogContent>
      </AlertDialog>
      <div className="grid items-start gap-4 lg:grid-cols-[minmax(16rem,22rem)_minmax(0,1fr)]">
        <Card className="min-w-0">
          <CardHeader>
            <CardTitle>
              {swt("interface.mitigationListTitle")} {data ? ` · ${findings.length}` : ""}
            </CardTitle>
            <CardDescription>{swt("interface.mitigationListDescription")}</CardDescription>
          </CardHeader>
          <CardContent className="flex max-h-[32rem] flex-col overflow-y-auto">
            {!data && !error ? <Skeleton className="h-24 w-full" /> : null}
            {findings.map((finding, index) => {
              const latest = finding.mitigations[0];
              return (
                <React.Fragment key={finding.id}>
                  {index > 0 ? <Separator className="my-1" /> : null}
                  <Button
                    variant={finding.id === selectedId ? "secondary" : "ghost"}
                    className="h-auto w-full shrink-0 flex-col items-start gap-2 whitespace-normal py-3 text-left"
                    aria-pressed={finding.id === selectedId}
                    onClick={() => setSelectedId(finding.id)}
                  >
                    <span className="line-clamp-2 break-words">{findingLabel(finding)}</span>
                    <span className="flex flex-wrap items-center gap-2">
                      <StatusBadge domain="severity" value={finding.severity} dot />
                      <Badge variant={finding.verification === "confirmed" ? "secondary" : "outline"}>
                        {finding.verification === "confirmed"
                          ? swt("interface.mitigationVerified")
                          : swt("interface.mitigationNotVerified")}
                      </Badge>
                      {latest ? (
                        <Badge variant={stateVariant(latest.state)}>{stateLabels[latest.state]()}</Badge>
                      ) : null}
                      {validatedAfterMerge(finding) ? (
                        <Badge variant="outline" className={toneClasses.green}>
                          <CircleCheckIcon data-icon="inline-start" />
                          {swt("interface.mitigationValidated")}
                        </Badge>
                      ) : null}
                    </span>
                  </Button>
                </React.Fragment>
              );
            })}
            {data && findings.length === 0 ? (
              <Empty>
                <EmptyHeader>
                  <EmptyTitle>{swt("interface.mitigationNoFindings")}</EmptyTitle>
                </EmptyHeader>
              </Empty>
            ) : null}
          </CardContent>
        </Card>
        {selected ? (
          <div className="flex min-w-0 flex-col gap-4">
            <div className="flex flex-wrap items-start justify-between gap-2">
              <h2 className="min-w-0 flex-1 break-words font-medium">{findingLabel(selected)}</h2>
              {verified(selected) ? null : (
                <Button variant="outline" onClick={() => void verifyFirst(selected)}>
                  <ScanSearchIcon data-icon="inline-start" />
                  {swt("interface.verifyFindingStart")}
                </Button>
              )}
              <Button disabled={starting || !canFix(selected)} onClick={() => void start(selected)}>
                <WrenchIcon data-icon="inline-start" />
                {selected.mitigations.length
                  ? swt("interface.mitigationRegenerate")
                  : swt("interface.mitigationGenerate")}
              </Button>
            </div>
            {selected.verification !== "confirmed" ? (
              <p className="text-muted-foreground text-sm">{swt("interface.mitigationNotConfirmed")}</p>
            ) : null}
            {!githubReady && selected.mitigations.some((m) => m.state === "ready") ? (
              <>
                <p className="text-muted-foreground text-sm">{swt("interface.githubTokenMissing")}</p>
                <GitHubTokenCard onChange={refresh} />
              </>
            ) : null}
            {selected.mitigations.length === 0 ? (
              <p className="text-muted-foreground text-sm">{swt("interface.mitigationNoAttempts")}</p>
            ) : null}
            {selected.mitigations.map((attempt) => (
              <AttemptCard
                key={attempt.run.id}
                attempt={attempt}
                repository={data?.repository ?? ""}
                githubReady={githubReady}
                onChanged={refresh}
              />
            ))}
          </div>
        ) : null}
      </div>
    </div>
  );
}
