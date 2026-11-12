import { localizedFetch as fetch } from "@/i18n/request";
import { localizedUrl, translate as swt } from "@/i18n/runtime";

import type { ChatGPTAccount, ChatGPTLogin, ChatGPTModel } from "./types";

// Real backend client. /api/* is proxied to the Go backend (next.config rewrites).
// Returns the domain types in lib/types.ts. Shapes match the backend handlers;
// a few fields the backend serializes differently (e.g. created_at as a unix int)
// are passed through and formatted at the call site.

// Real backend client. /api/* is proxied to the Go backend (next.config rewrites).
// Returns the domain types in lib/types.ts. Shapes match the backend handlers;
// a few fields the backend serializes differently (e.g. created_at as a unix int)
// are passed through and formatted at the call site.

import type { ChatMention } from "@/lib/chat-mentions";
import { ManagementTransportError, managementTransportAllowed } from "@/lib/management-transport";
import { MOCK } from "@/lib/mock/enabled";
import { seg, segPath } from "@/lib/path-segment";
import type {
  ActiveFindingRetest,
  Activity,
  Agent,
  AgentDetail,
  AgentTrigger,
  ApiToken,
  ArchiveBatchItem,
  AssessmentType,
  Asset,
  AssetInterceptRule,
  AssetInterceptRuleInput,
  Audit,
  AuthzAccount,
  AuthzDocument,
  AuthzPolicyCheck,
  AuthzRun,
  BatchCategoryItem,
  BatchControlItem,
  ChatAttachment,
  CodeReviewMode,
  CommandRecord,
  Company,
  CompanyScopeMutation,
  CompanyScopeRule,
  Conversation,
  ConversationFindingRun,
  ConvTokenSummary,
  CoverageAssetRefs,
  CoverageGraphData,
  DailyTokenBucket,
  DeleteTaskOptions,
  DeleteTaskResult,
  Edge,
  EvidenceBodyPreview,
  ExecutionStatus,
  ExplorationNodePage,
  ExplorationNodeQuery,
  Finding,
  FindingAssetTree,
  FindingDeepenResponse,
  FindingGroupsPage,
  FindingMitigation,
  FindingQuery,
  FindingRetest,
  FindingStats,
  FindingStatus,
  FindingsPage,
  FindingTraffic,
  FindingTrafficDetail,
  GitHubTokenStatus,
  IntentAsset,
  InterceptApprovalFilter,
  InterceptApprovalRow,
  InterceptDetail,
  InterceptPending,
  InterceptRule,
  JiraConfig,
  JiraLink,
  JiraProject,
  JiraProjectMeta,
  JiraStatus,
  JiraTarget,
  JiraTaskExport,
  JiraTaskLinks,
  JudgeConfig,
  LLMPoolStatus,
  LLMProfile,
  LLMRecordDetail,
  LLMRecordItem,
  LLMRetryOverride,
  LLMRetryPolicy,
  LLMTask,
  MCPServer,
  MCPTool,
  MissingSkill,
  ModelTokenStat,
  NotificationChannel,
  NotificationDelivery,
  NotificationFilter,
  NotificationMeta,
  PromptVar,
  PromptVersion,
  ReconToolStatus,
  ReportView,
  ReviewChangeInput,
  ScanSettings,
  SessionTokenUsage,
  Settings,
  Severity,
  SkillCall,
  SkillItem,
  SSProject,
  SSTask,
  StarPrompt,
  Stats,
  Task,
  TaskArchive,
  TaskArchivePage,
  TaskAssetMutation,
  TaskAssetScopeMutation,
  TaskAuthz,
  TaskAutomation,
  TaskCategory,
  TaskConstraint,
  TaskGoal,
  TaskLLMResolutions,
  TaskMitigations,
  TaskNode,
  TaskScan,
  TaskScopeRow,
  TaskTemplate,
  TaskToolGrant,
  TokenTotal,
  TokenUsage,
  Tool,
  ToolStat,
  TrafficDetail,
  TrafficEvidenceRef,
  TrafficEvidenceRole,
  TrafficHost,
  TrafficResp,
  UpdateCheck,
  UsageStats,
  WorkspaceFile,
  WorkspaceListing,
} from "@/lib/types";

function getToken(): string | null {
  if (typeof window === "undefined") return null;
  if (!MOCK && !managementTransportAllowed(window.location.href)) {
    throw new ManagementTransportError(swt("common.managementHTTPSRequired"));
  }
  return localStorage.getItem("counterweight_token");
}

// The server waits up to ten minutes for a clone and writes the response within eleven; allow twelve.
export const CODE_REVIEW_CREATE_TIMEOUT_MS = 12 * 60 * 1000;

export async function http<T>(path: string, init?: RequestInit): Promise<T> {
  // Load the demo handler (and its ~8k lines of fixtures) only in the backend-free demo build.
  // A static import keeps that whole subtree in the production bundle; a dynamic one, gated on the
  // build-time MOCK constant, lets the bundler split it into a chunk that production never loads.
  if (MOCK) {
    const { mockHandle } = await import("@/lib/mock/handler");
    return mockHandle<T>(init?.method ?? "GET", path, init?.body ?? null);
  }
  const token = getToken();
  const r = await fetch(`/api${path}`, {
    ...init,
    headers: {
      ...(init?.body ? { "Content-Type": "application/json" } : {}),
      ...(token ? { Authorization: `Bearer ${token}` } : {}),
      ...(init?.headers as Record<string, string> | undefined),
    },
  });
  if (r.status === 401) {
    if (typeof window !== "undefined") {
      localStorage.removeItem("counterweight_token");
      document.cookie = "counterweight_token=; path=/; max-age=0";
      document.cookie = "counterweight_session=; path=/; max-age=0";
      window.location.href = "/login";
    }
    throw new Error(swt("interface.m2413"));
  }
  if (!r.ok) {
    const fallback = `${init?.method ?? "GET"} ${path}: ${r.status}`;
    let message = fallback;
    try {
      const payload = (await r.json()) as { error?: unknown };
      if (typeof payload.error === "string" && payload.error.trim()) {
        message = payload.error.trim();
      }
    } catch {
      // Keep the status-based fallback for empty or non-JSON error responses.
    }
    throw new Error(message);
  }
  if (r.status === 204) return undefined as T;
  return r.json();
}

// sseUrl builds a URL for Server-Sent Events streams. In a build served by the Go backend (the
// static export) the stream is on the same origin as the page and the API that issues the ticket.
// Only the Next.js development server differs: its `/api` rewrite buffers the streamed response,
// so event frames never reach the browser (the EventSource opens but receives 0 messages), and
// there the page connects straight to the Go backend on port 8787, which answers cross-origin
// requests only from the development server on a loopback name and port 3000. Set
// NEXT_PUBLIC_SSE_BASE to "" to force same-origin, or to another origin (HTTPS for a remote one)
// whose own CORS policy admits this page; the Go backend admits no origin but the two above.
// The URL carries no credential: streamUrl adds a short-lived single-use ticket.
// mockReport returns a canned Markdown report for the demo.
function mockReport(_task?: string): string {
  return swt("interface.m2414");
}

export function sseUrl(path: string): string {
  const devBackend =
    process.env.NODE_ENV === "development" && typeof window !== "undefined"
      ? `${window.location.protocol}//${window.location.hostname}:8787`
      : "";
  const base = process.env.NEXT_PUBLIC_SSE_BASE ?? devBackend;
  if (!MOCK && typeof window !== "undefined" && !managementTransportAllowed(`${base}${path}`, window.location.href)) {
    throw new ManagementTransportError(swt("common.managementHTTPSRequired"));
  }
  return localizedUrl(`${base}${path}`);
}

// streamUrl is sseUrl plus a fresh stream ticket. A ticket works once and for about a minute, so
// request one for every connection attempt, including each reconnect. The ticket is requested for
// this exact path, so the server can refuse it on any other stream.
export async function streamUrl(path: string): Promise<string> {
  const url = sseUrl(path);
  if (MOCK) return url;
  const { ticket } = await http<{ ticket: string }>("/auth/stream-ticket", {
    method: "POST",
    body: JSON.stringify({ path }),
  });
  return `${url}${url.includes("?") ? "&" : "?"}ticket=${encodeURIComponent(ticket)}`;
}

// isNotAvailable is true for the error of a request to a route this server does not have (a 404), so
// features added in newer builds can stay hidden on an older backend instead of showing an error.
export function isNotAvailable(error: unknown): boolean {
  return error instanceof Error && /(: 404$)|^404\b|page not found/i.test(error.message);
}

const get = <T>(p: string) => http<T>(p);
const post = <T>(p: string, body?: unknown) =>
  http<T>(p, { method: "POST", body: body ? JSON.stringify(body) : undefined });
const put = <T>(p: string, body?: unknown) =>
  http<T>(p, { method: "PUT", body: body ? JSON.stringify(body) : undefined });
const patch = <T>(p: string, body?: unknown) =>
  http<T>(p, { method: "PATCH", body: body ? JSON.stringify(body) : undefined });
const del = <T>(p: string, body?: unknown) =>
  http<T>(p, { method: "DELETE", body: body ? JSON.stringify(body) : undefined });

// Go serializes nil slices as JSON null — coerce to [].
const arr = <T>(x: T[] | null | undefined): T[] => x ?? [];
const tq = (task?: string, sep: "?" | "&" = "?") => (task ? `${sep}task=${encodeURIComponent(task)}` : "");

// findingFilterParams serializes shared filters for lists, groups,
// asset trees, and exports. Add new filters here; the backend uses one matching parser.
function findingFilterParams(q: Omit<FindingQuery, "page" | "pageSize">): URLSearchParams {
  const p = new URLSearchParams();
  if (q.severity && q.severity !== "all") p.set("severity", q.severity);
  if (q.status && q.status !== "all") p.set("status", q.status);
  if (q.vulnclass && q.vulnclass !== "all") p.set("vulnclass", q.vulnclass);
  if (q.task && q.task !== "all") p.set("task_id", q.task);
  if (q.query?.trim()) p.set("q", q.query.trim());
  if (q.sort) p.set("sort", q.sort);
  if (q.assetScope) p.set("asset_scope", q.assetScope);
  return p;
}

function interceptPageQuery(page: number, size: number, filter: InterceptApprovalFilter) {
  const query = new URLSearchParams({ page: String(page), size: String(size) });
  if (filter.status) query.set("status", filter.status);
  if (filter.decision_source) query.set("decision_source", filter.decision_source);
  return query.toString();
}

