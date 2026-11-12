"use client";

import * as React from "react";

import Link from "next/link";

import { ArrowUpRightIcon, RefreshCwIcon } from "lucide-react";

import { StatusBadge } from "@/components/status-badge";
import { Button, buttonVariants } from "@/components/ui/button";
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "@/components/ui/select";
import { type MessageKey, useI18n } from "@/i18n";
import { api } from "@/lib/api";
import { getLocalStorageValue, setLocalStorageValue } from "@/lib/local-storage.client";
import { TASK_WORKSPACES, taskWorkspaceHref } from "@/lib/task-workspace";
import type { AssetInterceptRule, Task, TaskConstraint, TaskScopeRow } from "@/lib/types";
import { cn } from "@/lib/utils";

type Snapshot = {
  task: Task | null;
  scope: TaskScopeRow[] | null;
  constraints: TaskConstraint[] | null;
  rules: AssetInterceptRule[] | null;
  coverage: Awaited<ReturnType<typeof api.taskCoverage>> | null;
  approvals: Awaited<ReturnType<typeof api.interceptTask>> | null;
  findingTotal: number | null;
  unavailable: MessageKey[];
  at: Date;
};

// Snapshot request order; a rejected request is listed by its label.
const SNAPSHOT_PARTS: MessageKey[] = [
  "taskdetail.contextPartTask",
  "taskdetail.contextPartScope",
  "taskdetail.contextPartConstraints",
  "taskdetail.contextPartAssetRules",
  "taskdetail.contextPartCoverage",
  "taskdetail.contextPartApprovals",
  "taskdetail.contextPartFindings",
];

