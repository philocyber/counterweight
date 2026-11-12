"use client";

import * as React from "react";

import Link from "next/link";
import { useRouter, useSearchParams } from "next/navigation";

import {
  ArchiveIcon,
  ArrowLeftIcon,
  BrainIcon,
  CheckIcon,
  CircleAlertIcon,
  ExternalLinkIcon,
  MessageSquareIcon,
  PauseIcon,
  PlayIcon,
  UsersIcon,
} from "lucide-react";
import { toast } from "sonner";

import { AssessmentTypeBadge } from "@/components/assessment-type-badge";
import { StatusBadge } from "@/components/status-badge";
import { TaskLLMProfileChain } from "@/components/task-llm-profile-chain";
import { TaskPauseReason } from "@/components/task-pause-reason";
import { Alert, AlertDescription, AlertTitle } from "@/components/ui/alert";
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
import { Button } from "@/components/ui/button";
import {
  Popover,
  PopoverContent,
  PopoverDescription,
  PopoverHeader,
  PopoverTitle,
  PopoverTrigger,
} from "@/components/ui/popover";
import { Separator } from "@/components/ui/separator";
import { Spinner } from "@/components/ui/spinner";
import { Tabs, TabsContent, TabsList, TabsTrigger } from "@/components/ui/tabs";
import { Tooltip, TooltipContent, TooltipTrigger } from "@/components/ui/tooltip";
import { pollWhileVisible } from "@/hooks/use-polling";
import { type MessageKey, useI18n } from "@/i18n";
import { api } from "@/lib/api";
import { approvalHold as findApprovalHold, platformHold } from "@/lib/task-holds";
import {
  assessmentTypeOf,
  type TaskTab,
  type TaskTabGroup,
  taskGroupTabs,
  taskTabGroup,
  validTaskTab,
  visibleTaskTabs,
} from "@/lib/task-workspace";
import type { CodeReviewMode, CodeTarget, LLMProfile, Task } from "@/lib/types";
import { cn } from "@/lib/utils";

import { AssetsTab } from "./_tabs/assets-tab";
import { AuthzTab } from "./_tabs/authz-tab";
import { BroadcastTab } from "./_tabs/broadcast-tab";
import { CoverageGraphTab } from "./_tabs/coverage-graph-tab";
import { FindingsTab } from "./_tabs/findings-tab";
import { GraphTab } from "./_tabs/graph-tab";
import { InterceptTab } from "./_tabs/intercept-tab";
import { JiraTaskExportButton, useJiraLinks } from "./_tabs/jira-export";
import { MitigationTab } from "./_tabs/mitigation-tab";
import { OverviewTab } from "./_tabs/overview-tab";
import { ReportTab } from "./_tabs/report-tab";
import { ResultsTab } from "./_tabs/results-tab";
import { RetestsTab } from "./_tabs/retests-tab";
import { ScanTab } from "./_tabs/scan-tab";
import { SessionsTab } from "./_tabs/sessions-tab";
import { TaskBudgetBanner, TaskBudgetIndicator } from "./_tabs/task-budget";
import { ExecutionBadge, ExecutionPausedAlert } from "./_tabs/task-execution";

// Labels for the tabs and groups defined in TASK_WORKSPACES / TASK_TAB_GROUPS.
const TAB_LABELS: Record<TaskTab, MessageKey> = {
  sessions: "interface.m0779",
  overview: "interface.m0147",
  checks: "taskdetail.resultsLedgerTab",
  graph: "interface.m0780",
  broadcast: "interface.m0781",
  findings: "interface.m0188",
  retests: "interface.m0332",
  mitigation: "interface.mitigationTab",
  scan: "scan.tabLabel",
  authz: "authz.tabLabel",
  assets: "interface.m0510",
  coverage: "interface.m0782",
  intercept: "interface.m0783",
  report: "interface.m0784",
};

const GROUP_LABELS: Record<TaskTabGroup, MessageKey> = {
  run: "taskdetail.groupRun",
  findings: "taskdetail.groupFindings",
  evidence: "taskdetail.groupEvidence",
  scope: "taskdetail.groupScope",
  approvals: "taskdetail.groupApprovals",
  report: "taskdetail.groupReport",
};

