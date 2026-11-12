"use client";

import * as React from "react";

import Link from "next/link";

import {
  AlertTriangleIcon,
  ArrowRightIcon,
  ChevronDownIcon,
  CircleCheckIcon,
  CodeIcon,
  ExternalLinkIcon,
  FileCodeIcon,
  GitCommitIcon,
  InfoIcon,
  ListChecksIcon,
  NetworkIcon,
  RouteIcon,
  ScrollTextIcon,
  ShieldIcon,
  TargetIcon,
} from "lucide-react";

import { CopyButton } from "@/components/copy-button";
import { Markdown } from "@/components/markdown";
import { StatusBadge } from "@/components/status-badge";
import { Badge } from "@/components/ui/badge";
import { Collapsible, CollapsibleContent, CollapsibleTrigger } from "@/components/ui/collapsible";
import { type MessageKey, type TFunction, useI18n } from "@/i18n";
import { statusMeta, type Tone, toneClasses, toneDot } from "@/lib/status";
import type {
  JiraTarget,
  JiraTaskLinks,
  ReportClassInfo,
  ReportEvidenceTrace,
  ReportFinding,
  ReportFindingLifecycle,
  ReportRecord,
  ReportView,
  TaskExecution,
} from "@/lib/types";
import { cn } from "@/lib/utils";

import { JiraFindingControl, JiraIssueBadge } from "./jira-export";
import { executionLabel, executionTip, hasNotRun } from "./task-execution";

// Severity numerals follow the app's severity tones (lib/status.ts).
const SEVERITY_TILES = [
  { key: "critical", label: "report.viewSeverityCritical", className: "text-rose-600 dark:text-rose-400" },
  { key: "high", label: "report.viewSeverityHigh", className: "text-red-600 dark:text-red-400" },
  { key: "medium", label: "report.viewSeverityMedium", className: "text-amber-600 dark:text-amber-400" },
  { key: "low", label: "report.viewSeverityLow", className: "text-slate-600 dark:text-slate-400" },
  { key: "info", label: "report.viewSeverityOther", className: "text-muted-foreground" },
] as const;

const SEVERITY_DOT: Record<string, string> = {
  critical: "bg-rose-500",
  high: "bg-red-500",
  medium: "bg-amber-500",
  low: "bg-slate-400",
  info: "bg-muted-foreground/50",
};

// Lifecycle stages of a code-review finding, as the Go report names them (report/lifecycle.go).
const STAGES: Record<string, { label: MessageKey; tone: Tone }> = {
  validated: { label: "report.viewStageValidated", tone: "green" },
  still_present: { label: "report.viewStageStillPresent", tone: "red" },
  merged: { label: "report.viewStageMerged", tone: "blue" },
  pr_open: { label: "report.viewStagePrOpen", tone: "blue" },
  fix_proposed: { label: "report.viewStageFixProposed", tone: "violet" },
  confirmed: { label: "report.viewStageConfirmed", tone: "amber" },
  false_positive: { label: "report.viewStageFalsePositive", tone: "slate" },
  needs_validation: { label: "report.viewStageNeedsValidation", tone: "amber" },
  unverified: { label: "report.viewStageUnverified", tone: "neutral" },
};

function StageBadge({ stage }: { stage: string }) {
  const { t: swt } = useI18n();
  const s = STAGES[stage];
  const tone = s?.tone ?? "neutral";
  return (
    <Badge variant="outline" className={toneClasses[tone]}>
      {stage === "validated" && <CircleCheckIcon data-icon="inline-start" />}
      {s ? swt(s.label) : stage}
    </Badge>
  );
}

function verificationText(v: string, swt: TFunction) {
  if (v === "confirmed") return swt("report.viewVerifConfirmed");
  if (v === "rejected") return swt("report.viewVerifRejected");
  if (v === "needs_validation") return swt("report.viewVerifNeedsValidation");
  return swt("report.viewVerifNone");
}

function fixText(l: ReportFindingLifecycle, swt: TFunction) {
  if (l.pr_state === "merged") return swt("report.viewFixMerged");
  if (l.pr_state === "open") return swt("report.viewFixOpen");
  if (l.pr_state === "closed") return swt("report.viewFixClosed");
  if (!l.fix) return swt("report.viewFixNone");
  if (l.fix === "ready") return swt("report.viewFixReady");
  if (l.fix === "drafting" || l.fix === "in_review") return swt("report.viewFixInProgress");
  return swt("report.viewFixUnusable", { p0: l.fix });
}

function afterMergeText(l: ReportFindingLifecycle, swt: TFunction) {
  if (l.pr_state !== "merged") return "-";
  if (l.recheck === "rejected") return swt("report.viewAfterFixed");
  if (l.recheck === "confirmed") return swt("report.viewAfterStill");
  if (l.recheck === "needs_validation") return swt("report.viewAfterInconclusive");
  return swt("report.viewAfterNotRechecked");
}

function when(value: string | undefined, swt: TFunction) {
  if (!value) return swt("report.viewNotRecorded");
  const d = new Date(value);
  return Number.isNaN(d.getTime()) ? value : d.toLocaleString();
}

function day(value?: string) {
  if (!value) return "";
  const d = new Date(value);
  return Number.isNaN(d.getTime()) ? value : d.toLocaleDateString();
}

function lifecycleDates(l: ReportFindingLifecycle, swt: TFunction) {
  return [
    ["report.viewDateVerified", l.verified_at],
    ["report.viewDatePrOpened", l.pr_opened_at],
    ["report.viewDateRechecked", l.rechecked_at],
  ]
    .filter(([, at]) => at)
    .map(([key, at]) => swt(key as MessageKey, { p0: day(at) }));
}

// lineHref links cited lines at the reviewed commit ("10-14" -> "#L10-L14").
function lineHref(blobBase: string | undefined, file: string, range?: string) {
  if (!blobBase) return undefined;
  if (!range) return blobBase + file;
  const [a, b] = range.split("-");
  return `${blobBase}${file}#L${a}${b ? `-L${b}` : ""}`;
}

function findingAnchor(id: number) {
  return `report-finding-${id}`;
}

function SectionHeading({
  id,
  icon: Icon,
  title,
  hint,
}: {
  id: string;
  icon: React.ElementType;
  title: string;
  hint?: string;
}) {
  return (
    <div id={id} className="flex scroll-mt-24 flex-col gap-1">
      <h3 className="flex items-center gap-2 font-semibold text-base tracking-tight">
        <Icon className="size-4 text-primary" />
        {title}
      </h3>
      {hint && <p className="text-muted-foreground text-xs">{hint}</p>}
    </div>
  );
}