export const api = {
  // Backend application version injected via release ldflags, defaulting to dev.
  health: () => get<{ ok: boolean; service: string; version: string }>("/health"),

  // ---- auth ----
  authStatus: () => get<{ initialized: boolean }>("/auth/status"),
  login: (username: string, password: string) => post<{ token: string }>("/auth/login", { username, password }),
  initPassword: (password: string, setupToken: string) =>
    post<{ token: string }>("/auth/init", { password, setup_token: setupToken }),
  changePassword: (oldPassword: string, newPassword: string) =>
    post<{ ok: boolean; token: string }>("/auth/change-password", {
      old_password: oldPassword,
      new_password: newPassword,
    }),

  // ---- tasks ----
  tasks: () =>
    get<{ tasks: Task[]; active: string }>("/tasks").then((r) => ({ tasks: arr(r.tasks), active: r.active ?? "" })),
  task: (id: string) => get<Task>(`/tasks/${seg(id)}`),
  createTask: (input: {
    name?: string;
    categoryId?: number;
    description: string;
    goal: string;
    llmProfileIds?: number[];
    sourceTaskIds?: string[];
    companyIds?: number[];
    timeoutSeconds?: number;
    seedFirstIntent?: boolean;
    planHeartbeatSeconds?: number;
    coverageEnabled?: boolean;
    interceptRules?: AssetInterceptRuleInput[];
    // Test accounts stored on the task before it starts; their passwords are write-only. Not for code reviews.
    authzAccounts?: { role: string; username?: string; password?: string; agent_access?: boolean }[];
    assessmentType?: AssessmentType;
    // Usage budget. Omitted fields use the server default; tokenBudget 0 means no limit.
    tokenBudget?: number;
    costBudget?: number;
    costCurrency?: string;
    // Code reviews only: the server clones the repository before it answers.
    repository?: string;
    ref?: string;
    mode?: CodeReviewMode;
    instructions?: string;
    change?: ReviewChangeInput; // Code reviews only: limit the review to a pull request or a base/head pair.
    // Per-call abort deadline; the default is no client deadline. A review clone needs CODE_REVIEW_CREATE_TIMEOUT_MS.
    timeoutMs?: number;
  }) =>
    http<Task>("/tasks", {
      method: "POST",
      signal: input.timeoutMs ? AbortSignal.timeout(input.timeoutMs) : undefined,
      body: JSON.stringify({
        name: input.name ?? "",
        category_id: input.categoryId ?? null,
        description: input.description,
        goal: input.goal,
        llm_profile_ids: input.llmProfileIds ?? [],
        source_task_ids: input.sourceTaskIds ?? [],
        company_ids: input.companyIds ?? [],
        timeout_seconds: input.timeoutSeconds ?? 0,
        seed_first_intent: input.seedFirstIntent ?? false,
        plan_heartbeat_seconds: input.planHeartbeatSeconds ?? 0, // 0 is normalized by the backend to 600 seconds (10 minutes).
        coverage_enabled: input.coverageEnabled ?? true, // Enabled by default; false disables asset coverage.
        intercept_rules: input.interceptRules ?? [], // Task-specific asset interception rules.
        assessment_type: input.assessmentType ?? "general",
        ...(input.tokenBudget !== undefined ? { token_budget: input.tokenBudget } : {}),
        ...(input.costBudget ? { cost_budget: input.costBudget, cost_currency: input.costCurrency || "USD" } : {}),
        ...(input.repository ? { repository: input.repository } : {}),
        ...(input.ref ? { ref: input.ref } : {}),
        ...(input.mode ? { mode: input.mode } : {}),
        ...(input.instructions ? { instructions: input.instructions } : {}),
        ...(input.change ? { change: input.change } : {}),
        ...(input.authzAccounts?.length ? { authz_accounts: input.authzAccounts } : {}),
      }),
    }),
  // Changes the type of a task that has not started, or reclassifies a finished legacy task (reclassify).
  // Changing into code_review clones the repository first, so it can take minutes.
  changeAssessmentType: (
    id: string,
    input: {
      assessmentType: AssessmentType;
      reclassify?: boolean;
      repository?: string;
      ref?: string;
      mode?: CodeReviewMode;
    },
  ) =>
    http<Task>(`/tasks/${seg(id)}/assessment-type`, {
      method: "PATCH",
      signal: AbortSignal.timeout(CODE_REVIEW_CREATE_TIMEOUT_MS),
      body: JSON.stringify({
        assessment_type: input.assessmentType,
        ...(input.reclassify ? { reclassify: true } : {}),
        ...(input.repository ? { repository: input.repository } : {}),
        ...(input.ref ? { ref: input.ref } : {}),
        ...(input.mode ? { mode: input.mode } : {}),
      }),
    }),
  taskCategories: () => get<{ categories: TaskCategory[] }>("/task-categories").then((r) => arr(r.categories)),
  updateTask: (id: string, input: { name?: string; pinned?: boolean }) => patch<Task>(`/tasks/${seg(id)}`, input),
  renameTask: (id: string, name: string) => patch<Task>(`/tasks/${seg(id)}`, { name }),
  pinTask: (id: string, pinned: boolean) => patch<Task>(`/tasks/${seg(id)}`, { pinned }),
  createTaskCategory: (name: string) => post<TaskCategory>("/task-categories", { name }),
  renameTaskCategory: (id: number, name: string) => patch<TaskCategory>(`/task-categories/${seg(id)}`, { name }),
  deleteTaskCategory: (id: number) => del<{ deleted: number }>(`/task-categories/${seg(id)}`),
  updateTaskCategory: (taskId: string, categoryId?: number) =>
    patch<Task>(`/tasks/${seg(taskId)}/category`, { category_id: categoryId ?? null }),
  // Omitted/undefined categoryId removes classification by sending null.
  updateTasksCategory: (taskIds: string[], categoryId?: number) =>
    post<{ items: BatchCategoryItem[]; category: TaskCategory | null }>("/tasks/category/batch", {
      task_ids: taskIds,
      category_id: categoryId ?? null,
    }),
  taskTemplates: () => get<{ templates: TaskTemplate[] }>("/task-templates").then((r) => arr(r.templates)),
  createTaskTemplate: (
    input: Pick<TaskTemplate, "name" | "description" | "goal" | "category_id" | "intercept_rules"> &
      Partial<Pick<TaskTemplate, "assessment_type" | "repository" | "mode">>,
  ) => post<TaskTemplate>("/task-templates", input),
  updateTaskTemplate: (
    id: number,
    input: Partial<
      Pick<
        TaskTemplate,
        "name" | "description" | "goal" | "category_id" | "intercept_rules" | "assessment_type" | "repository" | "mode"
      >
    >,
  ) => patch<TaskTemplate>(`/task-templates/${seg(id)}`, input),
  deleteTaskTemplate: (id: number) => del<{ deleted: number }>(`/task-templates/${seg(id)}`),
  updateTaskLLMProfiles: (id: string, llmProfileIds: number[], activeLLMProfileId?: number) =>
    put<{
      id: string;
      llm_profile_ids: number[];
      active_llm_profile_id?: number;
      llm_failover_state: string;
      reopened_intents: number;
      switch_event?: Activity;
    }>(`/tasks/${seg(id)}/llm`, {
      llm_profile_ids: llmProfileIds,
      active_llm_profile_id: activeLLMProfileId ?? null,
    }),
  deleteTask: (id: string, options: DeleteTaskOptions) => del<DeleteTaskResult>(`/tasks/${seg(id)}`, options),
  controlTask: (id: string, action: "pause" | "resume") =>
    post<{ id: string; paused: boolean; queued: boolean; status: string }>(`/tasks/${seg(id)}/control`, { action }),
  // Changes a task's usage budget in any state; it does not resume a paused task. 0 removes a limit.
  setTaskBudget: (id: string, budget: { token_budget?: number; cost_budget?: number; cost_currency?: string }) =>
    patch<Task>(`/tasks/${seg(id)}/budget`, budget),
  controlTasksBatch: (taskIds: string[], action: "pause" | "resume") =>
    post<{ items: BatchControlItem[] }>("/tasks/control/batch", { task_ids: taskIds, action }),
  taskArchives: (input?: { page?: number; size?: number; q?: string; state?: string }) => {
    const query = new URLSearchParams();
    query.set("page", String(input?.page ?? 1));
    query.set("size", String(input?.size ?? 20));
    if (input?.q) query.set("q", input.q);
    if (input?.state) query.set("state", input.state);
    return get<TaskArchivePage>(`/task-archives?${query.toString()}`).then((response) => ({
      ...response,
      items: arr(response.items),
    }));
  },
  taskArchive: (id: number) => get<TaskArchive>(`/task-archives/${seg(id)}`),
  archiveTask: (id: string) => post<TaskArchive>(`/tasks/${seg(id)}/archive`),
  archiveTasks: (taskIds: string[]) =>
    post<{ items: ArchiveBatchItem[] }>("/tasks/archive/batch", { task_ids: taskIds }),
  restoreTaskArchive: (id: number) => post<TaskArchive>(`/task-archives/${seg(id)}/restore`),
  restoreTaskArchives: (archiveIds: number[]) =>
    post<{ items: ArchiveBatchItem[] }>("/task-archives/restore/batch", { archive_ids: archiveIds }),
  deleteTaskArchive: (id: number) => del<TaskArchive>(`/task-archives/${seg(id)}`),
  deleteTaskArchives: (archiveIds: number[]) =>
    post<{ items: ArchiveBatchItem[] }>("/task-archives/delete/batch", { archive_ids: archiveIds }),
  controlIntent: (
    taskId: string,
    intentId: string,
    action: "pause" | "resume" | "cancel",
    reason?: string,
    // Cancel only: soft (default) marks deleted and records a reason; hard cascades through exclusive descendants.
    mode?: "soft" | "hard",
  ) =>
    post<{
      id: number;
      state: "paused" | "open" | "deleted" | "";
      deleted?: { intents: number; facts: number; findings: number; activities: number };
    }>(`/tasks/${seg(taskId)}/intents/${seg(intentId)}/control`, {
      action,
      reason: reason ?? "",
      mode: mode ?? "soft",
    }),
  sendWorkerMessage: (taskId: string, intentId: string, message: string, requestId: string) =>
    post<{
      id: number;
      state: "running";
      accepted: true;
      request_id: string;
    }>(`/tasks/${seg(taskId)}/intents/${seg(intentId)}/messages`, { message, request_id: requestId }),
  taskLLMResolution: (id: string) => get<TaskLLMResolutions>(`/tasks/${seg(id)}/llm/resolution`),
  // Reopen unsuccessful blocked/exhausted/stopped intents so Workers reclaim and restart them.
  rerunIntent: (taskId: string, intentId: string) =>
    post<{ id: string; reopened: number }>(`/tasks/${seg(taskId)}/intents/${seg(intentId)}/rerun`),
  // Rerun all blocked task intents, useful after a network/LLM disconnect blocks several at once.
  rerunBlocked: (taskId: string) =>
    post<{ id: string; reopened: number }>(`/tasks/${seg(taskId)}/intents/rerun-blocked`),
  setActive: (id: string) => post<{ active: string }>("/active", { id }),
  // ---- stats ----
  stats: (task?: string) => get<Stats>(`/stats${tq(task)}`),
  // Approximate asset coverage: in-scope assets touched by facts, with total/tested counts per type.
  taskCoverage: (id: string) =>
    get<{
      enabled: boolean; // Whether coverage is enabled; remaining fields are zero-valued when false.
      scope_rows: number;
      denominator: number;
      tested: number;
      pct: number | null;
      by_type: { type: string; total: number; tested: number }[];
    }>(`/tasks/${seg(id)}/coverage`),
  // Coverage graph includes in-scope assets plus connecting root/company nodes, with tested/in_scope flags.
  taskCoverageGraph: (id: string) => get<CoverageGraphData>(`/tasks/${seg(id)}/coverage-graph`),
  // Global llm_usage aggregates for the new dashboard token view: per-profile totals and daily buckets.
  usageStats: (days = 365) => get<UsageStats>(`/tokens/usage?days=${encodeURIComponent(days)}`),
  // Objective management in overview.
  // All task objectives with text/vulnclass/state.
  taskGoals: (id: string) =>
    get<{ goals: TaskGoal[] | null }>(`/tasks/${seg(id)}/goals`).then((response) => ({ goals: arr(response.goals) })),
  // Manual objective creation writes to the graph, notifies the planner, and reactivates the task.
  addGoal: (id: string, text: string, vulnclass?: string) =>
    post<TaskGoal>(`/tasks/${seg(id)}/goals`, { text, vulnclass: vulnclass ?? "" }),
  // Manual text/vulnclass edits notify the planner of old/new values and reactivate the task.
  updateGoal: (id: string, goalId: string, text: string, vulnclass?: string) =>
    patch<TaskGoal>(`/tasks/${seg(id)}/goals/${seg(goalId)}`, { text, vulnclass: vulnclass ?? "" }),
  // Manual hard deletion notifies the planner but does not reactivate the task.
  deleteGoal: (id: string, goalId: string) => del<{ ok: boolean }>(`/tasks/${seg(id)}/goals/${seg(goalId)}`),

  // Operational constraint management in overview.
  // All task operational constraints (allow/deny).
  taskConstraints: (id: string) =>
    get<{ constraints: TaskConstraint[] | null }>(`/tasks/${seg(id)}/constraints`).then((response) => ({
      constraints: arr(response.constraints),
    })),
  // Add a constraint without notifying the planner; the next planning round reads it.
  addConstraint: (id: string, text: string, kind: TaskConstraint["kind"]) =>
    post<TaskConstraint>(`/tasks/${seg(id)}/constraints`, { text, kind }),
  // Edit constraint text and allow/deny mode.
  updateConstraint: (id: string, constraintId: string, text: string, kind: TaskConstraint["kind"]) =>
    patch<TaskConstraint>(`/tasks/${seg(id)}/constraints/${seg(constraintId)}`, { text, kind }),
  // Delete a constraint.
  deleteConstraint: (id: string, constraintId: string) =>
    del<{ ok: boolean }>(`/tasks/${seg(id)}/constraints/${seg(constraintId)}`),

  // Task-specific asset block/allow rules in overview.
  taskInterceptRules: (id: string) =>
    get<{ rules: AssetInterceptRule[] | null }>(`/tasks/${seg(id)}/intercept-rules`).then((r) => arr(r.rules)),
  createTaskInterceptRule: (id: string, rule: AssetInterceptRuleInput) =>
    post<AssetInterceptRule>(`/tasks/${seg(id)}/intercept-rules`, rule),
  updateTaskInterceptRule: (id: string, ruleId: number, rule: AssetInterceptRuleInput) =>
    put<AssetInterceptRule>(`/tasks/${seg(id)}/intercept-rules/${seg(ruleId)}`, rule),
  deleteTaskInterceptRule: (id: string, ruleId: number) =>
    del<{ deleted: boolean }>(`/tasks/${seg(id)}/intercept-rules/${seg(ruleId)}`),
  toggleTaskInterceptRule: (id: string, ruleId: number, enabled: boolean) =>
    post<{ ok: boolean; enabled: boolean }>(`/tasks/${seg(id)}/intercept-rules/${seg(ruleId)}/toggle`, { enabled }),

  // Task test scopes, including inherited source-task scopes.
  taskScope: (id: string) => get<{ scope: TaskScopeRow[] }>(`/tasks/${seg(id)}/scope`),
  // Manually add company/root_domain/subdomain/ip/cidr test scope.
  addTaskScope: (id: string, kind: string, value: string, reason?: string) =>
    post<TaskScopeRow>(`/tasks/${seg(id)}/scope`, { kind, value, reason }),
  // Delete one scope owned by this task.
  deleteTaskScope: (id: string, scopeId: number) => del<{ ok: boolean }>(`/tasks/${seg(id)}/scope/${seg(scopeId)}`),
  // Asset-linked intents/facts/findings in this task, used by coverage-graph drawers.
  taskAssetRefs: (id: string, assetId: number) =>
    get<CoverageAssetRefs>(`/tasks/${seg(id)}/asset-refs?asset_id=${encodeURIComponent(assetId)}`),

  // ---- workspace file manager (workDir) ----
  workspaceList: (path = "") => get<WorkspaceListing>(`/workspace/list?path=${encodeURIComponent(path)}`),
  workspaceRead: (path: string) => get<WorkspaceFile>(`/workspace/read?path=${encodeURIComponent(path)}`),
  workspaceWrite: (path: string, content: string) =>
    post<{ ok: boolean; path: string }>(`/workspace/write`, { path, content }),
  workspaceMkdir: (path: string) => post<{ ok: boolean; path: string }>(`/workspace/mkdir`, { path }),
  workspaceDelete: (path: string) => del<{ ok: boolean }>(`/workspace/delete?path=${encodeURIComponent(path)}`),
  workspaceUpload: async (dir: string, files: File[]) => {
    if (MOCK) return { uploaded: files.length };
    const fd = new FormData();
    for (const f of files) fd.append("file", f);
    const token = getToken();
    const r = await fetch(`/api/workspace/upload?path=${encodeURIComponent(dir)}`, {
      method: "POST",
      headers: { ...(token ? { Authorization: `Bearer ${token}` } : {}) },
      body: fd,
    });
    if (!r.ok) throw new Error(swt("interface.m2415", { p0: r.status }));
    return r.json() as Promise<{ uploaded: number }>;
  },
  workspaceDownload: async (path: string) => {
    let blob: Blob;
    if (MOCK) {
      blob = new Blob([swt("interface.m2416", { p0: path })], { type: "text/plain" });
    } else {
      const token = getToken();
      const r = await fetch(`/api/workspace/download?path=${encodeURIComponent(path)}`, {
        headers: { ...(token ? { Authorization: `Bearer ${token}` } : {}) },
      });
      if (!r.ok) throw new Error(swt("interface.m2417", { p0: r.status }));
      blob = await r.blob();
    }
    const objUrl = URL.createObjectURL(blob);
    const a = document.createElement("a");
    a.href = objUrl;
    a.download = path.split("/").pop() || "download";
    document.body.appendChild(a);
    a.click();
    a.remove();
    URL.revokeObjectURL(objUrl);
  },

  // ---- assets ----
  // Server-side paginated: pass limit/offset, get back the page + full match total.
  assets: (type = "", limit = 50, offset = 0) =>
    get<{ count: number; total: number; assets: Asset[] }>(
      `/assets?type=${encodeURIComponent(type)}&limit=${encodeURIComponent(limit)}&offset=${encodeURIComponent(offset)}`,
    ).then((r) => ({ assets: r?.assets ?? [], total: r?.total ?? r?.count ?? 0 })),
  searchAssets: (dsl: string, type = "", limit = 50, offset = 0) =>
    get<{ count: number; total: number; assets: Asset[] }>(
      `/assets?dsl=${encodeURIComponent(dsl)}${type ? `&type=${encodeURIComponent(type)}` : ""}&limit=${encodeURIComponent(limit)}&offset=${encodeURIComponent(offset)}`,
    ).then((r) => ({ assets: r?.assets ?? [], total: r?.total ?? r?.count ?? 0 })),
  assetCounts: (taskId = "") =>
    get<Record<string, number>>(`/assets/counts${taskId ? `?task_id=${encodeURIComponent(taskId)}` : ""}`),
  deleteAssets: (ids: number[]) =>
    http<{ deleted: number }>("/assets", { method: "DELETE", body: JSON.stringify({ ids }) }),
  // task-scoped view of the same endpoint — server-side paginated like `assets`
  taskAssets: (taskId: string, type = "", limit = 50, offset = 0) =>
    get<{ count: number; total: number; assets: Asset[] }>(
      `/assets?task_id=${encodeURIComponent(taskId)}&type=${encodeURIComponent(type)}&limit=${encodeURIComponent(limit)}&offset=${encodeURIComponent(offset)}`,
    ).then((r) => ({ assets: r?.assets ?? [], total: r?.total ?? r?.count ?? 0 })),
  // DSL search scoped to a task — the backend forces the task_id filter, so it
  // always stays within that task's assets (same DSL grammar as `searchAssets`).
  searchTaskAssets: (taskId: string, dsl: string, type = "", limit = 50, offset = 0) =>
    get<{ count: number; total: number; assets: Asset[] }>(
      `/assets?task_id=${encodeURIComponent(taskId)}&dsl=${encodeURIComponent(dsl)}&type=${encodeURIComponent(type)}&limit=${encodeURIComponent(limit)}&offset=${encodeURIComponent(offset)}`,
    ).then((r) => ({ assets: r?.assets ?? [], total: r?.total ?? r?.count ?? 0 })),
  attachTaskAssets: (taskId: string, assetIds: number[], sourceSummary: string) =>
    post<TaskAssetMutation>(`/tasks/${seg(taskId)}/assets`, {
      asset_ids: assetIds,
      source_summary: sourceSummary,
    }),
  registerTaskAssetScopes: (taskId: string, scope: CompanyScopeRule[]) =>
    post<TaskAssetScopeMutation>(`/tasks/${seg(taskId)}/assets`, { scope }),
  detachTaskAsset: (taskId: string, assetId: number) =>
    del<{ detached: number }>(`/tasks/${seg(taskId)}/assets/${seg(assetId)}`),
  taskIntentAssets: (taskId: string) =>
    get<{ assets: IntentAsset[] }>(`/tasks/${seg(taskId)}/intent-assets`).then((r) => arr(r.assets)),

  // Companies and asset scopes: the sole ownership source.
  companies: () => get<Company[]>("/companies").then(arr),
  createCompany: (name: string, scope: CompanyScopeRule[]) =>
    post<{ id: number; created: boolean; scope_added?: number; scope_invalid?: number; scope_errors?: string[] }>(
      "/companies",
      {
        name,
        scope,
      },
    ),
  addCompanyScope: (id: number, scope: CompanyScopeRule[], reason = "") =>
    post<CompanyScopeMutation>(`/companies/${seg(id)}/scope`, {
      scope,
      reason,
    }),
  approveCompanyScope: (id: number, rule: number) =>
    post<{ approved: boolean }>(`/companies/${seg(id)}/scope/${seg(rule)}/approve`, {}),
  updateCompanyScope: (id: number, scope: CompanyScopeRule[], reason = "") =>
    post<CompanyScopeMutation>(`/companies/${seg(id)}/scope`, {
      scope,
      reason,
      reset: true,
    }),
  deleteCompany: (id: number, deleteAssets = false) =>
    http<{ deleted: number; assets_deleted: number }>(`/companies/${seg(id)}`, {
      method: "DELETE",
      body: JSON.stringify({ delete_assets: deleteAssets }),
    }),

  // ---- exploration (per task) ----
  frontier: (task?: string) => get<TaskNode[]>(`/exploration/frontier${tq(task)}`).then(arr),
  findings: (task?: string) => get<Finding[]>(`/exploration/findings${tq(task)}`).then(arr),
  findingsPage: (q: FindingQuery) => {
    const p = findingFilterParams(q);
    p.set("page", String(q.page));
    p.set("limit", String(q.pageSize));
    return get<FindingsPage>(`/exploration/findings?${p.toString()}`);
  },
  findingGroups: (q: FindingQuery) => {
    const p = findingFilterParams(q);
    p.set("page", String(q.page));
    p.set("limit", String(q.pageSize));
    return get<FindingGroupsPage>(`/exploration/findings/groups?${p.toString()}`);
  },
  // findingAssetTree loads assets with findings and their ancestors for the left navigation tree,
  // with subtree aggregate counts. Navigation trees load completely without pagination.
  findingAssetTree: (q: Omit<FindingQuery, "page" | "pageSize">) =>
    get<FindingAssetTree>(`/exploration/findings/asset-tree?${findingFilterParams(q).toString()}`),
  findingStats: () => get<FindingStats>("/exploration/findings/stats"),
  // exportFindings downloads findings. Selected scope passes finding IDs;
  // filtered scope passes FindingQuery filters; all scope ignores filters.
  exportFindings: async (opts: {
    format: "md-single" | "md-zip" | "csv" | "json" | "pdf" | "sarif";
    scope: "filtered" | "all" | "selected";
    filters?: Omit<FindingQuery, "page" | "pageSize">;
    ids?: string[];
  }) => {
    const p = new URLSearchParams({ format: opts.format, scope: opts.scope });
    if (opts.scope === "selected") {
      p.set("ids", (opts.ids ?? []).join(","));
    } else if (opts.scope === "filtered" && opts.filters) {
      for (const [k, v] of findingFilterParams(opts.filters)) p.set(k, v);
    }
    const token = getToken();
    const r = await fetch(`/api/exploration/findings/export?${p.toString()}`, {
      headers: token ? { Authorization: `Bearer ${token}` } : {},
    });
    if (!r.ok) throw new Error(`export: ${r.status}`);
    const blob = await r.blob();
    // Prefer the backend Content-Disposition filename, otherwise use the default.
    const disp = r.headers.get("Content-Disposition") ?? "";
    const m = disp.match(/filename="?([^"]+)"?/);
    const filename = m?.[1] ?? `findings-export`;
    const url = URL.createObjectURL(blob);
    const a = document.createElement("a");
    a.href = url;
    a.download = filename;
    document.body.appendChild(a);
    a.click();
    a.remove();
    URL.revokeObjectURL(url);
  },
  getFinding: (id: string, contextTaskId?: string) =>
    get<Finding>(
      `/exploration/findings/${seg(id)}${contextTaskId ? `?context_task=${encodeURIComponent(contextTaskId)}` : ""}`,
    ),
  findingTraffic: (id: string, contextTask?: string) =>
    get<FindingTraffic>(
      `/exploration/findings/${seg(id)}/traffic${contextTask ? `?context_task=${encodeURIComponent(contextTask)}` : ""}`,
    ),
  bindFindingTraffic: (id: string, traffic_refs: TrafficEvidenceRef[], contextTask?: string) =>
    post<FindingTraffic>(
      `/exploration/findings/${seg(id)}/traffic${contextTask ? `?context_task=${encodeURIComponent(contextTask)}` : ""}`,
      { traffic_refs },
    ),
  editFindingTraffic: (
    id: string,
    bindingId: string,
    version: number,
    fields: { role: TrafficEvidenceRole; note: string },
    contextTask?: string,
  ) =>
    patch<FindingTraffic>(
      `/exploration/findings/${seg(id)}/traffic/${seg(bindingId)}${contextTask ? `?context_task=${encodeURIComponent(contextTask)}` : ""}`,
      { version, ...fields },
    ),
  removeFindingTraffic: (id: string, bindingId: string, version: number, contextTask?: string) =>
    del<FindingTraffic>(
      `/exploration/findings/${seg(id)}/traffic/${seg(bindingId)}${contextTask ? `?context_task=${encodeURIComponent(contextTask)}` : ""}`,
      { version },
    ),
  orderFindingTraffic: (id: string, binding_ids: string[], version: number, contextTask?: string) =>
    http<FindingTraffic>(
      `/exploration/findings/${seg(id)}/traffic/order${contextTask ? `?context_task=${encodeURIComponent(contextTask)}` : ""}`,
      { method: "PUT", body: JSON.stringify({ binding_ids, version }) },
    ),
  findingTrafficDetail: (id: string, bindingId: string, contextTask?: string) =>
    get<FindingTrafficDetail>(
      `/exploration/findings/${seg(id)}/traffic/${seg(bindingId)}${contextTask ? `?context_task=${encodeURIComponent(contextTask)}` : ""}`,
    ),
  findingTrafficBody: (
    id: string,
    bindingId: string,
    side: "request" | "response",
    offset: number,
    contextTask?: string,
  ) =>
    get<EvidenceBodyPreview>(
      `/exploration/findings/${seg(id)}/traffic/${seg(bindingId)}/body?side=${encodeURIComponent(side)}&offset=${encodeURIComponent(offset)}${contextTask ? `&context_task=${encodeURIComponent(contextTask)}` : ""}`,
    ),
  downloadFindingTrafficBody: async (
    id: string,
    bindingId: string,
    side: "request" | "response",
    contextTask?: string,
  ) => {
    const token = getToken();
    const response = await fetch(
      `/api/exploration/findings/${seg(id)}/traffic/${seg(bindingId)}/body?side=${encodeURIComponent(side)}&download=1${contextTask ? `&context_task=${encodeURIComponent(contextTask)}` : ""}`,
      { headers: token ? { Authorization: `Bearer ${token}` } : {} },
    );
    if (!response.ok) {
      const error = await response.json().catch(() => ({ error: swt("interface.m2418") }));
      throw new Error(error.error ?? swt("interface.m2418"));
    }
    const blob = await response.blob();
    const url = URL.createObjectURL(blob);
    const a = document.createElement("a");
    a.href = url;
    a.download = `evidence-${bindingId}-${side}.bin`;
    a.click();
    setTimeout(() => URL.revokeObjectURL(url), 1000);
  },
  // Finding lineage is the node/edge subgraph back to the task's initial node.
  findingLineage: (id: string) => get<{ nodes: TaskNode[]; edges: Edge[] }>(`/exploration/findings/${seg(id)}/lineage`),
  setFindingStatus: (id: string, status: FindingStatus) =>
    patch<Finding>(`/exploration/findings/${seg(id)}`, { status }),
  setFindingSeverity: (id: string, severity: Severity) =>
    patch<Finding>(`/exploration/findings/${seg(id)}`, { severity }),
  // Save finding name/category/severity together for inline edits; submit only supplied fields.
  updateFinding: (
    id: string,
    fields: { name?: string; vulnclass?: string; severity?: Severity; status?: FindingStatus },
  ) => patch<Finding>(`/exploration/findings/${seg(id)}`, fields),
  // Delete the finding record and source exploration node from all lists, task tabs, and graphs.
  deleteFinding: (id: string) => del<{ deleted: boolean; id: number }>(`/exploration/findings/${seg(id)}`),
  findingRetests: (id: string) =>
    get<{ retests: FindingRetest[] }>(`/exploration/findings/${seg(id)}/retests`).then((r) => arr(r.retests)),
  activeFindingRetests: () =>
    get<{ retests: ActiveFindingRetest[] }>("/exploration/findings/retests/active").then((r) => arr(r.retests)),
  startFindingRetest: (id: string, notes: string) =>
    post<{ retest: FindingRetest; created: boolean }>(`/exploration/findings/${seg(id)}/retests`, {
      notes,
    }),
  // Stop one retest, verification or fix run; a queued run is sealed before it starts.
  stopFindingRun: (runId: number) => post<{ status: string }>(`/finding-runs/${seg(runId)}/stop`),
  // Mitigation tab: confirmed code-review findings, their fix attempts and the GitHub token.
  taskMitigations: (taskId: string) => get<TaskMitigations>(`/tasks/${seg(taskId)}/mitigations`),
  // unverifiedOk: the operator was warned the finding may be a false positive and chose to fix it anyway.
  startMitigation: (findingId: string, unverifiedOk = false) =>
    post<{ run: FindingRetest; created: boolean }>(
      `/exploration/findings/${seg(findingId)}/mitigations`,
      unverifiedOk ? { unverified_ok: true } : undefined,
    ),
  // Independent verification of a code review finding, on the operator's request.
  verifyFinding: (findingId: string) =>
    post<{ retest: FindingRetest; created: boolean }>(`/exploration/findings/${seg(findingId)}/verify`),
  taskAutomation: (taskId: string) => get<TaskAutomation>(`/tasks/${seg(taskId)}/automation`),
  setTaskAutomation: (taskId: string, patch: Partial<Pick<TaskAutomation, "verify" | "mitigate">>) =>
    put<TaskAutomation>(`/tasks/${seg(taskId)}/automation`, patch),
  publishMitigation: (runId: number) => post<{ pr_url: string }>(`/mitigations/${seg(runId)}/publish`),
  refreshMitigation: (runId: number) => post<{ mitigation: FindingMitigation }>(`/mitigations/${seg(runId)}/refresh`),
  // Re-verify the finding on the code its merged pull request produced.
  recheckMitigation: (runId: number) =>
    post<{ run: FindingRetest; created: boolean }>(`/mitigations/${seg(runId)}/recheck`),
  starPrompt: () => get<StarPrompt>("/star-prompt"),
  answerStarPrompt: (action: "star" | "later" | "dismiss") =>
    post<{ starred: boolean; url: string }>("/star-prompt", { action }),
  apiTokens: () => get<{ tokens: ApiToken[] }>("/api-tokens").then((r) => arr(r.tokens)),
  // The secret is in the reply only once; it cannot be fetched again.
  createApiToken: (input: { name: string; scopes: string[]; expires_at?: string }) =>
    post<{ token: string; api_token: ApiToken }>("/api-tokens", input),
  revokeApiToken: (id: number) => del<{ id: number; revoked: boolean }>(`/api-tokens/${seg(id)}`),
  githubToken: () => get<GitHubTokenStatus>("/github/token"),
  saveGitHubToken: (token: string) => put<GitHubTokenStatus>("/github/token", { token }),
  deleteGitHubToken: () => del<GitHubTokenStatus>("/github/token"),
  jira: () => get<JiraStatus>("/integrations/jira"),
  saveJiraCredentials: (site: string, email: string, token: string) =>
    put<JiraStatus>("/integrations/jira/credentials", { site, email, token }),
  deleteJiraCredentials: () => del<JiraStatus>("/integrations/jira/credentials"),
  saveJiraConfig: (config: JiraConfig) => put<JiraStatus>("/integrations/jira/config", config),
  jiraProjects: () => get<JiraProject[]>("/integrations/jira/projects"),
  jiraProjectMeta: (key: string, issueType?: string) =>
    get<JiraProjectMeta>(
      `/integrations/jira/projects/${seg(key)}/meta${issueType ? `?issue_type=${encodeURIComponent(issueType)}` : ""}`,
    ),
  jiraTaskLinks: (taskId: string, refresh = false) =>
    get<JiraTaskLinks>(`/integrations/jira/tasks/${seg(taskId)}/links${refresh ? "?refresh=1" : ""}`),
  jiraExportTask: (taskId: string, scope: "open" | "all", target?: JiraTarget) =>
    post<JiraTaskExport>(`/integrations/jira/tasks/${seg(taskId)}/export`, { scope, ...target }),
  // Without a target the finding goes to its task's epic space, or the defaults.
  jiraExportFinding: (findingId: number | string, target?: JiraTarget) =>
    post<{ issue: JiraLink; epic: JiraLink | null }>(
      `/integrations/jira/findings/${seg(String(findingId))}/export`,
      target,
    ),
  jiraSync: () => post<{ updated: number }>("/integrations/jira/sync"),
  deepenFinding: (id: string, description: string) =>
    post<FindingDeepenResponse>(`/exploration/findings/${seg(id)}/deepen`, { description }),
  intents: (task?: string) => get<TaskNode[]>(`/exploration/intents${tq(task)}`).then(arr),
  tokenStats: (task?: string) =>
    get<{ workers: TokenUsage[]; sessions: SessionTokenUsage[]; total: TokenTotal }>(
      `/exploration/tokens${tq(task)}`,
    ).then((r) => ({
      workers: arr(r.workers),
      sessions: arr(r.sessions),
      total: r.total,
    })),
  tokenDaily: (days = 30) => get<DailyTokenBucket[]>(`/tokens/daily?days=${encodeURIComponent(days)}`).then(arr),
  conversationTokens: () =>
    get<ConvTokenSummary[]>("/tokens/conversations")
      .then(arr)
      .catch(() => [] as ConvTokenSummary[]),
  explorationGraph: (task?: string) => get<{ nodes: TaskNode[]; edges: Edge[] }>(`/exploration/graph${tq(task)}`),
  // Broadcast board: server-paginated exploration nodes by creation time, newest first by default.
  explorationNodes: (task: string, query: ExplorationNodeQuery = {}) => {
    const q = new URLSearchParams();
    if (task) q.set("task", task);
    q.set("page", String(query.page ?? 1));
    q.set("size", String(query.size ?? 20));
    if (query.kinds?.length) q.set("kind", query.kinds.join(","));
    if (query.states?.length) q.set("state", query.states.join(","));
    if (query.q?.trim()) q.set("q", query.q.trim());
    if (query.order === "asc") q.set("order", "asc");
    return get<ExplorationNodePage>(`/exploration/nodes?${q.toString()}`).then((r) => ({
      items: arr(r.items),
      total: r.total ?? 0,
      page: r.page ?? 1,
      size: r.size ?? query.size ?? 20,
      edges: arr(r.edges),
      refs: r.refs ?? {},
      assets: r.assets ?? {},
    }));
  },
  activity: (task?: string, opts?: { intent?: string; since?: number; limit?: number }) => {
    const q = new URLSearchParams();
    if (task) q.set("task", task);
    if (opts?.intent) q.set("intent", opts.intent);
    if (opts?.since) q.set("since", String(opts.since));
    if (opts?.limit) q.set("limit", String(opts.limit));
    return get<{ items: Activity[]; cursor: number }>(`/exploration/activity?${q.toString()}`).then((r) => ({
      items: arr(r.items),
      cursor: r.cursor ?? 0,
    }));
  },
  activityDetail: (id: number, task?: string) => get<{ detail: string }>(`/exploration/activity/${seg(id)}${tq(task)}`),
  // Reverse-paginated session history. session = "main" | "plan" | "intent:<id>".
  // before=0 → latest page; before=<id> → the older page ending before that id.
  // snapshotCursor is the TASK-level max id at query time — open the task SSE at
  // since=snapshotCursor so history (id≤cursor) and the live tail (id>cursor) meet
  // gap-free. hasMore = still-older steps exist (drives scroll-up loading).
  activityHistory: (task: string, session: string, before = 0, limit = 200) => {
    const q = new URLSearchParams({ session, limit: String(limit) });
    if (task) q.set("task", task);
    if (before > 0) q.set("before", String(before));
    return get<{ items: Activity[]; snapshot_cursor: number; earliest_cursor: number; has_more: boolean }>(
      `/exploration/activity/history?${q.toString()}`,
    ).then((r) => ({
      items: arr(r.items),
      snapshotCursor: r.snapshot_cursor ?? 0,
      earliestCursor: r.earliest_cursor ?? 0,
      hasMore: !!r.has_more,
    }));
  },
  // Paged worker (intent) session list — reaches past the legacy fixed 300 cap.
  // before=0 → newest page; before=<id> → older page. has_more = older intents exist.
  intentsPage: (task: string, before = 0, limit = 300) => {
    const q = new URLSearchParams({ limit: String(limit) });
    if (task) q.set("task", task);
    q.set("before", String(before > 0 ? before : 0));
    q.set("page", "1"); // marker so the backend returns the paged {items,has_more} shape
    return get<{ items: TaskNode[]; has_more: boolean }>(`/exploration/intents?${q.toString()}`).then((r) => ({
      items: arr(r.items),
      hasMore: !!r.has_more,
    }));
  },

  // Main-agent conversation segments of a task. Each segment is a resettable session
  // (clean transcript/context) over the same task; `current` is the writable one.
  mainSessions: (task: string) =>
    get<{ sessions: { seq: number; created_at: string }[]; current: number }>(
      `/exploration/main-sessions${tq(task)}`,
    ).then((r) => ({ sessions: arr(r.sessions), current: r.current ?? 0 })),
  // Start a fresh main-agent session segment (does not touch the task's graph/assets/goal).
  newMainSession: (task: string) =>
    post<{ seq: number; created_at: string; current: number }>(`/exploration/main-session/new${tq(task)}`),

  // ---- traffic / audit / report / chat ----
  audit: (task?: string) => get<Audit>(`/audit${tq(task)}`),
  traffic: (
    page = 0,
    size = 100,
    host = "",
    method = "",
    q = "",
    opts: {
      body?: string;
      path?: string;
      status?: string;
      respMin?: string;
      respMax?: string;
      sort?: string;
      order?: string;
    } = {},
  ) =>
    get<TrafficResp>(
      `/traffic?page=${encodeURIComponent(page)}&size=${encodeURIComponent(size)}` +
        (host ? `&host=${encodeURIComponent(host)}` : "") +
        (method && method !== "all" ? `&method=${encodeURIComponent(method)}` : "") +
        (q ? `&q=${encodeURIComponent(q)}` : "") +
        (opts.body ? `&body=${encodeURIComponent(opts.body)}` : "") +
        (opts.path ? `&path=${encodeURIComponent(opts.path)}` : "") +
        (opts.status && opts.status !== "all" ? `&status=${encodeURIComponent(opts.status)}` : "") +
        (opts.respMin ? `&resp_min=${encodeURIComponent(opts.respMin)}` : "") +
        (opts.respMax ? `&resp_max=${encodeURIComponent(opts.respMax)}` : "") +
        (opts.sort ? `&sort=${encodeURIComponent(opts.sort)}` : "") +
        (opts.order ? `&order=${encodeURIComponent(opts.order)}` : ""),
    ),
  trafficExchange: (id: string) => get<TrafficDetail>(`/traffic/exchange?id=${encodeURIComponent(id)}`),
  trafficHosts: () => get<{ hosts: TrafficHost[] }>(`/traffic/hosts`),
  trafficDeleteHost: (host: string) => del<{ deleted: number }>(`/traffic?host=${encodeURIComponent(host)}`),
  trafficDeleteHosts: (hosts: string[]) => del<{ deleted: number }>(`/traffic/hosts`, { hosts }),
  // Purges every exchange and compacts the index; `reclaimed` is the bytes of
  // index handed back to the filesystem. Evidence bound to findings is kept.
  trafficDeleteAll: () => del<{ deleted: number; reclaimed: number }>(`/traffic/all`),

  // ---- app settings (runtime toggles) ----
  // Execution isolation status. 404 on servers that predate the feature.
  executionStatus: () => get<ExecutionStatus>("/execution/status"),
  recheckExecution: () => post<ExecutionStatus>("/execution/recheck"),
  // Supplementary scanner leads of a code review, and the global scanner settings. 404 on older servers.
  taskScan: (id: string) => get<TaskScan>(`/tasks/${seg(id)}/scan`),
  // Authorization check of a task: test accounts, policy and runs. 404 on older servers.
  // An account's password and token are write-only. On update: absent keeps the stored value, "" removes it.
  taskAuthz: (id: string) =>
    get<TaskAuthz>(`/tasks/${seg(id)}/authz`).then((r) => ({
      ...r,
      accounts: arr(r.accounts),
      runs: arr(r.runs),
    })),
  // `agent_access` lets the task's agents send requests as the role: absent is false on create, unchanged on update.
  createAuthzAccount: (
    id: string,
    body: { role: string; username?: string; password?: string; token?: string; agent_access?: boolean },
  ) => post<AuthzAccount>(`/tasks/${seg(id)}/authz/accounts`, body),
  updateAuthzAccount: (
    id: string,
    accountId: number,
    body: { role?: string; username?: string; password?: string; token?: string; agent_access?: boolean },
  ) => patch<AuthzAccount>(`/tasks/${seg(id)}/authz/accounts/${seg(accountId)}`, body),
  deleteAuthzAccount: (id: string, accountId: number) =>
    del<void>(`/tasks/${seg(id)}/authz/accounts/${seg(accountId)}`),
  saveAuthzPolicy: (id: string, policy: string) =>
    put<{ policy_updated_at: string; check: AuthzPolicyCheck; document?: AuthzDocument | null }>(
      `/tasks/${seg(id)}/authz/policy`,
      { policy },
    ),
  startAuthzRun: (id: string) => post<{ running: boolean }>(`/tasks/${seg(id)}/authz/runs`),
  authzRun: (id: string, runId: number) => get<AuthzRun>(`/tasks/${seg(id)}/authz/runs/${seg(runId)}`),
  scanSettings: () => get<ScanSettings>("/scan/settings"),
  setScanSettings: (body: { enabled?: boolean; paths?: Record<string, string | null> }) =>
    put<ScanSettings>("/scan/settings", body),
  settings: () => get<Settings>(`/settings`),
  setSettings: (patch: Partial<Settings>) => put<Settings>(`/settings`, patch),
  // Run a real "test" search with the given (or saved) config to verify it works.
  testWebSearch: (patch: {
    web_search_backend?: string;
    web_search_proxy?: string;
    brave_search_api_key?: string;
    tavily_search_api_key?: string;
  }) => post<{ ok: boolean; error?: string; count?: number; backend?: string }>(`/settings/web-search/test`, patch),

  // Vulnerability instant-message notifications.
  // Channels are multi-instance resources: multiple bots per type with independent filters,
  // so manage them separately rather than flattening them into settings.
  notifyMeta: () => get<NotificationMeta>(`/notify/meta`),
  notifyChannels: () => get<{ channels: NotificationChannel[] }>(`/notify/channels`).then((r) => arr(r.channels)),
  notifyCreateChannel: (payload: {
    name: string;
    kind: string;
    enabled?: boolean;
    mode?: string;
    config: Record<string, unknown>;
    filter?: NotificationFilter;
    rate_per_min?: number;
  }) => post<{ id: number }>(`/notify/channels`, payload),
  // PATCH submits changed fields only; unchanged masked config values preserve stored credentials.
  notifyUpdateChannel: (
    id: number,
    payload: {
      name?: string;
      kind?: string;
      enabled?: boolean;
      mode?: string;
      config?: Record<string, unknown>;
      filter?: NotificationFilter;
      rate_per_min?: number;
    },
  ) => patch<{ id: number }>(`/notify/channels/${seg(id)}`, payload),
  notifyDeleteChannel: (id: number) => del<{ ok: boolean }>(`/notify/channels/${seg(id)}`),
  // Send a test synchronously; raw channel errors are returned for configuration diagnosis.
  notifyTestChannel: (id: number) => post<{ ok: boolean; latency_ms: number }>(`/notify/channels/${seg(id)}/test`),
  notifyDeliveries: (q: { channelId?: number; state?: string; page?: number; pageSize?: number } = {}) => {
    const p = new URLSearchParams();
    if (q.channelId) p.set("channel_id", String(q.channelId));
    if (q.state) p.set("state", q.state);
    p.set("page", String(q.page ?? 1));
    p.set("page_size", String(q.pageSize ?? 50));
    return get<{ deliveries: NotificationDelivery[]; total: number; page: number; page_size: number }>(
      `/notify/deliveries?${p.toString()}`,
    ).then((r) => ({ ...r, deliveries: arr(r.deliveries) }));
  },
  notifyRetryDelivery: (id: number) => post<{ ok: boolean }>(`/notify/deliveries/${seg(id)}/retry`),
  report: async (task?: string) => {
    if (MOCK) return mockReport(task);
    const token = getToken();
    const r = await fetch(`/api/report${tq(task)}`, {
      headers: token ? { Authorization: `Bearer ${token}` } : {},
    });
    if (!r.ok) throw new Error(`report: ${r.status}`);
    return r.text();
  },
  // reportPdf renders the task report server-side; credentials are redacted by default.
  // reportView is the structured report the Report tab lays out; Markdown stays for download/copy.
  reportView: (task?: string) => get<ReportView>(`/report${tq(task)}${task ? "&" : "?"}format=json`),
  // Options: variant "technical" (default) or "executive"; models names the models
  // used; secrets disables credential and local-path redaction.
  reportPdf: async (
    task?: string,
    opts: { variant?: "technical" | "executive"; models?: boolean; secrets?: boolean } = {},
  ) => {
    if (MOCK) throw new Error("PDF export needs the backend");
    const token = getToken();
    const params = new URLSearchParams({ format: "pdf" });
    if (opts.variant === "executive") params.set("variant", "executive");
    if (opts.models) params.set("models", "include");
    if (opts.secrets) params.set("secrets", "include");
    const r = await fetch(`/api/report${tq(task)}${task ? "&" : "?"}${params}`, {
      headers: token ? { Authorization: `Bearer ${token}` } : {},
    });
    if (!r.ok) throw new Error(`report pdf: ${r.status}`);
    return r.blob();
  },
  // reportSarif downloads the task findings as SARIF 2.1.0 for GitHub code scanning and similar tools.
  reportSarif: async (task?: string) => {
    if (MOCK) throw new Error("SARIF export needs the backend");
    const token = getToken();
    const r = await fetch(`/api/report${tq(task)}${task ? "&" : "?"}format=sarif`, {
      headers: token ? { Authorization: `Bearer ${token}` } : {},
    });
    if (!r.ok) throw new Error(`report sarif: ${r.status}`);
    return r.blob();
  },
  chatMentions: (kind: string, query: string, signal?: AbortSignal, cursor = "") =>
    http<{ items: ChatMention[]; next_cursor?: string }>(
      `/chat/mentions?${new URLSearchParams({ kind, q: query, cursor })}`,
      { signal },
    ),
  chat: (message: string, task?: string, attachments?: ChatAttachment[], seg?: number) =>
    post<{ reply: string; mode: string }>(`/chat${tq(task)}`, { message, attachments, seg }),
  chatStatus: (taskId: string) => get<{ running: boolean }>(`/tasks/${seg(taskId)}/chat/status`),
  // Uploads enter session/task uploads/ directories and return relative paths usable by Agent Read.
  chatUpload: async (scope: "task" | "session" | "staging", id: string, files: File[]) => {
    if (MOCK)
      return {
        attachments: files.map((f) => ({
          name: f.name,
          path: `uploads/${f.name}`,
          size: f.size,
          abs: `/mock/${f.name}`,
        })),
      };
    const fd = new FormData();
    for (const f of files) fd.append("file", f);
    const token = getToken();
    const r = await fetch(`/api/chat/upload?scope=${encodeURIComponent(scope)}&id=${encodeURIComponent(id)}`, {
      method: "POST",
      headers: { ...(token ? { Authorization: `Bearer ${token}` } : {}) },
      body: fd,
    });
    if (!r.ok) throw new Error(swt("interface.m2419", { p0: r.status, p1: await r.text() }));
    return r.json() as Promise<{ attachments: ChatAttachment[] }>;
  },
  stopChat: (taskId: string) => post<{ status: string }>(`/tasks/${seg(taskId)}/chat/stop`, {}),
  gc: (ttl = 86400) => post<{ removed: number }>(`/gc?ttl=${encodeURIComponent(ttl)}`, {}),

  // ---- LLM ----
  getLLM: () =>
    get<{
      configured: boolean;
      provider: string;
      model: string;
      base_url: string;
      proxy?: string;
      key_set: boolean;
      rate_per_second?: number;
      rate_per_minute?: number;
      context_window_k?: number;
      thinking_type?: string;
      reasoning_effort?: string;
    }>("/llm"),
  setLLM: (
    provider: string,
    model: string,
    base_url: string,
    api_key: string,
    rate_per_second = 0,
    rate_per_minute = 0,
    proxy = "",
    context_window_k = 0,
  ) => post("/llm", { provider, model, base_url, proxy, api_key, rate_per_second, rate_per_minute, context_window_k }),
  testLLM: (
    provider: string,
    model: string,
    base_url: string,
    api_key: string,
    proxy = "",
    thinking_type = "",
    reasoning_effort = "",
    profile_id?: number,
    streaming = true, // Test the profile's actual transport mode so nonstreaming failures are caught before conversations.
    session_header_key = "", // Nonempty sends this custom header on test requests with a one-time session ID.
    subscription_id = "",
    cyber_access_program = "standard",
  ) =>
    // reply contains truncated actual model output; an entirely empty response fails the backend test.
    post<{ ok: boolean; error?: string; latency_ms?: number; model?: string; reply?: string }>("/llm/test", {
      provider,
      model,
      base_url,
      proxy,
      api_key,
      thinking_type,
      reasoning_effort,
      profile_id,
      streaming,
      session_header_key,
      subscription_id,
      cyber_access_program,
    }),
  chatGPTAccounts: () => get<{ accounts: ChatGPTAccount[] }>("/llm/subscription/accounts").then((r) => arr(r.accounts)),
  startChatGPTLogin: (subscription_id = "") => post<ChatGPTLogin>("/llm/subscription/start", { subscription_id }),
  chatGPTLoginStatus: (id: string) => get<ChatGPTLogin>(`/llm/subscription/attempts/${seg(id)}`),
  cancelChatGPTLogin: (id: string) => del<{ ok: boolean }>(`/llm/subscription/attempts/${seg(id)}`),
  llmProfiles: () => get<{ profiles: LLMProfile[] }>("/llm/profiles").then((r) => arr(r.profiles)),
  saveLLMProfile: (p: {
    id?: number; // omit/0 = create; set = update that profile
    name: string;
    format: string;
    model: string;
    base_url?: string;
    proxy?: string;
    api_key?: string; // blank on update keeps the existing key
    subscription_id?: string;
    cyber_access_program?: string;
    rate_per_second?: number;
    rate_per_minute?: number;
    context_window_k?: number;
    thinking_type?: string; // Empty string omits the field; otherwise disabled or enabled.
    reasoning_effort?: string; // Empty string omits the field; otherwise low/medium/high/xhigh/max.
    priority?: number; // Rotation priority, higher first.
    pool_exclude?: boolean; // True excludes this profile from failover targets.
    streaming?: boolean; // True (default) streams; false does not.
    max_tokens?: number; // Per-response output limit; 0 omits the field and uses the server default.
    max_tokens_field?: string; // Empty string defaults to max_tokens; max_completion_tokens applies only to OpenAI format.
    session_header_key?: string; // Nonempty header names send the current session ID on each request; empty omits the header.
    retry?: LLMRetryOverride; // Per-profile retry overrides; zeros inherit global retry policy.
    // Prices per million tokens. If any of the four keys is sent all four are saved as sent; none keeps the stored prices.
    price_input_per_million?: number | null;
    price_cached_input_per_million?: number | null;
    price_output_per_million?: number | null;
    price_currency?: string;
  }) => post<{ id: number }>("/llm/profiles", p),
  deleteLLMProfile: (id: string) => del<{ deleted: number }>(`/llm/profiles/${seg(id)}`),
  activateLLMProfile: (id: string) => post<{ ok: boolean }>("/llm/profiles/active", { id: Number(id) }),
  // Actual rotation order and circuit-breaker state per profile.
  llmPool: () => get<LLMPoolStatus>("/llm/pool"),
  // Clear a breaker to retry on the next call; omitted ID clears all.
  resetLLMPool: (id?: string) => post<LLMPoolStatus>("/llm/pool/reset", { id: id ? Number(id) : 0 }),
  // Global count/interval settings for five retry layers; all zeros use built-in defaults.
  llmRetryPolicy: () => get<LLMRetryPolicy>("/llm/retry-policy"),
  saveLLMRetryPolicy: (p: LLMRetryPolicy) => post<LLMRetryPolicy>("/llm/retry-policy", p),
  fetchLLMModels: (
    provider: string,
    base_url: string,
    api_key: string,
    proxy = "",
    profile_id?: number,
    subscription_id = "",
  ) =>
    post<{ ok: boolean; error?: string; models?: string[]; model_choices?: ChatGPTModel[] }>("/llm/models", {
      subscription_id,
      provider,
      base_url,
      api_key,
      proxy,
      profile_id,
    }),

  // ---- agents ----
  agents: () => get<{ agents: Agent[] }>("/agents").then((r) => arr(r.agents)),
  getAgent: (key: string) => get<AgentDetail>(`/agents/${seg(key)}`),
  createAgent: (key: string, name: string, description = "") => post<Agent>("/agents", { key, name, description }),
  updateAgent: (key: string, name: string, description = "") =>
    patch<{ ok: boolean }>(`/agents/${seg(key)}`, { name, description }),
  deleteAgent: (key: string) => del<{ deleted: string }>(`/agents/${seg(key)}`),

  // ---- conversations (chat page) ----
  conversations: () => get<{ conversations: Conversation[] }>("/conversations").then((r) => arr(r.conversations)),
  createConversation: (agent_key: string, title = "", llm_profile_id?: number | null) =>
    post<Conversation>("/conversations", { agent_key, title, llm_profile_id: llm_profile_id ?? null }),
  updateConversation: (id: number, input: { title?: string; pinned?: boolean }) =>
    patch<Conversation>(`/conversations/${seg(id)}`, input),
  renameConversation: (id: number, title: string) => patch<Conversation>(`/conversations/${seg(id)}`, { title }),
  pinConversation: (id: number, pinned: boolean) => patch<Conversation>(`/conversations/${seg(id)}`, { pinned }),
  updateConversationProfile: (id: number, llm_profile_id: number | null) =>
    patch<{ ok: boolean }>(`/conversations/${seg(id)}/profile`, { llm_profile_id }),
  deleteConversation: (id: number) => del<{ deleted: number }>(`/conversations/${seg(id)}`),
  deleteConversations: (ids: number[]) =>
    post<{ items: { id: number; ok: boolean; error?: string }[] }>("/conversations/delete/batch", { ids }),
  // Incremental tail: steps after `since` (id ASC) — live poll + post-send fetch.
  conversationMessages: (id: number, since = 0) =>
    get<{ items: Activity[]; cursor: number; running: boolean; finding_run?: ConversationFindingRun | null }>(
      `/conversations/${seg(id)}/messages?since=${encodeURIComponent(since)}`,
    ).then((r) => ({
      items: arr(r.items),
      cursor: r.cursor ?? 0,
      running: !!r.running,
      findingRun: r.finding_run ?? null,
    })),
  // Reverse pagination: the latest `limit` steps (before=0) or the page of older
  // steps ending before id `before`. hasMore = still-older steps exist.
  conversationHistory: (id: number, before = 0, limit = 200) => {
    const q = new URLSearchParams({ limit: String(limit) });
    if (before > 0) q.set("before", String(before));
    return get<{
      items: Activity[];
      cursor: number;
      running: boolean;
      hasMore: boolean;
      finding_run?: ConversationFindingRun | null;
    }>(`/conversations/${seg(id)}/messages?${q.toString()}`).then((r) => ({
      items: arr(r.items),
      cursor: r.cursor ?? 0,
      running: !!r.running,
      hasMore: !!r.hasMore,
      findingRun: r.finding_run ?? null,
    }));
  },
  conversationMsgDetail: (id: number, seq: number) =>
    get<{ detail: string }>(`/conversations/${seg(id)}/messages/${seg(seq)}`).then((r) => r.detail ?? ""),
  sendConversationMessage: (id: number, message: string, attachments?: ChatAttachment[]) =>
    post<{ status: string }>(`/conversations/${seg(id)}/messages`, { message, attachments }),
  stopConversation: (id: number) => post<{ status: string }>(`/conversations/${seg(id)}/stop`, {}),
  saveAgentPrompt: (key: string, template: string, note = "") =>
    put<{ version: number }>(`/agents/${seg(key)}/prompt`, { template, note }),
  resetAgentPrompt: (key: string) => post<{ version: number }>(`/agents/${seg(key)}/prompt/reset`, {}),
  // Settlement prompt for timeout/budget exhaustion; empty clears the override and uses the built-in prompt.
  // Omitted max_turns preserves it; 0 uses built-in round count.
  saveAgentWrapup: (key: string, prompt: string, maxTurns?: number) =>
    put<{ ok: boolean }>(`/agents/${seg(key)}/wrapup`, { prompt, max_turns: maxTurns }),
  resetAgentWrapup: (key: string) =>
    post<{ ok: boolean; wrapup_default: string; wrapup_max_turns_default: number }>(
      `/agents/${seg(key)}/wrapup/reset`,
      {},
    ),
  // Task-timeout wrap-up for worker/planner only; blank clears the override.
  saveAgentTaskTimeoutWrapup: (key: string, prompt: string, maxTurns?: number) =>
    put<{ ok: boolean }>(`/agents/${seg(key)}/wrapup/task-timeout`, { prompt, max_turns: maxTurns }),
  resetAgentTaskTimeoutWrapup: (key: string) =>
    post<{ ok: boolean; task_timeout_wrapup_default: string; task_timeout_wrapup_max_turns_default: number }>(
      `/agents/${seg(key)}/wrapup/task-timeout/reset`,
      {},
    ),
  // P3 triggers for custom Agents only.
  agentTriggers: (key: string) =>
    get<{ triggers: AgentTrigger[] }>(`/agents/${seg(key)}/triggers`).then((r) => arr(r.triggers)),
  createTrigger: (key: string, t: Omit<AgentTrigger, "id" | "agent_key" | "last_fire">) =>
    post<AgentTrigger>(`/agents/${seg(key)}/triggers`, t),
  updateTrigger: (id: number, t: Omit<AgentTrigger, "id" | "agent_key" | "last_fire">) =>
    patch<{ ok: boolean }>(`/triggers/${seg(id)}`, t),
  deleteTrigger: (id: number) => del<{ deleted: number }>(`/triggers/${seg(id)}`),
  saveAgentConfig: (
    key: string,
    patch: {
      llm_profile_id?: number | null; // Number binds a profile, null unbinds to inherited task/global settings, omission preserves.
      max_turns?: number;
      run_seconds?: number;
      web_search?: boolean;
      interactive_shell?: boolean;
      trigger_run_mode?: "serial" | "parallel";
      trigger_merge_mode?: "by_task" | "all" | "none";
      trigger_max_parallel?: number;
    },
  ) => put<{ ok: boolean }>(`/agents/${seg(key)}/config`, patch),
  agentPromptVersions: (key: string) =>
    get<{ versions: PromptVersion[] }>(`/agents/${seg(key)}/prompts`).then((r) => arr(r.versions)),
  agentVariables: (key: string) =>
    get<{ variables: PromptVar[] }>(`/agents/${seg(key)}/variables`).then((r) => arr(r.variables)),
  previewAgentPrompt: (key: string, template: string, sample?: Record<string, string>) =>
    post<{ rendered: string; error?: string }>(`/agents/${seg(key)}/prompt/preview`, { template, sample }),
  getAgentVisibility: (key: string) => get<{ mcp: number[]; skill: string[] }>(`/agents/${seg(key)}/visibility`),
  setAgentVisibility: (key: string, mcp: number[], skill: string[]) =>
    put<{ ok: boolean }>(`/agents/${seg(key)}/visibility`, { mcp, skill }),

  // Built-in tool catalog.
  tools: () => get<{ tools: Tool[] }>("/tools").then((r) => arr(r.tools)),
  saveTool: (key: string, patch: Pick<Tool, "description" | "schema" | "agents" | "enabled">) =>
    put<{ ok: boolean }>(`/tools/${seg(key)}`, patch),
  resetTool: (key: string) => post<{ ok: boolean }>(`/tools/${seg(key)}/reset`, {}),
  // Custom tools.
  createCustomTool: (
    t: Pick<Tool, "key" | "description" | "schema" | "agents" | "enabled" | "kind" | "exec" | "deferred">,
  ) => post<{ key: string }>("/tools/custom", t),
  updateCustomTool: (
    key: string,
    t: Pick<Tool, "description" | "schema" | "agents" | "enabled" | "kind" | "exec" | "deferred">,
  ) => put<{ ok: boolean }>(`/tools/custom/${seg(key)}`, t),
  deleteCustomTool: (key: string) => del<{ deleted: string }>(`/tools/custom/${seg(key)}`),
  testCustomTool: (body: { kind: string; exec: Record<string, unknown>; params: Record<string, unknown> }) =>
    post<{ output: string; is_error: boolean }>("/tools/custom/test", body),
  detectPython: () => post<{ python_interpreter: string }>("/settings/python/detect", {}),

  // ---- mcp ----
  mcpServers: () => get<{ servers: MCPServer[] }>("/mcp").then((r) => arr(r.servers)),
  saveMcpServer: (m: Partial<MCPServer>) => post<{ id: number }>("/mcp", m),
  deleteMcpServer: (id: number) => del<{ deleted: number }>(`/mcp/${seg(id)}`),
  mcpTools: (id: number) => get<{ tools: MCPTool[] }>(`/mcp/${seg(id)}/tools`).then((r) => arr(r.tools)),
  refreshMcpServer: (id: number) => post<{ tools: MCPTool[] }>(`/mcp/${seg(id)}/refresh`, {}).then((r) => arr(r.tools)),

  // Asset synchronization from ScopeSentry.
  ssStatus: () =>
    get<{ exists: boolean; configured: boolean; enabled: boolean; reachable: boolean; url?: string; tools: string[] }>(
      "/sync/scopesentry/status",
    ),
  ssDatasource: (body: { url?: string; api_key?: string }) =>
    post<{ id: number; enabled: boolean }>("/sync/scopesentry/datasource", body),
  ssProjects: (page = 1, size = 50, search = "") =>
    get<{ projects: SSProject[]; tag: Record<string, number> }>(
      `/sync/scopesentry/projects?page=${encodeURIComponent(page)}&size=${encodeURIComponent(size)}${search ? `&search=${encodeURIComponent(search)}` : ""}`,
    ).then((r) => ({ projects: arr(r.projects), tag: r.tag ?? {} })),
  ssTasks: (page = 1, size = 50, search = "") =>
    get<{ tasks: SSTask[] }>(
      `/sync/scopesentry/tasks?page=${encodeURIComponent(page)}&size=${encodeURIComponent(size)}${search ? `&search=${encodeURIComponent(search)}` : ""}`,
    ).then((r) => arr(r.tasks)),
  ssSync: (body: {
    dimension: "project" | "task";
    targets: string[];
    asset_types: string[];
    create_company?: boolean;
    page_size?: number;
  }) =>
    post<{
      synced: Record<string, number>;
      companies: string[] | null;
      warnings: string[] | null;
      errors: string[] | null;
    }>("/sync/scopesentry/sync", body),

  // Filesystem skills.
  skills: () => get<{ skills: SkillItem[] }>("/skills").then((r) => arr(r.skills)),
  createSkill: (s: {
    name: string;
    description: string;
    license?: string;
    compatibility?: string;
    mcps?: string[];
    instructions?: string;
  }) => post<{ name: string }>("/skills", s),
  // uploadSkill installs a skill from a .zip (multipart). Surfaces the backend
  // Error text, such as already exists or missing SKILL.md, gives the UI a precise message.
  uploadSkill: async (file: File, overwrite = false): Promise<{ name: string; files: number }> => {
    if (MOCK) return { name: file.name.replace(/\.zip$/i, ""), files: 1 };
    const fd = new FormData();
    fd.append("file", file);
    const token = getToken();
    const r = await fetch(`/api/skills/upload${overwrite ? "?overwrite=true" : ""}`, {
      method: "POST",
      body: fd,
      headers: token ? { Authorization: `Bearer ${token}` } : {},
    });
    const body = await r.json().catch(() => ({}));
    if (!r.ok) throw new Error(body?.error || swt("interface.m2420", { p0: r.status }));
    return body;
  },
  deleteSkill: (name: string) => del<{ deleted: string }>(`/skills/${seg(name)}`),
  updateSkillMeta: (
    name: string,
    meta: { mcps?: string[]; description?: string; license?: string; compatibility?: string },
  ) => put<{ ok: boolean }>(`/skills/${seg(name)}/meta`, meta),
  createSkillDir: (skill: string, path: string) => post<{ dir: string }>(`/skills/${seg(skill)}/dirs`, { path }),
  skillFiles: (name: string) => get<{ files: string[] }>(`/skills/${seg(name)}/files`).then((r) => r.files),
  readSkillFile: (name: string, file: string) =>
    get<{ content: string; file: string }>(`/skills/${seg(name)}/files/${segPath(file)}`).then((r) => r.content),
  writeSkillFile: (name: string, file: string, content: string) =>
    put<{ ok: boolean }>(`/skills/${seg(name)}/files/${segPath(file)}`, { content }),
  deleteSkillPath: (skill: string, path: string) =>
    del<{ deleted: string }>(`/skills/${seg(skill)}/files/${segPath(path)}`),
  skillUsage: (name: string, limit = 50) =>
    get<{ calls: SkillCall[] }>(`/skills/${seg(name)}/usage?limit=${encodeURIComponent(limit)}`).then((r) =>
      arr(r.calls),
    ),
  missingSkills: (limit = 20) =>
    get<{ missing: MissingSkill[] }>(`/skills/missing?limit=${encodeURIComponent(limit)}`).then((r) => arr(r.missing)),

  // ---- visibility (MCP resource side) ---- (agent ids are strings per spec)
  resourceVisibility: (kind: string, id: number) =>
    get<{ agents: string[] }>(`/visibility/${seg(kind)}/${seg(id)}`).then((r) => arr(r.agents)),
  toggleVisibility: (agentId: string, kind: string, resourceId: number, visible: boolean) =>
    post<{ ok: boolean }>("/visibility/toggle", { agent_id: agentId, kind, resource_id: resourceId, visible }),

  // Skill visibility by name.
  skillVisibility: (name: string) =>
    get<{ agents: string[] }>(`/visibility/skill/${seg(name)}`).then((r) => arr(r.agents)),
  toggleSkillVisibility: (agentId: string, skillName: string, visible: boolean) =>
    post<{ ok: boolean }>("/visibility/skill/toggle", { agent_id: agentId, skill_name: skillName, visible }),

  // ---- intercept rules ----
  interceptRules: () => get<{ rules: InterceptRule[] }>("/intercept/rules").then((r) => arr(r.rules)),
  createInterceptRule: (rule: Omit<InterceptRule, "id" | "created_at" | "updated_at">) =>
    post<InterceptRule>("/intercept/rules", rule),
  updateInterceptRule: (id: number, rule: Omit<InterceptRule, "id" | "created_at" | "updated_at">) =>
    put<InterceptRule>(`/intercept/rules/${seg(id)}`, rule),
  deleteInterceptRule: (id: number) => del<{ deleted: number }>(`/intercept/rules/${seg(id)}`),
  toggleInterceptRule: (id: number, enabled: boolean) =>
    post<{ ok: boolean; enabled: boolean }>(`/intercept/rules/${seg(id)}/toggle`, { enabled }),

  // Global asset blocklist rules.
  assetInterceptRules: () => get<{ rules: AssetInterceptRule[] }>("/asset-intercept/rules").then((r) => arr(r.rules)),
  createAssetInterceptRule: (rule: Pick<AssetInterceptRule, "enabled" | "kind" | "pattern" | "note">) =>
    post<AssetInterceptRule>("/asset-intercept/rules", rule),
  updateAssetInterceptRule: (id: number, rule: Pick<AssetInterceptRule, "enabled" | "kind" | "pattern" | "note">) =>
    put<AssetInterceptRule>(`/asset-intercept/rules/${seg(id)}`, rule),
  deleteAssetInterceptRule: (id: number) => del<{ deleted: number }>(`/asset-intercept/rules/${seg(id)}`),
  toggleAssetInterceptRule: (id: number, enabled: boolean) =>
    post<{ ok: boolean; enabled: boolean }>(`/asset-intercept/rules/${seg(id)}/toggle`, { enabled }),

  // ---- intercept pending (ask) ----
  interceptPending: () => get<{ pending: InterceptPending[] }>("/intercept/pending").then((r) => arr(r.pending)),
  interceptGetOne: (id: number) => get<InterceptPending>(`/intercept/pending/${seg(id)}`),
  interceptDecide: (
    id: number,
    decision: "allowed" | "denied",
    grant?: "once" | "command" | "input" | "tool" | "all",
  ) => post<{ ok: boolean }>(`/intercept/pending/${seg(id)}/decide`, { decision, grant }),
  toolGrants: () => get<{ grants: TaskToolGrant[] }>("/intercept/grants").then((r) => arr(r.grants)),
  taskToolGrants: (taskId: string) =>
    get<{ grants: TaskToolGrant[] }>(`/intercept/task/${seg(taskId)}/grants`).then((r) => arr(r.grants)),
  revokeTaskToolGrant: (taskId: string, id: number) =>
    del<{ ok: boolean }>(`/intercept/task/${seg(taskId)}/grants/${seg(id)}`),
  interceptExecution: (id: number, conversationId?: number) =>
    get<import("@/lib/types").InterceptExecution>(
      `/intercept/history/${seg(id)}/execution${conversationId ? `?conversation=${encodeURIComponent(conversationId)}` : ""}`,
    ),
  interceptDetail: (id: number) => get<InterceptDetail>(`/intercept/history/${seg(id)}`),
  interceptHistory: () => get<{ items: InterceptApprovalRow[] }>("/intercept/history").then((r) => arr(r.items)),
  interceptHistoryPage: (page = 1, size = 20, filter: InterceptApprovalFilter = {}) =>
    get<{ items: InterceptApprovalRow[]; total?: number }>(
      `/intercept/history?${interceptPageQuery(page, size, filter)}`,
    ).then((r) => ({
      items: arr(r.items),
      total: r.total ?? r.items?.length ?? 0,
    })),
  interceptTask: (taskId: string) =>
    get<{ items: InterceptApprovalRow[] }>(`/intercept/task/${seg(taskId)}`).then((r) => arr(r.items)),
  interceptTaskPage: (taskId: string, page = 1, size = 20, filter: InterceptApprovalFilter = {}) =>
    get<{ items: InterceptApprovalRow[]; total?: number }>(
      `/intercept/task/${seg(taskId)}?${interceptPageQuery(page, size, filter)}`,
    ).then((r) => ({
      items: arr(r.items),
      total: r.total ?? r.items?.length ?? 0,
    })),

  // Global tool interception scope configuration.
  interceptGetToolConfig: async (): Promise<{ enabled_tools: string[] }> => {
    if (MOCK) return { enabled_tools: ["bash"] };
    const token = getToken();
    const r = await fetch("/api/intercept/tool-config", {
      headers: token ? { Authorization: `Bearer ${token}` } : {},
    });
    if (!r.ok) throw new Error(await r.text());
    return r.json();
  },
  interceptSetToolConfig: async (enabledTools: string[]): Promise<void> => {
    if (MOCK) return;
    const token = getToken();
    const r = await fetch("/api/intercept/tool-config", {
      method: "PUT",
      headers: {
        "Content-Type": "application/json",
        ...(token ? { Authorization: `Bearer ${token}` } : {}),
      },
      body: JSON.stringify({ enabled_tools: enabledTools }),
    });
    if (!r.ok) throw new Error(await r.text());
  },

  // Global model fallback approval configuration.
  interceptGetJudgeConfig: () => get<JudgeConfig>("/intercept/judge"),
  interceptSetJudgeConfig: (cfg: JudgeConfig) => put<{ ok: boolean }>("/intercept/judge", cfg),

  // ---- commands (tool execution history, any tool) ----
  commands: (params?: { task?: string; q?: string; page?: number; size?: number }) => {
    const sp = new URLSearchParams();
    if (params?.task) sp.set("task", params.task);
    if (params?.q) sp.set("q", params.q);
    sp.set("page", String(params?.page ?? 0));
    sp.set("size", String(params?.size ?? 50));
    return get<{ commands: CommandRecord[]; total: number }>(`/commands?${sp}`);
  },
  // Tool call counts use list task/query filters across the full result set, not just the page.
  commandStats: (params?: { task?: string; q?: string }) => {
    const sp = new URLSearchParams();
    if (params?.task) sp.set("task", params.task);
    if (params?.q) sp.set("q", params.q);
    return get<{ stats: ToolStat[] }>(`/commands/stats?${sp}`);
  },

  // ---- LLM records ----
  llmRecords: (params?: { model?: string; session?: string; task?: string; page?: number; size?: number }) => {
    const sp = new URLSearchParams();
    if (params?.model) sp.set("model", params.model);
    if (params?.session) sp.set("session", params.session);
    if (params?.task) sp.set("task", params.task);
    if (params?.page !== undefined) sp.set("page", String(params.page));
    sp.set("size", String(params?.size ?? 50));
    return get<{ records: LLMRecordItem[]; total: number }>(`/llm/records?${sp}`);
  },
  llmRecordDetail: (id: number) => get<LLMRecordDetail>(`/llm/records/${seg(id)}`),
  llmTasks: () => get<{ tasks: LLMTask[] }>(`/llm/records/tasks`),
  llmRecordsDeleteTask: (task: string) => del<{ deleted: number }>(`/llm/records?task=${encodeURIComponent(task)}`),
  // Per-model task token totals from the always-on, precise per-call llm_usage ledger,
  // including Agent bindings, rotation, and interrupted calls.
  tokensByModel: (task: string) =>
    get<{ models: ModelTokenStat[] }>(`/llm/records/by-model?task=${encodeURIComponent(task)}`),

  // One-click updates.
  // Trust backend connectivity checks because it performs downloads. The browser may reach GitHub
  // while an internal server or browser-only proxy cannot, in which case updating must fail.
  // Cache GitHub results for 30 minutes; unauthenticated requests are limited to 60/hour/IP.
  // Header checks on full-page loads would otherwise exhaust the quota. force=true bypasses cache
  // for explicit user update checks.
  checkUpdate: (force = false) => get<UpdateCheck>(`/update/check${force ? "?force=1" : ""}`),
  // Return 202 immediately; download in the background and stream progress through /api/update/stream.
  applyUpdate: () => post<{ ok: boolean; target: string }>(`/update/apply`),
  rollbackUpdate: () => post<{ ok: boolean }>(`/update/rollback`),

  // One-click installer for optional recon tools (subfinder/httpx/naabu/dnsx).
  listReconTools: () => get<{ tools: ReconToolStatus[] }>(`/recontools/status`),
  // Return 202 immediately; download in the background and stream progress through /api/recontools/stream.
  installReconTool: (tool: string) => post<{ ok: boolean; tool: string }>(`/recontools/install`, { tool }),
};