const SUBTABS_ID = "task-detail-subtabs";

// Horizontal scroll instead of wrapping; the bottom padding keeps the line-variant underline visible.
const TAB_ROW_SCROLL = "min-w-0 overflow-x-auto overflow-y-hidden pb-1 [scrollbar-width:thin]";

function taskProfileIDs(task: Task): string[] {
  if (task.llm_profile_ids && task.llm_profile_ids.length > 0) {
    return task.llm_profile_ids.map(String);
  }
  return task.llm_profile_id ? [String(task.llm_profile_id)] : [];
}

function TaskLLMControl({ task, profiles, onUpdated }: { task: Task; profiles: LLMProfile[]; onUpdated: () => void }) {
  "use no memo";
  const { t: swt, locale: swLocale } = useI18n();

  const [open, setOpen] = React.useState(false);
  const [profileIDs, setProfileIDs] = React.useState<string[]>(() => taskProfileIDs(task));
  const [activeProfileID, setActiveProfileID] = React.useState(
    task.active_llm_profile_id ? String(task.active_llm_profile_id) : (taskProfileIDs(task)[0] ?? ""),
  );
  const [saving, setSaving] = React.useState(false);
  const popoverContentRef = React.useRef<HTMLDivElement>(null);

  const chain = taskProfileIDs(task);
  const exhausted = task.llm_failover_state === "chain_exhausted";
  // Profile chains can change in any state, including final states. Main-Agent conversations still use them
  // after completion, so failed models must remain replaceable for continued interaction.
  const terminal = ["done", "failed", "timeout"].includes(task.status);
  // A null active profile on an exhausted, non-empty chain is a persisted end
  // cursor. Keep the status display honest; choosing the first profile is only
  // the editor's reset draft and does not mean it is currently active.
  let activeID = chain[0] ?? "";
  if (exhausted) activeID = "";
  if (task.active_llm_profile_id) activeID = String(task.active_llm_profile_id);
  const activeProfile = profiles.find((profile) => profile.id === activeID);
  const currentLabel = exhausted
    ? swt("interface.m0785")
    : (activeProfile?.name ?? (activeID ? swt("interface.m0786", { p0: activeID }) : swt("interface.m0787")));
  const activeIndex = chain.indexOf(activeID);
  const backupCount = activeIndex >= 0 ? Math.max(0, chain.length - activeIndex - 1) : 0;
  const currentTitle = [
    currentLabel,
    activeProfile?.model,
    backupCount > 0 ? swt("interface.m0788", { p0: backupCount }) : "",
  ]
    .filter(Boolean)
    .join(" · ");
  let editorDescription = swt("interface.m0789");
  if (terminal) editorDescription = swt("interface.m0790");
  let saveLabel = swt("interface.m0273");
  if (exhausted) saveLabel = swt("interface.m0791");
  if (saving) saveLabel = swt("interface.m0792");

  const syncDraft = React.useCallback(() => {
    const next = taskProfileIDs(task);
    setProfileIDs(next);
    setActiveProfileID(task.active_llm_profile_id ? String(task.active_llm_profile_id) : (next[0] ?? ""));
  }, [task]);

  const handleOpenChange = (next: boolean) => {
    setOpen(next);
    if (next) syncDraft();
  };

  const handleProfileIDsChange = (next: string[]) => {
    setProfileIDs(next);
    setActiveProfileID((current) => (next.includes(current) ? current : (next[0] ?? "")));
  };

  const save = async () => {
    setSaving(true);
    try {
      const result = await api.updateTaskLLMProfiles(
        task.id,
        profileIDs.map(Number),
        activeProfileID ? Number(activeProfileID) : undefined,
      );
      if (result.switch_event) {
        toast.info(result.switch_event.summary, { id: `task-${task.id}-llm-${result.switch_event.seq}` });
      } else {
        toast.success(
          result.reopened_intents > 0
            ? swt("interface.m0793", { p0: result.reopened_intents })
            : swt("interface.m0794"),
        );
      }
      setOpen(false);
      onUpdated();
    } catch (error) {
      toast.error(swt("interface.m0352") + (error as Error).message);
    } finally {
      setSaving(false);
    }
  };

  return (
    <Popover open={open} onOpenChange={handleOpenChange}>
      <PopoverTrigger asChild>
        <Button
          size="sm"
          variant={exhausted ? "destructive" : "outline"}
          aria-label={swt("interface.m0795")}
          title={currentTitle}
        >
          <BrainIcon data-icon="inline-start" />
          <span className="hidden max-w-36 truncate lg:inline">{currentLabel}</span>
          {backupCount > 0 && <span className="hidden text-muted-foreground xl:inline">+{backupCount}</span>}
        </Button>
      </PopoverTrigger>
      <PopoverContent ref={popoverContentRef} align="start" className="w-[min(28rem,calc(100vw-2rem))] gap-4 p-4">
        <PopoverHeader>
          <PopoverTitle>{swt("interface.m0796")}</PopoverTitle>
          <PopoverDescription>{editorDescription}</PopoverDescription>
        </PopoverHeader>

        {exhausted && (
          <Alert variant="destructive">
            <CircleAlertIcon />
            <AlertTitle>{swt("interface.m0797")}</AlertTitle>
            <AlertDescription>{task.llm_failover_reason ?? swt("interface.m0798")}</AlertDescription>
          </Alert>
        )}

        <TaskLLMProfileChain
          profiles={profiles}
          value={profileIDs}
          onValueChange={handleProfileIDsChange}
          activeProfileId={activeProfileID}
          onActiveProfileChange={setActiveProfileID}
          inputId="task-llm-profiles"
          disabled={saving}
          portalContainer={popoverContentRef}
        />

        <div className="flex justify-end gap-2">
          <Button type="button" variant="outline" size="sm" onClick={() => setOpen(false)}>
            {swt("interface.m0211")}
          </Button>
          <Button type="button" size="sm" onClick={save} disabled={saving}>
            {saving && <Spinner data-icon="inline-start" />}
            {saveLabel}
          </Button>
        </div>
      </PopoverContent>
    </Popover>
  );
}