function Fact({ label, children }: { label: string; children: React.ReactNode }) {
  return (
    <div className="grid grid-cols-[9rem_1fr] gap-3 border-b py-2.5 text-sm last:border-b-0">
      <dt className="font-medium text-muted-foreground text-xs uppercase tracking-wide">{label}</dt>
      <dd className="min-w-0 break-words">{children}</dd>
    </div>
  );
}

function LineChips({ lines, file, blobBase }: { lines: string[]; file: string; blobBase?: string }) {
  const { t: swt } = useI18n();
  return (
    <div className="flex flex-wrap gap-1">
      {lines.map((l) => {
        const href = lineHref(blobBase, file, l);
        const chip = "rounded border bg-muted/60 px-1.5 py-0.5 font-mono text-[11px] text-foreground/80";
        return href ? (
          <a
            key={l}
            href={href}
            target="_blank"
            rel="noreferrer"
            title={swt("report.viewLineTitle", { p0: file, p1: l })}
            className={cn(chip, "transition-colors hover:border-primary/50 hover:text-primary")}
          >
            {l}
          </a>
        ) : (
          <span key={l} className={chip}>
            {l}
          </span>
        );
      })}
    </div>
  );
}

function ClassBadges({ info }: { info: ReportClassInfo }) {
  if (!info.label) return null;
  return (
    <span className="flex flex-wrap items-center gap-1.5">
      <span className="text-muted-foreground text-xs">{info.label}</span>
      {info.cwe && (
        <span className="rounded border px-1.5 py-0.5 font-mono text-[10.5px] text-muted-foreground">{info.cwe}</span>
      )}
      {info.owasp && (
        <span className="rounded border px-1.5 py-0.5 text-[10.5px] text-muted-foreground">{info.owasp}</span>
      )}
    </span>
  );
}

// ClassReference links the CWE entry; shown inside the expanded finding.
function ClassReference({ info }: { info: ReportClassInfo }) {
  const { t: swt } = useI18n();
  if (!info.cwe) return null;
  return (
    <p className="text-muted-foreground text-xs">
      {swt("report.viewClassification", { p0: info.label })} ·{" "}
      <a
        href={`https://cwe.mitre.org/data/definitions/${info.cwe.replace(/^CWE-/, "")}.html`}
        target="_blank"
        rel="noreferrer"
        className="inline-flex items-center gap-1 text-primary underline-offset-4 hover:underline"
      >
        {info.cwe} <ExternalLinkIcon className="size-3" />
      </a>
      {info.owasp && ` · ${swt("report.viewOwaspTop10", { p0: info.owasp })}`}
    </p>
  );
}

function Lifecycle({ view }: { view: ReportView }) {
  const { t: swt } = useI18n();
  return (
    <div className="flex flex-col gap-4">
      <div className="flex flex-wrap gap-2">
        {(view.lifecycle ?? []).map((c) => (
          <div key={c.key} className="flex items-center gap-2 rounded-lg border bg-card px-3 py-2">
            <span className="font-semibold text-xl tabular-nums">{c.count}</span>
            <StageBadge stage={c.key} />
          </div>
        ))}
      </div>
      <div className="overflow-x-auto rounded-lg border bg-card">
        <table className="w-full text-sm">
          <thead className="bg-muted/60 text-left text-[11px] text-muted-foreground uppercase tracking-wide">
            <tr>
              <th className="px-3 py-2 font-semibold">{swt("report.viewColFinding")}</th>
              <th className="px-3 py-2 font-semibold">{swt("report.viewColVerification")}</th>
              <th className="px-3 py-2 font-semibold">{swt("report.viewColFix")}</th>
              <th className="px-3 py-2 font-semibold">{swt("report.viewColAfterMerge")}</th>
              <th className="px-3 py-2 font-semibold">{swt("report.viewColDates")}</th>
            </tr>
          </thead>
          <tbody>
            {view.findings.map((f) => {
              const l = f.lifecycle;
              if (!l) return null;
              return (
                <tr key={f.id} className="border-t align-top">
                  <td className="px-3 py-2">
                    <div className="flex flex-col gap-1">
                      <a href={`#${findingAnchor(f.id)}`} className="font-medium leading-snug hover:text-primary">
                        {f.title}
                      </a>
                      <span className="flex flex-wrap items-center gap-1.5">
                        <StatusBadge domain="severity" value={f.severity} dot />
                        <span className="font-mono text-muted-foreground text-xs">#{f.id}</span>
                      </span>
                    </div>
                  </td>
                  <td className="px-3 py-2">{verificationText(l.verification, swt)}</td>
                  <td className="px-3 py-2">
                    {l.pr_url ? (
                      <a
                        href={l.pr_url}
                        target="_blank"
                        rel="noreferrer"
                        className="inline-flex items-center gap-1 text-primary underline-offset-4 hover:underline"
                      >
                        {fixText(l, swt)}
                        <ExternalLinkIcon className="size-3" />
                      </a>
                    ) : (
                      fixText(l, swt)
                    )}
                  </td>
                  <td className="px-3 py-2">
                    {f.stage === "validated" || f.stage === "still_present" ? (
                      <StageBadge stage={f.stage} />
                    ) : (
                      afterMergeText(l, swt)
                    )}
                  </td>
                  <td className="px-3 py-2 text-muted-foreground text-xs">
                    {lifecycleDates(l, swt).map((d) => (
                      <div key={d} className="whitespace-nowrap">
                        {d}
                      </div>
                    ))}
                    {lifecycleDates(l, swt).length === 0 && "-"}
                  </td>
                </tr>
              );
            })}
          </tbody>
        </table>
      </div>
      <p className="text-muted-foreground text-xs">{swt("report.viewLifecycleNote")}</p>
    </div>
  );
}

