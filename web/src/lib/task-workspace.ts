import type { Activity, AssessmentType } from "./types";

// Navigation groups on the task detail page, in display order.
export const TASK_TAB_GROUPS = ["run", "findings", "evidence", "scope", "approvals", "report"] as const;
export type TaskTabGroup = (typeof TASK_TAB_GROUPS)[number];

// Keep existing task URLs stable, including bookmarks and approval source links.
// The `value` is the `tab` query parameter; `group` places the tab in the page navigation.
export const TASK_WORKSPACES = [
  { value: "sessions", group: "run", label: "Session", description: "Conversation and agent execution" },
  { value: "overview", group: "run", label: "Overview", description: "Objectives, scope and constraints" },
  {
    value: "checks",
    group: "evidence",
    label: "Results ledger",
    description: "Recorded conclusions and unfinished exploration",
  },
  { value: "graph", group: "evidence", label: "Exploration paths", description: "Intent and evidence relationships" },
  { value: "broadcast", group: "evidence", label: "Broadcast board", description: "The complete exploration record" },
  { value: "findings", group: "findings", label: "Findings", description: "Evidence, severity and review" },
  { value: "retests", group: "findings", label: "Retest", description: "Validation after remediation" },
  { value: "mitigation", group: "findings", label: "Mitigation", description: "Verified fixes and pull requests" },
  {
    value: "scan",
    group: "findings",
    label: "Scanner leads",
    description: "Automated leads from supplementary scanners",
  },
  {
    value: "authz",
    group: "findings",
    label: "Authorization",
    description: "Allowed and denied requests for every role",
  },
  { value: "assets", group: "scope", label: "Test assets", description: "Assets associated with this task" },
  { value: "coverage", group: "scope", label: "Asset coverage graph", description: "Assets with recorded results" },
  {
    value: "intercept",
    group: "approvals",
    label: "Interception approvals",
    description: "Pending actions and decisions",
  },
  { value: "report", group: "report", label: "Report", description: "Task report and export" },
] as const satisfies readonly { value: string; group: TaskTabGroup; label: string; description: string }[];

export type TaskTab = (typeof TASK_WORKSPACES)[number]["value"];

export function validTaskTab(value: string | null): TaskTab {
  return TASK_WORKSPACES.find((tab) => tab.value === value)?.value ?? "sessions";
}

// taskTabGroup returns the navigation group of a tab; unknown values fall back like validTaskTab.
export function taskTabGroup(value: string | null): TaskTabGroup {
  return TASK_WORKSPACES.find((item) => item.value === value)?.group ?? "run";
}

// taskGroupTabs lists a group's tabs in display order.
export function taskGroupTabs(group: TaskTabGroup): TaskTab[] {
  return TASK_WORKSPACES.filter((item) => item.group === group).map((item) => item.value);
}

// assessmentTypeOf normalizes a task's type; older backends omit it and unknown values count as general.
export function assessmentTypeOf(task?: { assessment_type?: string } | null): AssessmentType {
  const value = task?.assessment_type;
  return value === "web_app" || value === "network" || value === "code_review" ? value : "general";
}

// Tabs with no meaning for a code review: there is no live target to scope, cover, intercept or
// send authorization checks to.
const CODE_REVIEW_HIDDEN_TABS: readonly TaskTab[] = ["assets", "coverage", "intercept", "authz"];

// visibleTaskTabs returns the navigation groups and tabs to offer for an assessment type.
// Hidden tabs still open when addressed by URL (validTaskTab ignores visibility): pass the open
// tab as `openTab` and it is kept in the navigation while it is shown.
// Scanner leads only exist for code reviews, so other types hide that tab.
// Fix generation only exists for code reviews, so the other known types hide Mitigation; an
// undefined type (older backend or legacy task) keeps it visible.
export function visibleTaskTabs(
  type: AssessmentType | undefined,
  openTab?: TaskTab,
): { groups: TaskTabGroup[]; tabs: TaskTab[] } {
  const hidden = new Set<TaskTab>();
  if (type === "code_review") {
    for (const tab of CODE_REVIEW_HIDDEN_TABS) hidden.add(tab);
  } else {
    // Scanner leads exist only for code reviews.
    hidden.add("scan");
    if (type !== undefined) hidden.add("mitigation");
  }
  const tabs = TASK_WORKSPACES.filter((item) => item.value === openTab || !hidden.has(item.value)).map(
    (item) => item.value,
  );
  const groups = TASK_TAB_GROUPS.filter((group) => taskGroupTabs(group).some((tab) => tabs.includes(tab)));
  return { groups, tabs };
}

export function taskWorkspaceHref(id: string, tab = "sessions"): string {
  return `/function/tasks/detail/?${new URLSearchParams({ id, tab: validTaskTab(tab) })}`;
}

// Only a successful spawn_task or start_code_review response can suggest a task. Conversation
// prose and user-supplied identifiers must never masquerade as a created-task event.
export function latestSpawnedTask(activities: Activity[]): string | null {
  const calls = new Map(
    activities.filter((a) => a.kind === "tool_use" && a.tool_use_id).map((a) => [a.tool_use_id, a.tool]),
  );
  for (let i = activities.length - 1; i >= 0; i--) {
    const activity = activities[i];
    if (activity.kind !== "tool_result" || activity.is_error) continue;
    const tool = activity.tool || (activity.tool_use_id ? calls.get(activity.tool_use_id) : undefined);
    if (tool !== "spawn_task" && tool !== "start_code_review") continue;
    for (const text of [activity.detail, activity.summary]) {
      const match = text?.trim().match(/^task created:\s*([1-9]\d*)$/);
      if (match) return match[1];
    }
  }
  return null;
}

export function nodeSummary(payload?: string): string {
  if (!payload) return "No recorded summary";
  try {
    const value = JSON.parse(payload);
    if (value && typeof value === "object") {
      for (const key of ["summary", "text", "name", "description", "conclusion"]) {
        if (typeof value[key] === "string" && value[key].trim()) return value[key];
      }
    }
  } catch {
    /* Legacy nodes may contain plain text. */
  }
  return payload;
}