// Long goals are clipped: two lines in a narrow header; beside the tabs in a wide one, as many as
// fit their height (`tall` says the header has its second row of tabs). The toggle appears only
// when text is clipped.
function TaskGoal({ goal, tall }: { goal: string; tall: boolean }) {
  const { t: swt } = useI18n();
  const ref = React.useRef<HTMLParagraphElement>(null);
  const [expanded, setExpanded] = React.useState(false);
  const [clipped, setClipped] = React.useState(false);
  // biome-ignore lint/correctness/useExhaustiveDependencies: re-measure when the goal text changes
  React.useLayoutEffect(() => {
    const el = ref.current;
    if (!el || expanded) return;
    const check = () => setClipped(el.scrollHeight > el.clientHeight + 1);
    check();
    const observer = new ResizeObserver(check);
    observer.observe(el);
    return () => observer.disconnect();
  }, [goal, expanded, tall]);
  if (!goal) return null;
  return (
    <div className="flex min-w-0 flex-col items-start gap-0.5">
      <p
        ref={ref}
        className={cn(
          "w-full whitespace-pre-line break-words text-muted-foreground text-xs",
          !expanded && (tall ? "line-clamp-2 @5xl:line-clamp-5" : "line-clamp-2 @5xl:line-clamp-3"),
        )}
      >
        {goal}
      </p>
      {(clipped || expanded) && (
        <button
          type="button"
          className="font-medium text-primary text-xs hover:underline"
          aria-expanded={expanded}
          onClick={() => setExpanded((value) => !value)}
        >
          {expanded ? swt("app.taskGoalShowLess") : swt("app.taskGoalShowMore")}
        </button>
      )}
    </div>
  );
}

const DEPTH_LABELS: Record<CodeReviewMode, MessageKey> = {
  light: "taskdetail.depthLight",
  balanced: "taskdetail.depthBalanced",
  comprehensive: "taskdetail.depthComprehensive",
};