function Header({ view, execution, status }: { view: ReportView; execution?: TaskExecution; status?: string }) {
  const { t: swt } = useI18n();
  const cr = view.code_review;
  // The report's own isolation sentence wins; the task's execution record is the fallback.
  const notRun = execution ? hasNotRun(execution, status) : false;
  const isolation = view.execution_isolation || (execution ? executionLabel(execution, swt, notRun) : "");
  const isolationTip = execution ? executionTip(execution, swt, notRun) : undefined;
  return (
    <section className="relative overflow-hidden rounded-xl border bg-card p-6 lg:p-8">
      <div className="absolute inset-y-0 left-0 w-1 bg-primary" aria-hidden />
      <p className="font-semibold text-[11px] text-primary uppercase tracking-[0.14em]">
        {cr ? swt("report.viewHeaderCodeReview") : swt("report.viewHeaderPentest")}
      </p>
      <h2 className="mt-2 text-balance font-semibold text-2xl tracking-tight">{view.title}</h2>
      <p className="mt-2 max-w-3xl text-muted-foreground text-sm">
        {view.execution.headline}. {view.execution.detail}
      </p>
      <div className="mt-4 flex flex-wrap items-center gap-2 text-xs">
        {view.draft && (
          <span className="rounded-md bg-foreground px-2 py-0.5 font-semibold text-background tracking-wide">
            {swt("report.viewDraft")}
          </span>
        )}
        <code className="rounded bg-muted px-1.5 py-0.5 font-mono text-muted-foreground">
          {swt("report.viewTaskNumber", { p0: view.task_id })}
        </code>
        <StatusBadge domain="task" value={view.status} dot />
        <span className="text-muted-foreground">
          {swt("report.viewGenerated", { p0: when(view.generated_at, swt) })}
        </span>
        {isolation && (
          <span className="inline-flex items-center gap-1.5 text-muted-foreground" title={isolationTip}>
            <ShieldIcon className="size-3.5 shrink-0" aria-hidden="true" />
            {swt("report.viewIsolation", { p0: isolation })}
          </span>
        )}
        {view.document_id && (
          <span className="font-mono text-muted-foreground" title={swt("report.viewDocumentId")}>
            {view.document_id}
          </span>
        )}
      </div>
      {cr && (
        <div className="mt-4 flex flex-wrap items-center gap-x-4 gap-y-2 border-t pt-4 text-sm">
          <a
            href={cr.url}
            target="_blank"
            rel="noreferrer"
            className="inline-flex items-center gap-1.5 font-medium text-primary underline-offset-4 hover:underline"
          >
            <CodeIcon className="size-4" />
            {cr.repository}
            <ExternalLinkIcon className="size-3" />
          </a>
          <span className="inline-flex items-center gap-1.5 text-muted-foreground">
            <GitCommitIcon className="size-4" />
            {cr.commit ? (
              <code className="font-mono text-foreground text-xs">{cr.commit.slice(0, 12)}</code>
            ) : (
              swt("report.viewCommitNotRecorded")
            )}
          </span>
          {cr.mode && <Badge variant="outline">{cr.mode_label || cr.mode}</Badge>}
          {cr.inferred && (
            <Badge variant="outline" className="text-muted-foreground" title={cr.target_source}>
              {swt("report.viewTargetInferred")}
            </Badge>
          )}
        </div>
      )}
    </section>
  );
}

function Metrics({ view }: { view: ReportView }) {
  const { t: swt } = useI18n();
  return (
    <div className="grid grid-cols-2 gap-3 sm:grid-cols-3 lg:grid-cols-6">
      {SEVERITY_TILES.map((s) => (
        <div key={s.key} className="rounded-lg border bg-card p-4">
          <div className={cn("font-semibold text-3xl tabular-nums", s.className)}>{view.severity[s.key]}</div>
          <div className="mt-1 font-medium text-[11px] text-muted-foreground uppercase tracking-wide">
            {swt(s.label)}
          </div>
        </div>
      ))}
      <div className="rounded-lg border bg-card p-4">
        <div className="font-semibold text-3xl tabular-nums">{view.open_reviews}</div>
        <div className="mt-1 font-medium text-[11px] text-muted-foreground uppercase tracking-wide">
          {swt("report.viewAwaitingReview")}
        </div>
      </div>
    </div>
  );
}

function Priorities({ view }: { view: ReportView }) {
  const { t: swt } = useI18n();
  const actions = view.key_actions;
  return (
    <div className="rounded-lg border bg-card p-5">
      <div className="mb-3 flex items-center gap-2 font-semibold text-[11px] text-muted-foreground uppercase tracking-wide">
        <TargetIcon className="size-3.5 text-primary" /> {swt("report.viewPriorities")}
      </div>
      {actions.length === 0 ? (
        <p className="text-muted-foreground text-sm">{swt("report.viewNoActions")}</p>
      ) : (
        <ol className="flex flex-col gap-3">
          {actions.map((a, i) => (
            <li key={a.finding_id} className="flex gap-3">
              <span className="mt-0.5 font-mono text-muted-foreground text-xs tabular-nums">{i + 1}</span>
              <div className="flex min-w-0 flex-col gap-1">
                <a
                  href={`#${findingAnchor(a.finding_id)}`}
                  className="flex items-start gap-2 font-medium text-sm leading-snug hover:text-primary"
                >
                  <span className="shrink-0">
                    <StatusBadge domain="severity" value={a.severity} dot />
                  </span>
                  <span className="min-w-0">{a.title}</span>
                </a>
                <span className="flex items-start gap-1.5 text-muted-foreground text-xs">
                  <ArrowRightIcon className="mt-0.5 size-3 shrink-0" />
                  {a.action}
                </span>
              </div>
            </li>
          ))}
        </ol>
      )}
    </div>
  );
}

function ReviewBar({ view }: { view: ReportView }) {
  const { t: swt } = useI18n();
  const total = view.review.reduce((n, r) => n + r.count, 0);
  return (
    <div className="flex flex-col gap-3">
      {total > 0 ? (
        <div className="flex h-2.5 overflow-hidden rounded-full bg-muted">
          {view.review.map((r) => (
            <div
              key={r.key}
              className={toneDot[statusMeta("finding", r.key).tone]}
              style={{ width: `${(r.count / total) * 100}%` }}
              title={`${statusMeta("finding", r.key).label}: ${r.count}`}
            />
          ))}
        </div>
      ) : (
        <p className="text-muted-foreground text-sm">{swt("report.viewNoFindingsRegistered")}</p>
      )}
      <div className="flex flex-wrap gap-2">
        {view.review.map((r) => (
          <span key={r.key} className="inline-flex items-center gap-1.5 text-sm">
            <StatusBadge domain="finding" value={r.key} dot />
            <span className="text-muted-foreground tabular-nums">{r.count}</span>
          </span>
        ))}
      </div>
      {view.posture && <p className="text-sm">{view.posture}</p>}
      <p className="text-muted-foreground text-xs leading-relaxed">{swt("report.viewSeverityNote")}</p>
    </div>
  );
}

