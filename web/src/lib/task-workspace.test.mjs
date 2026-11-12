import {
  assessmentTypeOf,
  latestSpawnedTask,
  nodeSummary,
  TASK_TAB_GROUPS,
  TASK_WORKSPACES,
  taskGroupTabs,
  taskTabGroup,
  taskWorkspaceHref,
  validTaskTab,
  visibleTaskTabs,
} from "./task-workspace.ts";
import assert from "node:assert/strict";
import test from "node:test";

test("task navigation preserves all original workspaces and encodes untrusted IDs", () => {
  for (const tab of [
    "sessions",
    "overview",
    "graph",
    "broadcast",
    "findings",
    "retests",
    "assets",
    "coverage",
    "intercept",
    "report",
  ]) {
    assert.ok(TASK_WORKSPACES.some((item) => item.value === tab));
    assert.equal(validTaskTab(tab), tab);
  }
  assert.equal(validTaskTab("javascript:alert(1)"), "sessions");
  assert.equal(
    taskWorkspaceHref("1&tab=report", "findings"),
    "/function/tasks/detail/?id=1%26tab%3Dreport&tab=findings",
  );
});

test("task tabs are grouped for navigation without changing their URL values", () => {
  assert.deepEqual(TASK_TAB_GROUPS, ["run", "findings", "evidence", "scope", "approvals", "report"]);
  assert.deepEqual(
    TASK_TAB_GROUPS.map((group) => taskGroupTabs(group)),
    [
      ["sessions", "overview"],
      ["findings", "retests", "mitigation", "scan", "authz"],
      ["checks", "graph", "broadcast"],
      ["assets", "coverage"],
      ["intercept"],
      ["report"],
    ],
  );
  // Every tab belongs to exactly one group, so the groups cover all workspaces.
  assert.equal(TASK_TAB_GROUPS.flatMap((group) => taskGroupTabs(group)).length, TASK_WORKSPACES.length);
  for (const item of TASK_WORKSPACES) {
    assert.equal(validTaskTab(item.value), item.value);
    assert.ok(taskGroupTabs(taskTabGroup(item.value)).includes(item.value));
  }
  assert.equal(taskTabGroup("checks"), "evidence");
  assert.equal(taskTabGroup("mitigation"), "findings");
  assert.equal(taskTabGroup(null), "run");
  assert.equal(taskTabGroup("unknown"), "run");
});

test("only a successful spawn_task result can suggest a created task", () => {
  const result = { seq: 2, kind: "tool_result", tool: "spawn_task", summary: "task created: 42" };
  assert.equal(latestSpawnedTask([result]), "42");
  assert.equal(latestSpawnedTask([{ ...result, tool: "start_code_review" }]), "42");
  for (const override of [
    { kind: "user" },
    { kind: "text" },
    { is_error: true },
    { tool: "Bash" },
    { summary: "task created: 0" },
    { summary: "task created: 42 and then 43" },
  ]) {
    assert.equal(latestSpawnedTask([{ ...result, ...override }]), null);
  }
  assert.equal(
    latestSpawnedTask([
      { seq: 1, kind: "tool_use", tool: "spawn_task", tool_use_id: "call-1" },
      { seq: 2, kind: "tool_result", tool_use_id: "call-1", summary: "task created: 7" },
    ]),
    "7",
  );
});

test("recorded conclusions are displayed without inventing a security verdict", () => {
  assert.equal(nodeSummary('{"summary":"Request timed out"}'), "Request timed out");
  assert.equal(nodeSummary('{"text":"No issue reproduced in this attempt"}'), "No issue reproduced in this attempt");
  assert.equal(nodeSummary("Legacy raw evidence"), "Legacy raw evidence");
  assert.equal(nodeSummary(), "No recorded summary");
});

test("a missing or unknown assessment type counts as general", () => {
  assert.equal(assessmentTypeOf(null), "general");
  assert.equal(assessmentTypeOf({}), "general");
  assert.equal(assessmentTypeOf({ assessment_type: "pentest" }), "general");
  for (const type of ["general", "web_app", "network", "code_review"]) {
    assert.equal(assessmentTypeOf({ assessment_type: type }), type);
  }
});

test("code reviews hide scope, approval and authorization tabs but keep mitigation", () => {
  const { groups, tabs } = visibleTaskTabs("code_review");
  assert.deepEqual(groups, ["run", "findings", "evidence", "report"]);
  for (const hidden of ["assets", "coverage", "intercept", "authz"]) assert.ok(!tabs.includes(hidden));
  assert.ok(tabs.includes("mitigation"));
  assert.ok(tabs.includes("scan"));
  assert.equal(tabs.length, TASK_WORKSPACES.length - 4);
});

test("the authorization check is offered for every type with a running target", () => {
  assert.equal(validTaskTab("authz"), "authz");
  assert.equal(taskTabGroup("authz"), "findings");
  for (const type of ["general", "web_app", "network", undefined]) {
    assert.ok(visibleTaskTabs(type).tabs.includes("authz"));
  }
  // Like every hidden tab, it stays reachable when opened by URL.
  assert.ok(visibleTaskTabs("code_review", "authz").tabs.includes("authz"));
});

test("other known types hide mitigation, and a missing type keeps it", () => {
  for (const type of ["general", "web_app", "network"]) {
    const all = visibleTaskTabs(type);
    // Scanner leads and mitigation exist only for code reviews.
    assert.ok(!all.tabs.includes("scan"));
    assert.ok(!all.tabs.includes("mitigation"));
    assert.equal(all.tabs.length, TASK_WORKSPACES.length - 2);
    assert.ok(all.tabs.includes("intercept"));
    assert.ok(all.groups.includes("findings"));
  }
  // Older backends and legacy tasks report no type: only scanner leads are hidden.
  const unknown = visibleTaskTabs(undefined);
  assert.deepEqual(unknown.groups, [...TASK_TAB_GROUPS]);
  assert.ok(unknown.tabs.includes("mitigation"));
  assert.ok(!unknown.tabs.includes("scan"));
  assert.equal(unknown.tabs.length, TASK_WORKSPACES.length - 1);
});

test("a hidden tab opened by URL stays reachable in the navigation", () => {
  assert.equal(validTaskTab("intercept"), "intercept");
  const { groups, tabs } = visibleTaskTabs("code_review", "intercept");
  assert.ok(tabs.includes("intercept"));
  assert.ok(groups.includes("approvals"));
  assert.ok(!tabs.includes("assets"));
  assert.ok(visibleTaskTabs("web_app", "mitigation").tabs.includes("mitigation"));
  // The scanner leads tab opens by URL for any type, and sits in the findings group.
  assert.equal(validTaskTab("scan"), "scan");
  assert.equal(taskTabGroup("scan"), "findings");
  assert.ok(visibleTaskTabs("web_app", "scan").tabs.includes("scan"));
});