// The facts of a code review: its repository, commit and depth. The description is shown beside them.
function CodeReviewSummary({ target }: { target: CodeTarget }) {
  const { t: swt } = useI18n();
  const mode = DEPTH_LABELS[target.mode as CodeReviewMode];
  return (
    <div className="flex min-w-0 flex-wrap items-center gap-x-3 gap-y-0.5 text-muted-foreground text-xs">
      {/^https:\/\/github\.com\//.test(target.url) ? (
        <a
          href={target.url}
          target="_blank"
          rel="noopener noreferrer"
          className="inline-flex min-w-0 max-w-full items-center gap-1 font-medium text-primary hover:underline"
          title={swt("taskdetail.openGithub")}
        >
          <span className="truncate">{target.repository}</span>
          <ExternalLinkIcon className="size-3 shrink-0" aria-hidden="true" />
          <span className="sr-only">{swt("taskdetail.openGithub")}</span>
        </a>
      ) : (
        <span className="truncate font-medium">{target.repository}</span>
      )}
      {target.commit_sha && (
        <span title={target.commit_sha}>
          {swt("taskdetail.commitLabel")} <code className="font-mono">{target.commit_sha.slice(0, 7)}</code>
        </span>
      )}
      {mode && (
        <span>
          {swt("taskdetail.depthLabel")}: {swt(mode)}
        </span>
      )}
    </div>
  );
}