export function TaskContextPanel({
  conversationId,
  createdTaskId,
}: {
  conversationId: number | null;
  createdTaskId?: string | null;
}) {
  const { t: swt } = useI18n();
  const storageKey = `counterweight.task-context.${conversationId ?? "draft"}`;
  const [tasks, setTasks] = React.useState<Task[]>([]);
  const [taskId, setTaskId] = React.useState("");
  const [listError, setListError] = React.useState(false);
  const [listLoaded, setListLoaded] = React.useState(false);
  const [snapshot, setSnapshot] = React.useState<Snapshot | null>(null);
  const [loading, setLoading] = React.useState(false);
  const [revision, refresh] = React.useReducer((n: number) => n + 1, 0);

  React.useEffect(() => {
    const requested = new URL(window.location.href).searchParams.get("task");
    setTaskId((requested && /^[1-9]\d*$/.test(requested) ? requested : getLocalStorageValue(storageKey)) || "");
  }, [storageKey]);

  const chooseTask = (id: string) => {
    setSnapshot(null);
    setLoading(!!id);
    setTaskId(id);
    setLocalStorageValue(storageKey, id);
    // A URL hint is consumed by the explicit selection; it must not override a
    // different conversation's stored context when switching chat history.
    const url = new URL(window.location.href);
    url.searchParams.delete("task");
    window.history.replaceState(null, "", url);
  };

  React.useEffect(() => {
    let live = true;
    let timer: ReturnType<typeof setTimeout>;
    async function load() {
      try {
        const result = await api.tasks();
        if (live) {
          setTasks(result.tasks);
          setListError(false);
          setListLoaded(true);
        }
      } catch {
        if (live) {
          setListError(true);
          setListLoaded(true);
        }
      } finally {
        if (live) timer = setTimeout(load, 10000);
      }
    }
    void load();
    return () => {
      live = false;
      clearTimeout(timer);
    };
  }, [revision]);

  React.useEffect(() => {
    if (!taskId) {
      setSnapshot(null);
      setLoading(false);
      return;
    }
    let live = true;
    let timer: ReturnType<typeof setTimeout>;
    setSnapshot(null);
    setLoading(true);
    async function load() {
      const results = await Promise.allSettled([
        api.task(taskId),
        api.taskScope(taskId),
        api.taskConstraints(taskId),
        api.taskInterceptRules(taskId),
        api.taskCoverage(taskId),
        api.interceptTask(taskId),
        api.findingsPage({ task: taskId, page: 1, pageSize: 1 }),
      ] as const);
      if (!live) return;
      const [task, scope, constraints, rules, coverage, approvals, registeredFindings] = results;
      setSnapshot({
        task: task.status === "fulfilled" ? task.value : null,
        scope: scope.status === "fulfilled" ? (scope.value.scope ?? []) : null,
        constraints: constraints.status === "fulfilled" ? constraints.value.constraints : null,
        rules: rules.status === "fulfilled" ? rules.value : null,
        coverage: coverage.status === "fulfilled" ? coverage.value : null,
        approvals: approvals.status === "fulfilled" ? approvals.value : null,
        findingTotal: registeredFindings.status === "fulfilled" ? registeredFindings.value.total : null,
        unavailable: results.flatMap((result, index) => (result.status === "rejected" ? [SNAPSHOT_PARTS[index]] : [])),
        at: new Date(),
      });
      setLoading(false);
      timer = setTimeout(load, 10000);
    }
    void load();
    return () => {
      live = false;
      clearTimeout(timer);
    };
  }, [taskId, revision]);

  const task = snapshot?.task;
  const pending = snapshot?.approvals?.filter((item) => item.status === "pending").length;
  const totalFindings = snapshot?.findingTotal;
  const taskLink = (id: string, tab?: string) =>
    taskWorkspaceHref(id, tab) + (conversationId ? `&chat=${conversationId}` : "");

  return (
    <aside
      aria-label={swt("taskdetail.contextAria")}
      className="console-task-context flex min-h-0 min-w-0 flex-col overflow-hidden rounded-lg border bg-card"
    >
      <div className="flex items-center justify-between border-b px-4 py-3">
        <div>
          <h2 className="text-sm font-semibold">{swt("taskdetail.contextTitle")}</h2>
          <p className="mt-0.5 text-xs text-muted-foreground">{swt("taskdetail.contextSubtitle")}</p>
        </div>
        <Button variant="ghost" size="icon-sm" aria-label={swt("taskdetail.contextRefreshAria")} onClick={refresh}>
          <RefreshCwIcon className="size-3.5" />
        </Button>
      </div>
      <div className="min-h-0 flex-1 space-y-5 overflow-y-auto p-4">
        <div className="space-y-2">
          <label htmlFor="chat-task-context" className="text-xs font-medium">
            {swt("taskdetail.contextTaskToInspect")}
          </label>
          <Select value={taskId || "none"} onValueChange={(value) => chooseTask(value === "none" ? "" : value)}>
            <SelectTrigger id="chat-task-context" className="w-full min-w-0">
              <SelectValue placeholder={swt("taskdetail.contextSelectTask")} />
            </SelectTrigger>
            <SelectContent>
              <SelectItem value="none">{swt("taskdetail.contextNoTaskSelected")}</SelectItem>
              {taskId && !tasks.some((item) => item.id === taskId) && (
                <SelectItem value={taskId}>{swt("taskdetail.contextTaskFallback", { p0: taskId })}</SelectItem>
              )}
              {tasks.map((item) => (
                <SelectItem key={item.id} value={item.id}>
                  #{item.id} · {item.name || item.description}
                </SelectItem>
              ))}
            </SelectContent>
          </Select>
          <p className="text-xs leading-relaxed text-muted-foreground">{swt("taskdetail.contextViewingNote")}</p>
          {listError && (
            <p role="status" className="text-xs text-destructive">
              {swt("taskdetail.contextListUnavailable")}
            </p>
          )}
        </div>
        {createdTaskId && createdTaskId !== taskId && (
          <div className="rounded-md border bg-accent/40 p-3 text-xs">
            <p>{swt("taskdetail.contextAutoCreated", { p0: createdTaskId })}</p>
            <Button size="sm" variant="outline" className="mt-2" onClick={() => chooseTask(createdTaskId)}>
              {swt("taskdetail.contextInspectCreated")}
            </Button>
          </div>
        )}
        {!taskId && (
          <div className="space-y-3 py-3">
            <h3 className="text-sm font-medium">{swt("taskdetail.contextStartTitle")}</h3>
            <p className="text-sm leading-relaxed text-muted-foreground">{swt("taskdetail.contextStartBody")}</p>
            {listLoaded && tasks.length === 0 && !listError && (
              <p className="text-xs text-muted-foreground">{swt("taskdetail.contextNoTasks")}</p>
            )}
            <Link className={cn(buttonVariants({ variant: "outline", size: "sm" }), "w-full")} href="/function/tasks/">
              {swt("taskdetail.contextOpenTasks")} <ArrowUpRightIcon />
            </Link>
          </div>
        )}
        {loading && (
          <div role="status" className="space-y-3">
            <div className="h-5 w-2/3 animate-pulse rounded bg-muted" />
            <div className="h-20 animate-pulse rounded bg-muted" />
            <p className="text-xs text-muted-foreground">{swt("taskdetail.contextLoading")}</p>
          </div>
        )}
        {snapshot?.unavailable.length ? (
          <p role="status" className="rounded border border-destructive/30 p-2 text-xs text-destructive">
            {swt("taskdetail.contextUnavailable", { p0: snapshot.unavailable.map((part) => swt(part)).join(", ") })}
          </p>
        ) : null}
        {task && (
          <>
            <section className="space-y-2">
              <div className="flex flex-wrap items-center gap-2">
                <code className="text-xs text-muted-foreground">TASK-{task.id}</code>
                <StatusBadge domain="task" value={task.status} dot />
              </div>
              <h3 className="break-words text-base font-semibold">{task.name || task.description}</h3>
              <p className="whitespace-pre-wrap break-words text-xs leading-relaxed text-muted-foreground">
                {task.goal || swt("taskdetail.contextNoObjective")}
              </p>
              <Link href={taskLink(task.id)} className={cn(buttonVariants({ size: "sm" }), "w-full")}>
                {swt("taskdetail.contextOpenFullTask")} <ArrowUpRightIcon />
              </Link>
            </section>
            <div className="grid grid-cols-2 divide-x rounded-md border">
              <Link href={taskLink(task.id, "findings")} className="p-3 hover:bg-accent">
                <span className="block text-xs text-muted-foreground">{swt("taskdetail.contextFindings")}</span>
                <strong className="mt-1 block text-lg tabular-nums">{totalFindings ?? "—"}</strong>
              </Link>
              <Link href={taskLink(task.id, "intercept")} className="p-3 hover:bg-accent">
                <span className="block text-xs text-muted-foreground">{swt("taskdetail.contextPendingApprovals")}</span>
                <strong className="mt-1 block text-lg tabular-nums">{pending ?? "—"}</strong>
              </Link>
            </div>
            <section className="space-y-2">
              <h3 className="text-xs font-semibold">{swt("taskdetail.contextScopeTitle")}</h3>
              {snapshot.scope === null ? (
                <p className="text-xs text-muted-foreground">{swt("taskdetail.contextScopeUnavailable")}</p>
              ) : snapshot.scope.length ? (
                <ul className="space-y-2">
                  {snapshot.scope.slice(0, 5).map((row) => (
                    <li key={row.id} className="rounded border px-2 py-1.5">
                      <span className="break-all font-mono text-xs">
                        {row.company_name || row.domain || row.net || row.value || row.kind}
                      </span>
                      <span className="mt-0.5 block text-[11px] text-muted-foreground">
                        {row.operator_authorized
                          ? swt("taskdetail.contextOperatorAuthorized")
                          : swt("taskdetail.contextScopeSource", { p0: row.source })}
                      </span>
                    </li>
                  ))}
                  {snapshot.scope.length > 5 && (
                    <li className="text-xs text-muted-foreground">
                      {swt("taskdetail.contextMoreInOverview", { p0: snapshot.scope.length - 5 })}
                    </li>
                  )}
                </ul>
              ) : (
                <p className="text-xs text-muted-foreground">{swt("taskdetail.contextNoScopeRows")}</p>
              )}
            </section>
            <details className="rounded-md border p-3">
              <summary className="cursor-pointer text-xs font-medium">
                {swt("taskdetail.contextConstraintsSummary")}
              </summary>
              <div className="mt-3 space-y-2 text-xs">
                {snapshot.constraints?.map((constraint) => (
                  <p key={constraint.id} className="break-words">
                    <strong>
                      {constraint.kind === "deny" ? swt("taskdetail.contextProhibit") : swt("taskdetail.contextPermit")}
                      :
                    </strong>{" "}
                    {constraint.text}
                  </p>
                ))}
                {snapshot.constraints?.length === 0 && (
                  <p className="text-muted-foreground">{swt("taskdetail.contextNoConstraints")}</p>
                )}
                <p>
                  {snapshot.rules === null
                    ? swt("taskdetail.contextAssetRulesUnavailable")
                    : swt("taskdetail.contextEnabledRules", {
                        p0: snapshot.rules.filter((rule) => rule.enabled).length,
                      })}
                </p>
                <p className="leading-relaxed text-muted-foreground">{swt("taskdetail.contextLowImpactNote")}</p>
                <Link href={taskLink(task.id, "overview")} className="text-primary underline underline-offset-4">
                  {swt("taskdetail.contextReviewScope")}
                </Link>
              </div>
            </details>
            {snapshot.coverage?.enabled && (
              <section className="space-y-1">
                <h3 className="text-xs font-semibold">{swt("taskdetail.contextCoverageTitle")}</h3>
                <p className="text-sm tabular-nums">
                  {swt("taskdetail.contextCoverageCount", {
                    p0: snapshot.coverage.tested,
                    p1: snapshot.coverage.denominator,
                  })}
                </p>
                <p className="text-xs leading-relaxed text-muted-foreground">{swt("taskdetail.contextCoverageNote")}</p>
              </section>
            )}
            <section>
              <h3 className="mb-2 text-xs font-semibold">{swt("taskdetail.contextWorkspaces")}</h3>
              <div className="grid grid-cols-2 gap-1.5">
                {TASK_WORKSPACES.map((tab) => (
                  <Link
                    key={tab.value}
                    href={taskLink(task.id, tab.value)}
                    title={tab.description}
                    className="rounded-md border px-2 py-2 text-xs leading-snug transition-colors hover:bg-accent focus-visible:outline-2 focus-visible:outline-ring"
                  >
                    {tab.label}
                  </Link>
                ))}
              </div>
            </section>
            <p className="text-[11px] text-muted-foreground">
              {swt("taskdetail.contextUpdated", {
                p0: snapshot.at.toLocaleTimeString([], { hour: "2-digit", minute: "2-digit" }),
              })}
            </p>
          </>
        )}
      </div>
    </aside>
  );
}