function Objective({ view }: { view: ReportView }) {
  const { t: swt } = useI18n();
  const instructions = view.goal.trim();
  const objective = view.objective || instructions || swt("report.viewNotSpecified");
  return (
    <div className="flex flex-col gap-3 rounded-lg border bg-card p-5">
      <div className="font-semibold text-[11px] text-muted-foreground uppercase tracking-wide">
        {swt("report.viewObjective")}
      </div>
      <p className="text-sm leading-7">{objective}</p>
      {instructions && instructions !== objective && (
        <Collapsible>
          <CollapsibleTrigger className="group inline-flex items-center gap-1 font-medium text-primary text-xs">
            {swt("report.viewTaskInstructions")}
            <ChevronDownIcon className="size-3.5 transition-transform group-data-[state=open]:rotate-180" />
          </CollapsibleTrigger>
          <CollapsibleContent>
            <p className="mt-2 whitespace-pre-wrap rounded-md border bg-muted/40 p-3 text-muted-foreground text-xs leading-relaxed">
              {instructions}
            </p>
          </CollapsibleContent>
        </Collapsible>
      )}
    </div>
  );
}

function ReviewTarget({ view }: { view: ReportView }) {
  const { t: swt } = useI18n();
  const cr = view.code_review;
  if (!cr) return null;
  return (
    <div className="grid gap-4 lg:grid-cols-5">
      <dl className="rounded-lg border bg-card px-4 py-1 lg:col-span-2">
        <Fact label={swt("report.viewFactRepository")}>
          <a href={cr.url} target="_blank" rel="noreferrer" className="text-primary underline-offset-4 hover:underline">
            {cr.url}
          </a>
        </Fact>
        <Fact label={swt("report.viewFactRef")}>{cr.ref || swt("report.viewDefaultBranch")}</Fact>
        <Fact label={swt("report.viewFactCommit")}>
          {cr.commit ? <code className="break-all font-mono text-xs">{cr.commit}</code> : swt("report.viewNotRecorded")}
        </Fact>
        <Fact label={swt("report.viewFactReviewMode")}>
          {cr.mode_label || cr.mode || swt("report.viewNotRecorded")}
        </Fact>
        <Fact label={swt("report.viewFactMethod")}>{swt("report.viewMethodText")}</Fact>
        <Fact label={swt("report.viewFactTargetDetails")}>{cr.target_source}</Fact>
      </dl>
      <div className="flex flex-col gap-2 lg:col-span-3">
        <div className="overflow-x-auto rounded-lg border bg-card">
          <table className="w-full text-sm">
            <thead className="bg-muted/60 text-left text-[11px] text-muted-foreground uppercase tracking-wide">
              <tr>
                <th className="px-3 py-2 font-semibold">{swt("report.viewColFileCited")}</th>
                <th className="px-3 py-2 font-semibold">{swt("report.viewColLines")}</th>
                <th className="px-3 py-2 text-right font-semibold">{swt("report.viewFindings")}</th>
              </tr>
            </thead>
            <tbody>
              {view.cited_files.length === 0 && (
                <tr>
                  <td colSpan={3} className="px-3 py-4 text-muted-foreground">
                    {swt("report.viewNoCitedFiles")}
                  </td>
                </tr>
              )}
              {view.cited_files.map((f) => (
                <tr key={f.file} className="border-t align-top">
                  <td className="min-w-40 px-3 py-2 font-mono text-xs [overflow-wrap:anywhere]">
                    {cr.blob_base ? (
                      <a
                        href={lineHref(cr.blob_base, f.file)}
                        target="_blank"
                        rel="noreferrer"
                        className="hover:text-primary"
                      >
                        {f.file}
                      </a>
                    ) : (
                      f.file
                    )}
                  </td>
                  <td className="px-3 py-2">
                    <LineChips lines={f.lines} file={f.file} blobBase={cr.blob_base} />
                  </td>
                  <td className="px-3 py-2 text-right tabular-nums">{f.findings}</td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
        <p className="text-muted-foreground text-xs">
          {swt("report.viewCoverageNote")}
          {cr.blob_base && ` ${swt("report.viewLineLinksNote")}`}
        </p>
      </div>
    </div>
  );
}

function NetworkScope({ view }: { view: ReportView }) {
  const { t: swt } = useI18n();
  const pct =
    view.coverage && view.coverage.total > 0 ? Math.round((view.coverage.tested / view.coverage.total) * 100) : null;
  return (
    <div className="grid gap-4 lg:grid-cols-5">
      <div className="overflow-hidden rounded-lg border bg-card lg:col-span-3">
        <table className="w-full text-sm">
          <thead className="bg-muted/60 text-left text-[11px] text-muted-foreground uppercase tracking-wide">
            <tr>
              <th className="px-3 py-2 font-semibold">{swt("report.viewColKind")}</th>
              <th className="px-3 py-2 font-semibold">{swt("report.viewColTarget")}</th>
              <th className="px-3 py-2 font-semibold">{swt("report.viewColSource")}</th>
            </tr>
          </thead>
          <tbody>
            {view.scope.length === 0 && (
              <tr>
                <td colSpan={3} className="px-3 py-4 text-muted-foreground">
                  {swt("report.viewNoScopeRows")}
                </td>
              </tr>
            )}
            {view.scope.map((s) => (
              <tr key={`${s.kind}-${s.target}`} className="border-t">
                <td className="px-3 py-2">{s.kind}</td>
                <td className="break-all px-3 py-2 font-mono text-xs">{s.target}</td>
                <td className="px-3 py-2 text-muted-foreground">{s.source}</td>
              </tr>
            ))}
          </tbody>
        </table>
      </div>
      <div className="flex flex-col gap-3 rounded-lg border bg-card p-4 lg:col-span-2">
        <div className="font-medium text-[11px] text-muted-foreground uppercase tracking-wide">
          {swt("report.viewAssetCoverage")}
        </div>
        {pct === null ? (
          <p className="text-muted-foreground text-sm">{swt("report.viewCoverageDisabled")}</p>
        ) : (
          <>
            <div className="font-semibold text-3xl text-primary tabular-nums">{pct}%</div>
            <div className="h-2 overflow-hidden rounded-full bg-muted">
              <div className="h-full bg-primary" style={{ width: `${pct}%` }} />
            </div>
            <p className="text-muted-foreground text-xs">
              {swt("report.viewCoverageSummary", { p0: view.coverage?.tested, p1: view.coverage?.total })}
            </p>
          </>
        )}
        {view.assets.length > 0 && (
          <div className="flex flex-wrap gap-1.5 border-t pt-3">
            {view.assets.map((a) => (
              <Badge key={a.key} variant="outline">
                {a.key} <span className="text-muted-foreground tabular-nums">{a.count}</span>
              </Badge>
            ))}
          </div>
        )}
      </div>
    </div>
  );
}

const STEP_TONE: Record<string, string> = {
  entrypoint: "border-sky-500/40 bg-sky-500/10 text-sky-700 dark:text-sky-300",
  source: "border-sky-500/40 bg-sky-500/10 text-sky-700 dark:text-sky-300",
  sink: "border-rose-500/40 bg-rose-500/10 text-rose-700 dark:text-rose-300",
};

// Trace shows a source-to-sink code path as connected steps.
function Trace({ trace, blobBase }: { trace: ReportEvidenceTrace; blobBase?: string }) {
  const { t: swt } = useI18n();
  const fields = trace.fields.filter((f) => f.label !== "Fingerprint" && f.label !== "Ref");
  return (
    <div className="flex flex-col gap-4">
      <ol className="relative flex flex-col gap-3">
        {trace.steps.map((s, i) => {
          const [file, lines] = (s.location ?? "").split(":");
          const href = s.location ? lineHref(blobBase, file, lines) : undefined;
          return (
            <li key={`${s.kind}:${s.location ?? ""}:${s.detail.slice(0, 40)}`} className="relative flex gap-3">
              <div className="flex flex-col items-center">
                <span className="flex size-6 shrink-0 items-center justify-center rounded-full border bg-card font-mono text-[11px] text-muted-foreground">
                  {i + 1}
                </span>
                {i < trace.steps.length - 1 && <span className="mt-1 w-px flex-1 bg-border" aria-hidden />}
              </div>
              <div className="flex min-w-0 flex-1 flex-col gap-1 pb-1">
                <div className="flex flex-wrap items-center gap-2">
                  <span
                    className={cn(
                      "rounded border px-1.5 py-0.5 font-medium text-[10.5px] uppercase tracking-wide",
                      STEP_TONE[s.kind] ?? "border-border bg-muted/60 text-muted-foreground",
                    )}
                  >
                    {s.kind === "entrypoint" ? swt("report.viewStepEntryPoint") : s.kind}
                  </span>
                  {s.location &&
                    (href ? (
                      <a
                        href={href}
                        target="_blank"
                        rel="noreferrer"
                        className="font-mono text-primary text-xs underline-offset-4 hover:underline"
                      >
                        {s.location}
                      </a>
                    ) : (
                      <code className="font-mono text-xs">{s.location}</code>
                    ))}
                </div>
                <p className="text-sm leading-relaxed">{s.detail}</p>
              </div>
            </li>
          );
        })}
      </ol>
      {fields.length > 0 && (
        <dl className="rounded-lg border bg-card px-4 py-1">
          {fields.map((f) => (
            <Fact key={`${f.label}:${f.text.slice(0, 60)}`} label={f.label}>
              {f.text}
            </Fact>
          ))}
        </dl>
      )}
    </div>
  );
}

function evidenceTitle(traced: boolean, codeReview: boolean, swt: TFunction) {
  if (traced) return swt("report.viewEvidenceRaw");
  return codeReview ? swt("report.viewEvidenceCode") : swt("report.viewEvidence");
}

function FindingCard({
  finding,
  index,
  codeReview,
  blobBase,
  defaultOpen,
  jira,
}: {
  finding: ReportFinding;
  index: number;
  codeReview: boolean;
  blobBase?: string;
  defaultOpen: boolean;
  jira?: ReportJira;
}) {
  const { t: swt } = useI18n();
  const jiraLink = jira?.links?.findings[String(finding.id)];
  const [open, setOpen] = React.useState(defaultOpen);
  const traced = finding.trace.parsed;
  return (
    <Collapsible
      id={findingAnchor(finding.id)}
      open={open}
      onOpenChange={setOpen}
      className="scroll-mt-24 rounded-xl border bg-card"
    >
      <CollapsibleTrigger className="flex w-full items-start gap-3 p-4 text-left lg:p-5">
        <span className="mt-0.5 font-mono text-muted-foreground text-xs tabular-nums">
          {String(index + 1).padStart(2, "0")}
        </span>
        <div className="flex min-w-0 flex-1 flex-col gap-2">
          <div className="flex flex-wrap items-center gap-2">
            <StatusBadge domain="severity" value={finding.severity} dot />
            <StatusBadge domain="finding" value={finding.status} dot />
            {finding.stage && <StageBadge stage={finding.stage} />}
            {jiraLink && <JiraIssueBadge link={jiraLink} interactive={false} />}
          </div>
          <span className="font-semibold text-base leading-snug">{finding.title}</span>
          <ClassBadges info={finding.class_info} />
          <span className="flex flex-wrap gap-x-3 gap-y-1 text-muted-foreground text-xs">
            <span>{swt("report.viewFindingNumber", { p0: finding.id })}</span>
            <span>{swt("report.viewFoundAt", { p0: when(finding.created_at, swt) })}</span>
            {finding.code_locations.length > 0 && (
              <span>{swt("report.viewCodeLocations", { p0: finding.code_locations.length })}</span>
            )}
            {(!codeReview || finding.traffic.length > 0) && (
              <span>{swt("report.viewTrafficItems", { p0: finding.traffic.length })}</span>
            )}
          </span>
        </div>
        <ChevronDownIcon
          className={cn("mt-1 size-4 shrink-0 text-muted-foreground transition-transform", open && "rotate-180")}
        />
      </CollapsibleTrigger>
      <CollapsibleContent>
        <div className="flex flex-col gap-6 border-t px-4 py-5 lg:px-6">
          {finding.next_action && (
            <div className="flex items-start gap-2.5 rounded-lg border border-primary/25 bg-primary/5 px-4 py-3 text-sm">
              <ArrowRightIcon className="mt-0.5 size-4 shrink-0 text-primary" />
              <div>
                <span className="font-semibold">{swt("report.viewNextStep")} </span>
                {finding.next_action}
              </div>
            </div>
          )}
          {finding.summary && (
            <div className="flex flex-col gap-2">
              <h4 className="font-semibold text-[11px] text-muted-foreground uppercase tracking-wide">
                {swt("report.viewOverview")}
              </h4>
              <Markdown text={finding.summary} variant="document" />
              <ClassReference info={finding.class_info} />
            </div>
          )}
          {finding.code_locations.length > 0 && (
            <div className="flex flex-col gap-2">
              <h4 className="font-semibold text-[11px] text-muted-foreground uppercase tracking-wide">
                {swt("report.viewAffectedCode")}
              </h4>
              <div className="divide-y rounded-lg border">
                {finding.code_locations.map((l) => (
                  <div key={l.file} className="flex flex-wrap items-center justify-between gap-2 px-3 py-2">
                    <span className="inline-flex min-w-0 items-center gap-2 font-mono text-xs [overflow-wrap:anywhere]">
                      <FileCodeIcon className="size-3.5 shrink-0 text-primary" />
                      {l.file}
                    </span>
                    <LineChips lines={l.lines} file={l.file} blobBase={blobBase} />
                  </div>
                ))}
              </div>
            </div>
          )}
          {traced && (
            <div className="flex flex-col gap-2">
              <h4 className="font-semibold text-[11px] text-muted-foreground uppercase tracking-wide">
                {swt("report.viewCodeTrace")}
              </h4>
              <Trace trace={finding.trace} blobBase={blobBase} />
            </div>
          )}
          {finding.evidence && (
            <Collapsible defaultOpen={!traced} className="flex flex-col gap-2">
              <div className="flex items-center justify-between gap-2">
                <CollapsibleTrigger className="group inline-flex items-center gap-1 font-semibold text-[11px] text-muted-foreground uppercase tracking-wide">
                  {evidenceTitle(traced, codeReview, swt)}
                  <ChevronDownIcon className="size-3.5 transition-transform group-data-[state=open]:rotate-180" />
                </CollapsibleTrigger>
                <CopyButton text={finding.evidence} size="sm" variant="ghost" />
              </div>
              <CollapsibleContent>
                <pre className="max-h-96 overflow-auto whitespace-pre-wrap break-words rounded-lg border bg-muted/60 p-4 font-mono text-[12.5px] leading-relaxed">
                  {finding.evidence}
                </pre>
              </CollapsibleContent>
            </Collapsible>
          )}
          {finding.traffic.length > 0 && (
            <div className="flex flex-col gap-2">
              <h4 className="font-semibold text-[11px] text-muted-foreground uppercase tracking-wide">
                {swt("report.viewLinkedTraffic")}
              </h4>
              <div className="overflow-x-auto rounded-lg border">
                <table className="w-full text-sm">
                  <tbody>
                    {finding.traffic.map((t) => (
                      <tr key={t.id} className="border-b last:border-b-0">
                        <td className="px-3 py-2 font-mono text-muted-foreground text-xs">#{t.id}</td>
                        <td className="px-3 py-2">{t.role}</td>
                        <td className="px-3 py-2 font-mono text-xs">{t.method}</td>
                        <td className="break-all px-3 py-2 font-mono text-xs">
                          {t.url}
                          {t.note && <div className="mt-1 font-sans text-muted-foreground">{t.note}</div>}
                        </td>
                        <td className="px-3 py-2 text-right font-mono text-xs tabular-nums">{t.status}</td>
                      </tr>
                    ))}
                  </tbody>
                </table>
              </div>
            </div>
          )}
          {finding.report && (
            <div className="flex flex-col gap-2">
              <h4 className="font-semibold text-[11px] text-muted-foreground uppercase tracking-wide">
                {swt("report.viewDetailedReport")}
              </h4>
              {finding.report_stale && (
                <p className="flex items-center gap-2 rounded-md border border-amber-500/30 bg-amber-500/10 px-3 py-2 text-amber-800 text-sm dark:text-amber-300">
                  <AlertTriangleIcon className="size-4" /> {swt("report.viewReportStale")}
                </p>
              )}
              <div className="rounded-lg border bg-background/60 p-5 lg:p-6">
                <Markdown text={finding.report} variant="document" />
              </div>
            </div>
          )}
          <div className="flex flex-wrap items-center gap-3">
            {jira && <JiraFindingControl findingId={finding.id} links={jira.links} onExport={jira.exportFinding} />}
            <Link
              href={`/function/findings/detail?id=${finding.id}`}
              className="inline-flex items-center gap-1.5 font-medium text-primary text-sm underline-offset-4 hover:underline"
            >
              {swt("report.viewOpenFindingWorkspace")} <ExternalLinkIcon className="size-3.5" />
            </Link>
          </div>
        </div>
      </CollapsibleContent>
    </Collapsible>
  );
}

function Records({ title, records, domain }: { title: string; records: ReportRecord[]; domain: "node" | "intent" }) {
  const { t: swt } = useI18n();
  return (
    <Collapsible className="rounded-lg border bg-card">
      <CollapsibleTrigger className="group flex w-full items-center justify-between gap-2 px-4 py-3 text-left font-medium text-sm">
        <span>
          {title} <span className="text-muted-foreground tabular-nums">({records.length})</span>
        </span>
        <ChevronDownIcon className="size-4 text-muted-foreground transition-transform group-data-[state=open]:rotate-180" />
      </CollapsibleTrigger>
      <CollapsibleContent>
        <div className="divide-y border-t">
          {records.length === 0 && (
            <p className="px-4 py-3 text-muted-foreground text-sm">{swt("report.viewNothingRecorded")}</p>
          )}
          {records.map((r) => (
            <div key={r.id} className="flex flex-col gap-1.5 px-4 py-3">
              <div className="flex flex-wrap items-center gap-2 text-muted-foreground text-xs">
                <StatusBadge domain={domain} value={r.state} />
                <span>{swt("report.viewRecordNumber", { p0: r.id })}</span>
                <span>{when(r.created_at, swt)}</span>
                <span>{swt("report.viewRecordOrigin", { p0: r.origin })}</span>
              </div>
              <p className="whitespace-pre-wrap text-sm leading-relaxed">{r.summary}</p>
              {r.issue && (
                <p className="text-amber-700 text-xs dark:text-amber-400">
                  {swt("report.viewExecutionIssue", { p0: r.issue })}
                </p>
              )}
              {r.removal && (
                <p className="text-muted-foreground text-xs">{swt("report.viewRemovalReason", { p0: r.removal })}</p>
              )}
            </div>
          ))}
        </div>
      </CollapsibleContent>
    </Collapsible>
  );
}

type StatusFilter = "all" | "open" | "closed";

const STATUS_FILTER_LABELS: Record<StatusFilter, MessageKey> = {
  all: "report.viewFilterAll",
  open: "report.viewFilterOpen",
  closed: "report.viewFilterClosed",
};

function FilterChip({
  active,
  onClick,
  children,
}: {
  active: boolean;
  onClick: () => void;
  children: React.ReactNode;
}) {
  return (
    <button
      type="button"
      aria-pressed={active}
      onClick={onClick}
      className={cn(
        "inline-flex items-center gap-1.5 rounded-full border px-2.5 py-1 font-medium text-xs transition-colors",
        active
          ? "border-primary/50 bg-primary/10 text-foreground"
          : "bg-card text-muted-foreground hover:text-foreground",
      )}
    >
      {children}
    </button>
  );
}

// Contents is the sticky side index on wide screens: sections, then findings.
function Contents({ nav, findings }: { nav: { id: string; label: string }[]; findings: ReportFinding[] }) {
  const { t: swt } = useI18n();
  return (
    <nav aria-label={swt("report.viewContentsAria")} className="flex flex-col gap-4 text-sm">
      <div className="flex flex-col gap-0.5">
        <span className="mb-1 font-semibold text-[11px] text-muted-foreground uppercase tracking-wide">
          {swt("report.viewContents")}
        </span>
        {nav.map((n) => (
          <a
            key={n.id}
            href={`#${n.id}`}
            className="rounded-md px-2 py-1 text-muted-foreground transition-colors hover:bg-muted hover:text-foreground"
          >
            {n.label}
          </a>
        ))}
      </div>
      {findings.length > 0 && (
        <div className="flex flex-col gap-0.5">
          <span className="mb-1 font-semibold text-[11px] text-muted-foreground uppercase tracking-wide">
            {swt("report.viewFindings")}
          </span>
          {findings.map((f, i) => (
            <a
              key={f.id}
              href={`#${findingAnchor(f.id)}`}
              className="flex items-start gap-2 rounded-md px-2 py-1 text-muted-foreground text-xs leading-snug transition-colors hover:bg-muted hover:text-foreground"
            >
              <span
                className={cn("mt-1 size-1.5 shrink-0 rounded-full", SEVERITY_DOT[f.severity] ?? SEVERITY_DOT.info)}
              />
              <span className="line-clamp-2">
                {i + 1}. {f.title}
              </span>
            </a>
          ))}
        </div>
      )}
    </nav>
  );
}

// ReportJira lets the report file findings in Jira; absent, no Jira control shows.
export interface ReportJira {
  links: JiraTaskLinks | null;
  exportFinding: (findingId: number, target?: JiraTarget) => Promise<boolean>;
}

export function ReportDocument({
  view,
  jira,
  execution,
  status,
}: {
  view: ReportView;
  jira?: ReportJira;
  execution?: TaskExecution;
  status?: string;
}) {
  "use no memo";
  const { t: swt } = useI18n();
  const codeReview = view.kind === "code_review";
  const blobBase = view.code_review?.blob_base;
  const [severities, setSeverities] = React.useState<Set<string>>(new Set());
  const [statusFilter, setStatusFilter] = React.useState<StatusFilter>("all");

  const presentSeverities = SEVERITY_TILES.map((s) => s.key).filter((k) => view.findings.some((f) => f.severity === k));
  const visible = view.findings.filter(
    (f) =>
      (severities.size === 0 || severities.has(f.severity)) &&
      (statusFilter === "all" || (statusFilter === "open" ? !!f.next_action : !f.next_action)),
  );
  const toggleSeverity = (key: string) =>
    setSeverities((prev) => {
      const next = new Set(prev);
      if (next.has(key)) next.delete(key);
      else next.add(key);
      return next;
    });

  let findingsBody: React.ReactNode;
  if (view.findings.length === 0) {
    findingsBody = (
      <p className="rounded-lg border bg-card p-5 text-muted-foreground text-sm">
        {codeReview ? swt("report.viewNoFindingsCode") : swt("report.viewNoFindingsPentest")}
      </p>
    );
  } else if (visible.length === 0) {
    findingsBody = (
      <p className="rounded-lg border bg-card p-5 text-muted-foreground text-sm">{swt("report.viewNoFindingsMatch")}</p>
    );
  } else {
    findingsBody = visible.map((f) => (
      <FindingCard
        key={f.id}
        finding={f}
        index={view.findings.indexOf(f)}
        codeReview={codeReview}
        blobBase={blobBase}
        jira={jira}
        defaultOpen={view.findings.length <= 3 || view.findings.indexOf(f) === 0}
      />
    ));
  }

  const nav = [
    { id: "report-summary", label: swt("report.viewNavSummary") },
    { id: "report-target", label: codeReview ? swt("report.viewNavReviewTarget") : swt("report.viewNavScope") },
    ...(view.lifecycle?.length ? [{ id: "report-lifecycle", label: swt("report.viewNavLifecycle") }] : []),
    { id: "report-findings", label: swt("report.viewNavFindings", { p0: view.findings.length }) },
    { id: "report-records", label: swt("report.viewNavRecords") },
    { id: "report-limitations", label: swt("report.viewNavLimitations") },
  ];
  return (
    <div className="grid gap-8 xl:grid-cols-[minmax(0,1fr)_14rem]">
      <div className="flex min-w-0 flex-col gap-8">
        <Header view={view} execution={execution} status={status} />

        <nav className="flex flex-wrap gap-1.5 xl:hidden" aria-label={swt("report.viewSectionsAria")}>
          {nav.map((n) => (
            <a
              key={n.id}
              href={`#${n.id}`}
              className="rounded-full border bg-card px-3 py-1 font-medium text-muted-foreground text-xs transition-colors hover:border-primary/40 hover:text-foreground"
            >
              {n.label}
            </a>
          ))}
        </nav>

        {view.execution.kind !== "completed" && (
          <div className="flex gap-3 rounded-lg border border-amber-500/30 bg-amber-500/10 p-4 text-sm">
            <AlertTriangleIcon className="mt-0.5 size-4 shrink-0 text-amber-600" />
            <div>
              <p className="font-semibold text-amber-900 dark:text-amber-200">{view.execution.headline}</p>
              <p className="mt-0.5 text-amber-900/80 dark:text-amber-200/80">{view.execution.detail}</p>
            </div>
          </div>
        )}

        <section className="flex flex-col gap-4">
          <SectionHeading id="report-summary" icon={ListChecksIcon} title={swt("report.viewExecutiveSummary")} />
          <Metrics view={view} />
          <div className="grid gap-4 lg:grid-cols-5">
            <div className="flex flex-col gap-4 lg:col-span-3">
              <Objective view={view} />
              <Priorities view={view} />
            </div>
            <div className="rounded-lg border bg-card p-5 lg:col-span-2">
              <div className="mb-3 font-semibold text-[11px] text-muted-foreground uppercase tracking-wide">
                {swt("report.viewReviewStatus")}
              </div>
              <ReviewBar view={view} />
            </div>
          </div>
        </section>

        <section className="flex flex-col gap-4">
          {codeReview ? (
            <>
              <SectionHeading
                id="report-target"
                icon={CodeIcon}
                title={swt("report.viewNavReviewTarget")}
                hint={swt("report.viewReviewTargetHint")}
              />
              <ReviewTarget view={view} />
              {view.lifecycle?.length ? (
                <>
                  <SectionHeading
                    id="report-lifecycle"
                    icon={RouteIcon}
                    title={swt("report.viewLifecycleTitle")}
                    hint={swt("report.viewLifecycleHint")}
                  />
                  <Lifecycle view={view} />
                </>
              ) : null}
            </>
          ) : (
            <>
              <SectionHeading
                id="report-target"
                icon={NetworkIcon}
                title={swt("report.viewScopeTitle")}
                hint={swt("report.viewScopeHint")}
              />
              <NetworkScope view={view} />
            </>
          )}
        </section>

        <section className="flex flex-col gap-4">
          <SectionHeading
            id="report-findings"
            icon={AlertTriangleIcon}
            title={swt("report.viewFindings")}
            hint={swt("report.viewFindingsHint")}
          />
          {view.findings.length > 1 && (
            <fieldset className="m-0 flex min-w-0 flex-wrap items-center gap-2 border-0 p-0">
              <legend className="sr-only">{swt("report.viewFilterFindings")}</legend>
              {presentSeverities.map((k) => (
                <FilterChip key={k} active={severities.has(k)} onClick={() => toggleSeverity(k)}>
                  <span className={cn("size-1.5 rounded-full", SEVERITY_DOT[k])} />
                  {swt(SEVERITY_TILES.find((s) => s.key === k)?.label ?? "report.viewSeverityOther")}
                </FilterChip>
              ))}
              <span className="mx-1 h-4 w-px bg-border" aria-hidden />
              {(["all", "open", "closed"] as const).map((s) => (
                <FilterChip key={s} active={statusFilter === s} onClick={() => setStatusFilter(s)}>
                  {swt(STATUS_FILTER_LABELS[s])}
                </FilterChip>
              ))}
              <span className="ml-auto text-muted-foreground text-xs tabular-nums">
                {swt("report.viewVisibleOf", { p0: visible.length, p1: view.findings.length })}
              </span>
            </fieldset>
          )}
          {findingsBody}
        </section>

        <section className="flex flex-col gap-4">
          <SectionHeading
            id="report-records"
            icon={ScrollTextIcon}
            title={swt("report.viewRecordsTitle")}
            hint={swt("report.viewRecordsHint")}
          />
          <Records title={swt("report.viewConclusions")} records={view.conclusions} domain="node" />
          <Records title={swt("report.viewExecutionRecord")} records={view.attempts} domain="intent" />
        </section>

        <section className="grid gap-4 lg:grid-cols-2">
          <div className="flex flex-col gap-4">
            <SectionHeading id="report-limitations" icon={InfoIcon} title={swt("report.viewLimitations")} />
            <ul className="flex flex-col gap-2.5 rounded-lg border bg-card p-5 text-sm leading-relaxed">
              {view.limitations.map((l) => (
                <li key={l} className="flex gap-2.5">
                  <span className="mt-2 size-1.5 shrink-0 rounded-full bg-primary" />
                  {l}
                </li>
              ))}
            </ul>
          </div>
          <div className="flex flex-col gap-4">
            <SectionHeading id="report-provenance" icon={GitCommitIcon} title={swt("report.viewProvenance")} />
            <dl className="rounded-lg border bg-card px-4 py-1">
              {view.document_id && <Fact label={swt("report.viewFactDocument")}>{view.document_id}</Fact>}
              <Fact label={swt("report.viewFactTask")}>#{view.task_id}</Fact>
              <Fact label={swt("report.viewFactStatus")}>{view.status_label || view.status}</Fact>
              <Fact label={swt("report.viewFactCreated")}>{when(view.created_at, swt)}</Fact>
              {view.first_run_at && <Fact label={swt("report.viewFactFirstRun")}>{when(view.first_run_at, swt)}</Fact>}
              <Fact label={swt("report.viewFactCompleted")}>
                {view.completed_at
                  ? when(view.completed_at, swt)
                  : view.execution.kind === "completed"
                    ? swt("report.viewNotRecorded")
                    : swt("report.viewNotCompleted")}
              </Fact>
              <Fact label={swt("report.viewFactModels")}>
                {view.models.length ? view.models.join("; ") : swt("report.viewNotRecorded")}
              </Fact>
              <Fact label={swt("report.viewFactSoftware")}>Counterweight {view.version}</Fact>
              {view.fingerprint && (
                <Fact label={swt("report.viewFactFingerprint")}>
                  <span className="flex items-start gap-1">
                    <code className="break-all font-mono text-xs" title={swt("report.viewFingerprintTitle")}>
                      {view.fingerprint}
                    </code>
                    <CopyButton text={view.fingerprint} size="sm" variant="ghost" />
                  </span>
                </Fact>
              )}
            </dl>
            <p className="text-muted-foreground text-xs">{swt("report.viewModelsNote")}</p>
          </div>
        </section>
      </div>

      <aside className="hidden xl:block">
        <div className="sticky top-20">
          <Contents nav={nav} findings={view.findings} />
        </div>
      </aside>
    </div>
  );
}