function TaskDetailInner() {
  "use no memo";
  const { t: swt, rich: swRich, locale: swLocale } = useI18n();

  const router = useRouter();
  const searchParams = useSearchParams();
  const id = searchParams.get("id") ?? "";
  const jira = useJiraLinks(id);
  const [task, setTask] = React.useState<Task | null>(null);
  const [paused, setPaused] = React.useState(false);
  const [loaded, setLoaded] = React.useState(false);
  const [tab, setTab] = React.useState("sessions");
  const contentRef = React.useRef<HTMLDivElement>(null);
  React.useLayoutEffect(() => {
    contentRef.current?.scrollTo({ top: 0, left: 0 });
  }, [tab, id]);
  React.useEffect(() => {
    setTab(validTaskTab(searchParams.get("tab")));
  }, [searchParams]);
  function changeTab(value: string) {
    setTab(value);
    const url = new URL(window.location.href);
    url.searchParams.set("tab", value);
    window.history.replaceState(null, "", url);
  }
  // Remembers the last tab opened in each group so returning to a group restores it.
  const lastTabByGroup = React.useRef<Partial<Record<TaskTabGroup, string>>>({});
  const activeGroup = taskTabGroup(tab);
  // Tabs that mean nothing for this assessment type are not offered, but one opened by URL stays reachable.
  // A task without a recorded type (older backend or legacy task) keeps the Mitigation tab.
  const typeKnown = !!task?.assessment_type && task.assessment_type_explicit !== false;
  const nav = visibleTaskTabs(typeKnown ? assessmentTypeOf(task) : undefined, validTaskTab(tab));
  const groupTabs = (group: TaskTabGroup) => taskGroupTabs(group).filter((value) => nav.tabs.includes(value));
  const activeGroupTabs = groupTabs(activeGroup);
  React.useEffect(() => {
    lastTabByGroup.current[taskTabGroup(tab)] = tab;
  }, [tab]);
  function changeGroup(value: string) {
    const group = value as TaskTabGroup;
    changeTab(lastTabByGroup.current[group] ?? groupTabs(group)[0]);
  }
  const [interceptPendingCount, setInterceptPendingCount] = React.useState(0);
  const [profiles, setProfiles] = React.useState<LLMProfile[]>([]);
  const [archiving, setArchiving] = React.useState(false);

  React.useEffect(() => {
    api
      .llmProfiles()
      .then(setProfiles)
      .catch(() => setProfiles([]));
  }, []);

  React.useEffect(() => {
    let alive = true;
    const load = () =>
      api
        .interceptTask(id)
        .then((rows) => {
          if (alive) setInterceptPendingCount(rows.filter((r) => r.status === "pending").length);
        })
        .catch(() => {
          // Polling is best-effort; the next interval retries automatically.
        });
    void load();
    const t = pollWhileVisible(load, 5000);
    return () => {
      alive = false;
      t();
    };
  }, [id]);

  const taskLoadInFlight = React.useRef<string | null>(null);
  const load = React.useCallback(() => {
    if (taskLoadInFlight.current === id) return;
    taskLoadInFlight.current = id;
    Promise.all([api.task(id), api.stats(id).catch(() => null)])
      .then(([task, s]) => {
        if (taskLoadInFlight.current !== id) return;
        const base = { ...task };
        const at = s?.active_task;
        if (at) {
          base.in_flight = at.in_flight;
          base.goals_total = at.goals_total;
          base.goals_met = at.goals_met;
          base.engine_mode = s?.engine_mode ?? at.engine_mode;
          base.paused = at.paused;
        }
        setTask(base);
        // Budget and isolation pauses are not approval holds: they have their own banners and the task resumes normally.
        const approvalActive = !!findApprovalHold(base);
        setPaused(!approvalActive && (at?.paused ?? base.paused ?? false));
      })
      .catch(() => {
        // Keep the last rendered task state during a transient poll failure.
      })
      .finally(() => {
        if (taskLoadInFlight.current === id) {
          taskLoadInFlight.current = null;
          setLoaded(true);
        }
      });
  }, [id]);
  React.useEffect(() => {
    load();
    const timer = pollWhileVisible(load, 5000);
    return () => timer();
  }, [load]);

  async function togglePause() {
    if (task && ["done", "failed", "timeout"].includes(task.status)) return;
    const next = !paused;
    try {
      await api.controlTask(id, next ? "pause" : "resume");
      setPaused(next);
      load();
      toast.success(next ? swt("interface.m0799") : swt("interface.m0800"));
    } catch (e) {
      toast.error(swt("interface.m0801") + (e as Error).message);
    }
  }

  async function continueAfterApproval() {
    try {
      await api.controlTask(id, "resume");
      setPaused(false);
      load();
      toast.success(swt("app.resumeScanAction"));
    } catch (e) {
      toast.error(swt("interface.m0801") + (e as Error).message);
    }
  }

  async function archiveTask() {
    if (!task || archiving) return;
    setArchiving(true);
    try {
      await api.archiveTask(task.id);
      toast.success(swt("interface.m0802"));
      router.push("/function/tasks");
    } catch (error) {
      toast.error(swt("interface.m0803", { p0: (error as Error).message }));
      setArchiving(false);
    }
  }

  if (!task) {
    return (
      <div className="flex flex-1 flex-col items-center justify-center gap-3 p-10 text-center">
        <p className="text-muted-foreground">{loaded ? swt("interface.m0804", { p0: id }) : swt("interface.m0260")}</p>
        {loaded && (
          <Button asChild variant="outline">
            <Link href="/function/tasks">
              <ArrowLeftIcon /> {swt("interface.m0805")}
            </Link>
          </Button>
        )}
      </div>
    );
  }

  const platform = platformHold(task);
  const budgetHold = platform?.kind === "budget" ? platform : undefined;
  const executionHold = platform?.kind === "execution" ? platform : undefined;
  const approvalHold = findApprovalHold(task);
  const approvalRequests = task.approval_state?.pending ?? [];
  const waitingPermission = !!approvalHold && approvalRequests.length > 0;
  const completed = task.status === "done";
  const terminal = ["done", "failed", "timeout"].includes(task.status);
  const archiveLifecycleEligible = terminal || paused || task.status === "paused";
  const canArchive = archiveLifecycleEligible && !task.archive_blocked_by_task_id;
  let archiveDisabledReason = task.queued ? swt("interface.m0806") : swt("interface.m0807");
  if (archiveLifecycleEligible && task.archive_blocked_by_task_id) {
    archiveDisabledReason = swt("interface.m0808", { p0: task.archive_blocked_by_task_id });
  }
  const engineMode = paused ? "paused" : (task.engine_mode ?? "idle");
  let controlVariant: "default" | "secondary" | "outline" = "outline";
  let controlIcon = <PauseIcon data-icon="inline-start" />;
  let controlLabel = swt("interface.m0151");
  if (terminal) {
    controlVariant = "secondary";
    controlIcon = <CheckIcon data-icon="inline-start" />;
    controlLabel = completed ? swt("interface.m0809") : swt("interface.m0810");
  } else if (paused) {
    controlVariant = "default";
    controlIcon = <PlayIcon data-icon="inline-start" />;
    controlLabel = swt("interface.m0811");
  }
  const archiveTrigger = (
    <Button
      size="icon-sm"
      variant="ghost"
      disabled={!canArchive || archiving}
      aria-label={canArchive ? swt("interface.m0812") : archiveDisabledReason}
    >
      {archiving ? <Spinner /> : <ArchiveIcon />}
    </Button>
  );

  return (
    <Tabs
      value={tab}
      onValueChange={changeTab}
      className="flex h-[calc(100dvh-var(--header-height))] min-h-0 min-w-0 flex-none flex-col gap-0 overflow-hidden"
    >
      {/* Top fixed area */}
      <header className="@container z-10 flex shrink-0 flex-col border-b bg-card">
        {/* The title bar: what the task is and what can be done with it. It stays on one line; the action
            cluster wraps below the identity under lg. A tint and a rule set it apart from what follows. */}
        <div className="flex flex-wrap items-center gap-2 border-b bg-muted/50 px-4 py-2.5 lg:flex-nowrap lg:px-6">
          <div className="flex min-w-0 basis-full items-center gap-2 lg:flex-1 lg:basis-0">
            <Button asChild variant="ghost" size="icon" className="-ml-1 size-7 shrink-0">
              <Link href="/function/tasks" aria-label={swt("interface.m0805")}>
                <ArrowLeftIcon />
              </Link>
            </Button>
            <h1 className="min-w-0 truncate font-semibold text-sm" title={task.name || task.description}>
              {task.name || task.description}
            </h1>
            <code className="shrink-0 rounded bg-muted px-1.5 py-0.5 font-mono text-muted-foreground text-xs">
              {task.id}
            </code>
            <span className="inline-flex shrink-0">
              <StatusBadge
                domain={terminal || approvalHold ? "task" : "engine"}
                value={approvalHold ? "waiting_approval" : terminal ? task.status : engineMode}
                dot
              />
            </span>
            <AssessmentTypeBadge type={task.assessment_type} />
            <ExecutionBadge execution={task.execution} status={task.status} />
            <TaskPauseReason task={task} />
          </div>
          <div className="flex flex-wrap items-center gap-2 lg:shrink-0 lg:flex-nowrap">
            <Separator orientation="vertical" className="mx-1 hidden h-4 lg:block" />
            <TaskLLMControl task={task} profiles={profiles} onUpdated={load} />
            <JiraTaskExportButton taskId={id} links={jira.links} reload={jira.reload} />
            {/* The test accounts and the authorization check live two levels down; this leads straight to them. */}
            {nav.tabs.includes("authz") && (
              <Button size="sm" variant="outline" onClick={() => changeTab("authz")}>
                <UsersIcon /> {swt("taskdetail.testAccounts")}
              </Button>
            )}
            <Button size="sm" variant="outline" asChild>
              <Link
                href={`/chat/?task=${encodeURIComponent(id)}${/^[1-9]\d*$/.test(searchParams.get("chat") ?? "") ? `&c=${searchParams.get("chat")}` : ""}`}
              >
                <MessageSquareIcon /> {swt("taskdetail.openChat")}
              </Link>
            </Button>
            {canArchive ? (
              <AlertDialog>
                <AlertDialogTrigger asChild>{archiveTrigger}</AlertDialogTrigger>
                <AlertDialogContent>
                  <AlertDialogHeader>
                    <AlertDialogTitle>
                      {swt("interface.m0813")}
                      {task.id}?
                    </AlertDialogTitle>
                    <AlertDialogDescription>{swt("interface.m0814")}</AlertDialogDescription>
                  </AlertDialogHeader>
                  <AlertDialogFooter>
                    <AlertDialogCancel>{swt("interface.m0063")}</AlertDialogCancel>
                    <AlertDialogAction onClick={() => void archiveTask()}>{swt("interface.m0815")}</AlertDialogAction>
                  </AlertDialogFooter>
                </AlertDialogContent>
              </AlertDialog>
            ) : (
              <Tooltip>
                <TooltipTrigger asChild>
                  <span className="inline-flex">{archiveTrigger}</span>
                </TooltipTrigger>
                <TooltipContent>{archiveDisabledReason}</TooltipContent>
              </Tooltip>
            )}
            <Button size="sm" variant={controlVariant} onClick={togglePause} disabled={terminal || !!approvalHold}>
              {controlIcon}
              {controlLabel}
            </Button>
          </div>
        </div>
        {/* Below the title bar: notices, then the facts, the tabs and the description. */}
        <div className="flex min-w-0 flex-col gap-2 px-4 pt-3 pb-3 lg:px-6">
          <TaskBudgetBanner taskId={id} budget={task.budget} hold={budgetHold} onChanged={load} />
          {executionHold && <ExecutionPausedAlert taskId={id} reason={executionHold.reason} onResumed={load} />}
          {approvalHold && (
            <Alert className="border-amber-500/40 bg-amber-500/10" role="alert">
              <CircleAlertIcon />
              <AlertTitle>{waitingPermission ? swt("app.toolPermissionTitle") : swt("app.resumeScanTitle")}</AlertTitle>
              <AlertDescription className="space-y-2">
                {waitingPermission ? (
                  <>
                    <p>{swt("app.toolPermissionStopped")}</p>
                    {approvalRequests.slice(0, 3).map((request) => (
                      <p key={request.id}>
                        {swRich(
                          "taskdetail.toolRequestedBy",
                          {
                            p0: request.tool,
                            p1: request.agent || swt("taskdetail.taskAgent"),
                            p2: request.id,
                            p3: request.reason,
                          },
                          { strong: (chunk) => <strong>{chunk}</strong> },
                        )}
                      </p>
                    ))}
                    {approvalRequests.length > 3 && (
                      <p>{swt("app.toolPermissionMore", { count: approvalRequests.length - 3 })}</p>
                    )}
                    <p>{swt("app.toolPermissionReview")}</p>
                    <Button size="sm" variant="outline" onClick={() => changeTab("intercept")}>
                      {swt("app.reviewToolPermissions")}
                    </Button>
                  </>
                ) : (
                  <>
                    <p>{swt("app.resumeScanBody")}</p>
                    <div className="flex flex-wrap gap-2">
                      <Button size="sm" onClick={() => void continueAfterApproval()}>
                        <PlayIcon data-icon="inline-start" />
                        {swt("app.resumeScanAction")}
                      </Button>
                      <Button size="sm" variant="outline" onClick={() => changeTab("intercept")}>
                        {swt("app.reviewToolPermissions")}
                      </Button>
                    </div>
                  </>
                )}
              </AlertDescription>
            </Alert>
          )}
          {/* Facts and tabs on the left; in a wide header the description fills the space beside them. */}
          <div className="grid min-w-0 grid-cols-1 gap-2 @5xl:grid-cols-[minmax(0,max-content)_minmax(20rem,1fr)] @5xl:gap-x-8">
            <div className="flex min-w-0 flex-wrap items-center gap-x-4 gap-y-1 @5xl:col-start-1">
              {assessmentTypeOf(task) === "code_review" && task.code_target && (
                <CodeReviewSummary target={task.code_target} />
              )}
              <TaskBudgetIndicator taskId={id} budget={task.budget} onChanged={load} />
            </div>
            {task.goal && (
              // Beside the tabs the description is centred in their height, so a short one does not hang
              // from the top edge.
              <div className="flex min-w-0 flex-col justify-center @5xl:col-start-2 @5xl:row-span-2 @5xl:row-start-1 @5xl:border-l @5xl:pl-6">
                <TaskGoal goal={task.goal} tall={activeGroupTabs.length > 1} />
              </div>
            )}
            <div className="flex min-w-0 flex-col gap-2 @5xl:col-start-1">
              {/* Primary row: tab groups. Its own Tabs root selects a group; the outer root still owns the tab value. */}
              <Tabs value={activeGroup} onValueChange={changeGroup} className="min-w-0 gap-0">
                <div className={TAB_ROW_SCROLL}>
                  <TabsList
                    variant="line"
                    aria-label={swt("taskdetail.sectionsLabel")}
                    className="h-auto w-max min-w-full justify-start gap-3 group-data-horizontal/tabs:h-auto"
                  >
                    {nav.groups.map((group) => (
                      <TabsTrigger
                        key={group}
                        value={group}
                        className="h-8 flex-none"
                        aria-controls={groupTabs(group).length > 1 && group === activeGroup ? SUBTABS_ID : undefined}
                      >
                        {swt(GROUP_LABELS[group])}
                        {group === "approvals" && interceptPendingCount > 0 && (
                          <span className="ml-1.5 inline-flex h-4 min-w-[16px] items-center justify-center rounded-full bg-amber-500 px-1 font-semibold text-[10px] text-white leading-none">
                            {interceptPendingCount > 99 ? "99+" : interceptPendingCount}
                          </span>
                        )}
                      </TabsTrigger>
                    ))}
                  </TabsList>
                </div>
              </Tabs>
              {/* Secondary row: the active group's tabs, only when it has more than one. */}
              {activeGroupTabs.length > 1 && (
                <div className={TAB_ROW_SCROLL}>
                  <TabsList id={SUBTABS_ID} aria-label={swt(GROUP_LABELS[activeGroup])} className="w-max justify-start">
                    {activeGroupTabs.map((value) => (
                      <TabsTrigger key={value} value={value} className="flex-none px-2.5 text-xs">
                        {swt(TAB_LABELS[value])}
                      </TabsTrigger>
                    ))}
                  </TabsList>
                </div>
              )}
            </div>
          </div>
        </div>
      </header>

      {/* Tab content */}
      <div key={tab} ref={contentRef} className="min-h-0 min-w-0 flex-1 overflow-y-auto p-4 lg:p-6">
        <TabsContent value="sessions" className="mt-0">
          <SessionsTab taskId={id} task={task} />
        </TabsContent>
        <TabsContent value="overview" className="mt-0">
          <OverviewTab taskId={id} />
        </TabsContent>
        <TabsContent value="checks" className="mt-0">
          <ResultsTab key={id} taskId={id} />
        </TabsContent>
        <TabsContent value="graph" className="mt-0">
          <GraphTab taskId={id} />
        </TabsContent>
        <TabsContent value="broadcast" className="mt-0">
          <BroadcastTab taskId={id} />
        </TabsContent>
        <TabsContent value="findings" className="mt-0">
          <FindingsTab taskId={id} jira={jira} />
        </TabsContent>
        <TabsContent value="retests" className="mt-0">
          <RetestsTab key={id} taskId={id} />
        </TabsContent>
        <TabsContent value="mitigation" className="mt-0">
          <MitigationTab key={id} taskId={id} />
        </TabsContent>
        <TabsContent value="scan" className="mt-0">
          <ScanTab key={id} taskId={id} task={task} />
        </TabsContent>
        <TabsContent value="authz" className="mt-0">
          <AuthzTab key={id} taskId={id} />
        </TabsContent>
        <TabsContent value="assets" className="mt-0">
          <AssetsTab taskId={id} />
        </TabsContent>
        <TabsContent value="coverage" className="mt-0">
          <CoverageGraphTab taskId={id} coverageEnabled={task?.coverage_enabled !== false} />
        </TabsContent>
        <TabsContent value="intercept" className="mt-0">
          <InterceptTab taskId={id} />
        </TabsContent>
        <TabsContent value="report" className="mt-0">
          <ReportTab taskId={id} jira={jira} execution={task.execution} status={task.status} />
        </TabsContent>
      </div>
    </Tabs>
  );
}

// useSearchParams must sit under a Suspense boundary for static export.
export default function TaskDetailPage() {
  "use no memo";
  const { t: swt, locale: swLocale } = useI18n();

  return (
    <React.Suspense fallback={null}>
      <TaskDetailInner />
    </React.Suspense>
  );
}
