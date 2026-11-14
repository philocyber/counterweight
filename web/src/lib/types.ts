// Counterweight domain model — types used across the UI.
// Derived from functional specification section 7, key data shapes.

export type TaskStatus =
  | "created"
  | "queued"
  | "running"
  | "waiting_approval"
  | "paused"
  | "done"
  | "failed"
  | "timeout";
export type EngineMode = "exploring" | "paused" | "stalled" | "idle";

// What a task assesses. Older backends omit the field; treat a missing value as "general".
export type AssessmentType = "general" | "web_app" | "network" | "code_review";
export type CodeReviewMode = "light" | "balanced" | "comprehensive";

// Repository a code-review task reads; the local clone path is never exposed.
export interface CodeTarget {
  task_id: number;
  repository: string; // owner/name
  url: string;
  ref?: string;
  commit_sha?: string;
  mode?: CodeReviewMode | string;
  created_at?: string;
  change?: CodeTargetChange; // Present when the review is scoped to a pull request or a base/head pair.
}

// The change a code review is scoped to. Commits are full 40-character ids; the file list is omitted in task lists.
export interface CodeTargetChange {
  base_ref?: string;
  base_commit: string;
  head_ref?: string;
  head_commit: string;
  merge_base: string;
  pull_number?: number;
  pull_url?: string;
  total_files: number;
  files_truncated?: boolean;
  diff_truncated?: boolean;
}

// Body of the optional `change` field of POST /api/tasks for a code review.
export interface ReviewChangeInput {
  pull_number?: number;
  base?: string;
  head?: string;
}

// An API token for automation (CI). The secret is returned only once, when the token is created.
export type ApiTokenScope = "reviews:write" | "reviews:read";

export interface ApiToken {
  id: number;
  name: string;
  scopes: ApiTokenScope[] | string[];
  hint: string; // First characters of the token, for recognition.
  created_at: string;
  expires_at?: string | null;
  last_used_at?: string | null;
  revoked_at?: string | null;
}

export interface Task {
  id: string;
  name?: string; // Optional task name; blank/omitted falls back to description for display.
  category_id?: number;
  category_name?: string;
  pinned?: boolean;
  pinned_at?: string | null;
  description: string;
  goal: string;
  status: TaskStatus;
  created_at: string;
  created_unix?: number; // created_at as unix seconds (run-duration calc)
  completed_at?: string; // RFC3339 finish time (done/failed); "" if unfinished
  completed_unix?: number; // completed_at as unix seconds (0/undef if unfinished)
  last_activity_unix?: number; // unix seconds of the last activity (0/undef if none)
  approval_state?: {
    // outcome "budget" and "execution" mark pauses the platform itself imposed; reason is set for those.
    holds: { id: number; started_at: string; ended_at: string | null; outcome: string; reason?: string }[];
    pending: { id: number; tool: string; agent: string; reason: string }[];
  };
  paused?: boolean;
  queued?: boolean;
  active?: boolean;
  in_flight?: number;
  findings?: { critical: number; high: number; medium: number; low: number }; // Registered finding counts by severity.
  last_activity?: string;
  stalled?: boolean;
  goals_total?: number;
  goals_met?: number;
  engine_mode?: EngineMode;
  tokens?: TokenTotal; // whole-task token consumption
  llm_profile_id?: number; // LLM profile used; absent = default profile
  llm_profile_ids?: number[]; // ordered task-level failover chain
  active_llm_profile_id?: number; // profile used by the next LLM call
  llm_failover_state?: "default" | "ready" | "chain_exhausted" | string;
  llm_failover_reason?: string;
  // Error of the last planning round while the task is active; absent once a round succeeds.
  planning_error?: string;
  source_task_ids?: string[]; // directly related tasks inherited as read-only context
  archive_blocked_by_task_id?: string; // live direct dependent that must be archived first
  company_ids?: number[]; // associated company scopes; current company assets join the task at creation
  coverage_enabled?: boolean; // Coverage feature switch set at creation, on by default; false disables calculation/display.
  assessment_type?: AssessmentType; // Absent on older backends; treated as "general".
  assessment_type_explicit?: boolean; // False for legacy tasks whose type was never chosen.
  code_target?: CodeTarget; // Present for code reviews.
  execution?: TaskExecution; // Absent on older backends: execution isolation is not recorded.
  budget?: TaskBudget; // Absent on older backends: no usage budget.
}

// What the platform confines while a task runs, as recorded for the task's latest run segment.
export interface ExecutionCapabilities {
  files_confined: boolean;
  processes_confined: boolean;
  network_confined: boolean;
  credentials_isolated?: boolean; // Absent on older backends.
}

export interface TaskExecution {
  mode: "host" | "confined-files" | "container" | string;
  capabilities: ExecutionCapabilities;
  mixed: boolean; // Recorded segments ran in different modes.
  assumed: boolean; // Nothing was recorded for this task (legacy or never run).
  image_digest?: string; // Image ID of the container the latest run used, when it ran commands in one.
  source_only?: boolean; // A source review: agents have no shell, so no command ran.
}

// One entry of ExecutionStatus.backends.
export interface ExecutionBackendStatus {
  backend: "host" | "confined-files" | "container" | string;
  status: "ready" | "not-available" | "failed" | string;
  reason?: string;
  fix?: string; // The single step that makes the backend ready.
  image?: string;
  image_digest?: string;
  last_self_test?: string;
}

// GET /api/execution/status and POST /api/execution/recheck.
export interface ExecutionStatus {
  setting: boolean;
  mode: string;
  capabilities: ExecutionCapabilities;
  status: "ready" | "off" | "failed" | string;
  reason?: string;
  last_self_test?: string;
  skipped?: string[];
  description?: string;
  // The fields below are absent on a backend that predates container mode.
  selected_mode?: "off" | "confined-files" | "container" | string;
  backends?: ExecutionBackendStatus[];
  unavailable_tool_families?: string[]; // Only filled while container mode is the selected one.
  tasks_on_earlier_mode?: string[]; // Running tasks that keep the mode they started with.
  image?: string;
  image_digest?: string;
}

// Per-task usage budget. A limit of 0 (tokens) or null (cost) means none.
export interface TaskBudget {
  token_budget: number;
  tokens_used: number;
  cost_budget: number | null;
  estimated_cost: number;
  currency: string;
  unknown_cost: boolean;
  percent_used: number;
  state: "ok" | "warning" | "reached" | string;
  paused_for_budget: boolean;
}

export type ScanLeadState = "new" | "delivered" | "confirmed" | "dismissed";

export interface ScannerStatus {
  key: string; // "osv-scanner" or "gitleaks"
  purpose: string;
  available: boolean;
  source?: string;
  reason?: string;
  step?: string;
}

export interface ScanRun {
  task_id: number;
  scanner: string;
  status: "running" | "ok" | "unavailable" | "failed" | string;
  reason?: string;
  tool_version?: string;
  leads_found: number;
  leads_stored: number;
  started_at: string;
  finished_at?: string;
}

// An automated pointer, not a finding. Secret values are never present.
export interface ScanLead {
  id: number;
  task_id: number;
  scanner: string;
  kind: "secret" | "advisory" | string;
  rule_id: string;
  title: string;
  file?: string;
  line?: number;
  package?: string;
  version?: string;
  fixed_version?: string;
  aliases?: string;
  detail?: string;
  fingerprint: string;
  state: ScanLeadState | string;
  finding_id?: number;
  resolution?: string;
  resolved_by?: string;
  created_at: string;
  delivered_at?: string;
  resolved_at?: string;
}

// GET /api/tasks/{id}/scan
export interface TaskScan {
  applicable: boolean;
  enabled: boolean;
  note: string;
  scanners: ScannerStatus[];
  runs: ScanRun[];
  leads: ScanLead[];
  counts: { new: number; delivered: number; confirmed: number; dismissed: number };
}

// GET/PUT /api/scan/settings
export interface ScanSettings {
  enabled: boolean;
  note: string;
  paths: Record<string, string>;
  scanners: ScannerStatus[];
}

// A test account of a task's authorization check. Its password and token are write-only:
// the server reports only whether each one is stored.
export interface AuthzAccount {
  id: number;
  key: string; // persona name in the policy; derived from the role once and never changed
  role: string;
  username: string;
  has_password: boolean;
  has_token: boolean;
  agent_access: boolean; // the task's agents may send requests as this role; they never get the secrets
  updated_at: string;
}

// Validation of the saved policy. `errors` are sentences written by the server.
export interface AuthzPolicyCheck {
  ok: boolean;
  errors?: string[] | null;
  personas?: string[] | null;
  cases: number;
  checks: number;
}

export interface AuthzCounts {
  cases: number;
  checks: number;
  passed: number;
  failed: number;
  inconclusive: number;
}

export interface AuthzRunSummary {
  id: number;
  result: "pass" | "fail" | "inconclusive" | "error" | string;
  exit_code: number;
  target?: string;
  started_at: string;
  duration_ms: number;
  counts: AuthzCounts;
}

// A policy as data (the server's reading of the policy text), for the matrix editor. References such
// as ${account:admin.password} stay references: a document never holds a credential.
export interface AuthzDocRequest {
  method?: string;
  path?: string;
  headers?: Record<string, string> | null;
  json?: unknown;
  form?: Record<string, string> | null;
  body?: string;
}

export interface AuthzDocPersona {
  key: string;
  role?: string;
  headers?: Record<string, string> | null;
  cookies?: Record<string, string> | null;
  login?: {
    request: AuthzDocRequest;
    status?: number[] | null;
    token?: { json_pointer?: string; header?: string; prefix?: string } | null;
  } | null;
  identity?: { request: AuthzDocRequest; status?: number[] | null; body_contains?: string[] | null } | null;
}

export interface AuthzDocExpectation {
  allow?: number[] | null;
  deny?: number[] | null;
  body_contains?: string[] | null;
  body_not_contains?: string[] | null;
  location_contains?: string;
}

export interface AuthzDocCase {
  id: string;
  allow_write?: boolean;
  deny_all?: boolean;
  request: AuthzDocRequest;
  expect?: Record<string, AuthzDocExpectation | null> | null;
}

export interface AuthzDocument {
  version: number;
  target: { base_url: string; allow_plain_http?: boolean; timeout_seconds?: number };
  personas: AuthzDocPersona[];
  cases: AuthzDocCase[];
}

// GET /api/tasks/{id}/authz
export interface TaskAuthz {
  accounts: AuthzAccount[];
  policy: string;
  policy_updated_at: string | null;
  check: AuthzPolicyCheck | null;
  // The saved policy as data; null when it can only be edited as text. Absent on older servers.
  document?: AuthzDocument | null;
  running: boolean;
  runs: AuthzRunSummary[]; // newest first
}

export interface AuthzPersonaResult {
  name: string; // persona key
  role?: string; // role name of the matching test account, when there is one
  authenticated: boolean;
  identity_verified: boolean;
}

export interface AuthzCheckResult {
  persona: string;
  expect: "allow" | "deny" | string;
  expected_status: number[] | null;
  status?: number;
  result: "pass" | "fail" | "inconclusive" | string;
  kind?: string;
  detail?: string;
  duration_ms: number;
  response_bytes: number;
  excerpt?: string;
}

export interface AuthzCaseResult {
  id: string;
  method: string;
  path: string;
  write?: boolean;
  result: string;
  checks: AuthzCheckResult[] | null;
}

// The report of one run, as written by the authz runner (schema counterweight-authz-report/1).
export interface AuthzReport {
  schema: string;
  policy_sha256: string;
  target?: string;
  started_at: string;
  duration_ms: number;
  result: string;
  exit_code: number;
  counts: AuthzCounts;
  errors?: string[] | null;
  personas?: AuthzPersonaResult[] | null;
  cases?: AuthzCaseResult[] | null;
}

// GET /api/tasks/{id}/authz/runs/{runId}
export interface AuthzRun extends AuthzRunSummary {
  report: AuthzReport;
}

export interface TaskCategory {
  id: number;
  name: string;
  task_count: number;
  created_at: string;
  updated_at: string;
}

export interface TaskTemplate {
  id: number;
  name: string;
  description: string;
  goal: string;
  category_id?: number | null; // Preset category; null/omitted means none.
  intercept_rules?: AssetInterceptRuleInput[]; // Preset task-specific block/allow rules.
  assessment_type?: AssessmentType; // Absent on older backends; treated as "general".
  repository?: string; // Code-review presets only; may carry a "#ref" suffix.
  mode?: CodeReviewMode | ""; // Code-review presets only.
  created_at: string;
  updated_at: string;
}

export interface DeleteTaskOptions {
  delete_assets: boolean;
  delete_traffic: boolean;
  delete_files: boolean;
  delete_findings: boolean;
  delete_llm_records: boolean;
}

export interface DeleteTaskResult {
  deleted: string;
  assets_deleted: number;
  assets_detached: number;
  traffic_deleted: number;
  files_deleted: boolean;
  findings_deleted: number;
  llm_records_deleted: number;
  cleanup_warning?: string;
}

export type TaskArchiveState =
  | "archive_queued"
  | "archiving"
  | "archive_failed"
  | "ready"
  | "restore_queued"
  | "restoring"
  | "restore_failed"
  | "delete_queued"
  | "deleting"
  | "delete_failed";

export interface TaskArchiveTokenStats {
  calls?: number;
  input_tokens?: number;
  output_tokens?: number;
  cache_read_tokens?: number;
  cache_write_tokens?: number;
}

export interface TaskArchive {
  id: number;
  task_id: number;
  state: TaskArchiveState;
  phase: string;
  progress: number;
  error?: string;
  warnings?: string[];
  format_version: number;
  sha256?: string;
  original_size: number;
  compressed_size: number;
  task_name: string;
  task_description: string;
  task_goal: string;
  original_status: TaskStatus;
  category_id?: number;
  category_name?: string;
  source_task_ids: number[];
  remaining_timeout_seconds: number;
  data_counts: Record<string, number>;
  aggregate_stats: {
    tokens?: TaskArchiveTokenStats;
    skills?: Record<string, number>;
    tools?: Record<string, number>;
    findings?: Record<string, number>;
  };
  archived_at?: string;
  requested_at: string;
  created_at: string;
  updated_at: string;
}

export interface TaskArchivePage {
  items: TaskArchive[];
  total: number;
  page: number;
  size: number;
}

export interface ArchiveBatchItem {
  id: string;
  archive_id?: number;
  ok: boolean;
  queued: boolean;
  error?: string;
}

// ---- Asset graph (global, shared across tasks) ----
export type AssetType =
  | "company"
  | "domain"
  | "ip"
  | "port"
  | "service"
  | "site"
  | "endpoint"
  | "parameter"
  | "tech"
  | "credential"
  | "data";

export type NodeState = "observed" | "confirmed" | "tombstoned";

export interface AssetNode {
  id: string;
  type: AssetType;
  name: string;
  key: string; // nkey
  value?: string;
  company_id?: string; // Owning company asset ID; empty means unassigned.
  state: NodeState;
  confidence: number; // 0..1
  attrs?: Record<string, unknown>;
  first_seen: string;
  last_seen: string;
}

export type AssetRel =
  | "owns"
  | "resolves"
  | "exposes"
  | "runs"
  | "serves"
  | "has_endpoint"
  | "has_param"
  | "fingerprinted"
  | "authenticates_as"
  | "reachable"
  | "has_subdomain";

export interface Edge {
  src: string;
  dst: string;
  rel: AssetRel | ExploreRel;
}

// Task asset view — server-side enriched, paginated.
export interface TaskAssetRef {
  id: string;
  name?: string;
  key: string;
  attrs?: Record<string, unknown>;
}

export interface TaskAssetItem extends AssetNode {
  techs?: TaskAssetRef[];
  auth?: TaskAssetRef[];
  params?: TaskAssetRef[];
}

export interface TaskAssetView {
  counts: Record<string, number>;
  total: number;
  items: TaskAssetItem[];
}

// ---- New unified asset model (new backend) ----
export type NewAssetType = "root_domain" | "ip" | "subdomain" | "app" | "service" | "endpoint";

export interface Asset {
  id: number;
  type: NewAssetType;
  company_id?: number;
  task_ids: number[];
  domain?: string;
  root_domain?: string;
  ip?: string;
  c_segment?: string;
  port?: number;
  icp?: string;
  bound_domains?: string[];
  open_ports?: { port: number; service?: string }[];
  record_type?: string;
  record_value?: string[] | string;
  bundle_id?: string;
  app_name?: string;
  category?: string;
  app_description?: string;
  app_icp?: string;
  url?: string;
  service_type?: string;
  service_name?: string;
  favicon_mmh3?: string;
  status_code?: number;
  content_length?: number;
  page_title?: string;
  technologies?: string[];
  auth?: Record<string, unknown>[];
  method?: string;
  params?: Record<string, unknown>[];
  extra?: Record<string, unknown>;
  last_seen: string;
  task_source?: string;
  task_source_summary?: string;
  task_source_node_id?: number;
}

export interface IntentAsset {
  intent_id: number | string;
  asset_id: number;
  type: NewAssetType;
  label: string;
  source: string;
  source_summary: string;
  source_node_id?: number;
  source_task_id: number;
  inherited: boolean;
}

export interface TaskAssetMutation {
  requested: number;
  attached: number;
  existing: number;
}

export interface TaskAssetScopeMutation {
  requested: number;
  assets_linked: number;
  assets_existing: number;
  scopes_added: number;
  scopes_existing: number;
}

// ---- Asset coverage graph (per task) ----
// Force-directed coverage node keys: assets a:<id>, companies c:<id>,
// and missing root-domain asset rows r:<domain>. in_scope=false identifies gray connector context nodes.
export interface CoverageGraphNode {
  key: string;
  kind: "company" | "root_domain" | "subdomain" | "ip" | "service" | "app" | "endpoint";
  label: string;
  tested: boolean;
  in_scope: boolean;
  asset_id?: number;
  company_id?: number;
  domain?: string;
  root_domain?: string;
  ip?: string;
  url?: string;
  port?: number;
  service_type?: string;
  app_name?: string;
  page_title?: string;
  status_code?: number;
}

export interface CoverageGraphEdge {
  src: string;
  dst: string;
}

export interface CoverageGraphData {
  nodes: CoverageGraphNode[];
  edges: CoverageGraphEdge[];
}

// Asset-linked intents/facts/findings in a task graph, for coverage drawers.
export interface CoverageAssetRef {
  id: number;
  kind: string;
  state: string;
  summary: string;
  source_task_id?: string;
  inherited?: boolean;
}
export interface CoverageAssetRefs {
  intents: CoverageAssetRef[];
  facts: CoverageAssetRef[];
  findings: CoverageAssetRef[];
}

// ---- Workspace file manager (workDir) ----
export interface WorkspaceEntry {
  name: string;
  path: string; // workspace-relative, forward slashes
  dir: boolean;
  size: number;
  mtime: number; // unix millis
}
export interface WorkspaceListing {
  path: string;
  entries: WorkspaceEntry[];
}
export interface WorkspaceFile {
  path: string;
  size: number;
  binary: boolean;
  too_large?: boolean;
  content?: string;
}

// Coverage entries propose scope; operator_authorized records a separate read grant.
export interface TaskScopeRow {
  id: number;
  task_id: number;
  kind: "company" | "root_domain" | "subdomain" | "ip" | "cidr" | "icp" | "keyword";
  company_id?: number;
  company_name?: string; // Resolved by backend companies JOIN; populated only for kind=company.
  domain?: string;
  net?: string;
  value?: string;
  source: "auto" | "agent" | "manual";
  operator_authorized: boolean;
  reason?: string;
}

export type CompanyScopeKind = "domain" | "ip" | "cidr" | "icp" | "keyword";

// Structured asset-scope rules submitted when creating a company.
export interface CompanyScopeRule {
  kind: CompanyScopeKind;
  value: string;
}

// Scope-write results: errors identify invalid submitted rows; warnings identify preexisting unrelated data
// that may distort ownership, such as assets storing hostnames in IP fields.
export interface CompanyScopeMutation {
  added: number;
  skipped: number;
  invalid: number;
  errors?: string[];
  warnings?: string[];
}

// Company asset-scope rule, the sole source of ownership truth.
export interface ScopeRow {
  id: number;
  company_id: number;
  kind: CompanyScopeKind;
  domain?: string; // Populated for kind=domain.
  net?: string; // Populated for kind=ip or cidr.
  value?: string; // May be returned directly by the backend for kind=icp or keyword.
  raw: string; // Original user input for display and form refill.
  reason?: string;
  source?: "manual" | "agent"; // An agent's rule grants no asset access until it is approved.
}

// Company combines a type=company asset node, icon, asset count, and scope rules.
export interface Company {
  id: number;
  name: string;
  logo?: string; // Remote icon URL; empty falls back to the name initial.
  asset_count: number;
  scope?: ScopeRow[];
}

// ---- Exploration graph (per task) ----
export type ExploreKind = "task" | "begin" | "goal" | "intent" | "fact" | "finding" | "hint" | "digest";
export type GoalState = "open" | "met" | "abandoned";
export type IntentState = "open" | "running" | "paused" | "done" | "blocked" | "exhausted" | "stopped";
export type FindingState = "confirmed" | "dismissed";
export type HintState = "active" | "consumed";
export type ExploreRel = "spawns" | "derived_from" | "yields" | "proves" | "covers";

export interface TaskNode {
  id: string;
  type: ExploreKind;
  payload?: string;
  priority: number; // 0..10
  state: string; // GoalState | IntentState | FindingState | HintState
  origin: string;
  ts: string;
  source_task_id?: string;
  inherited?: boolean;
  delete_reason?: string; // Deletion reason for soft-deleted intents (state='deleted').
  worker_no?: number; // 1-based worker order inside this task, not the global intent id.
}

// Broadcast page contains creation-ordered nodes, related edges, and opposite nodes indexed in refs,
// explaining origins and outputs without loading the whole graph.
export interface ExplorationNodePage {
  items: TaskNode[];
  total: number;
  page: number;
  size: number;
  edges: Edge[];
  refs: Record<string, TaskNode>;
  // Node-ID-to-anchored-assets map for page nodes and neighbors, shown on expansion.
  assets: Record<string, FindingAsset[]>;
}

export interface ExplorationNodeQuery {
  page?: number;
  size?: number;
  kinds?: ExploreKind[];
  states?: string[];
  q?: string;
  order?: "asc" | "desc";
}

// Objective-card data with payload already parsed into text/vulnclass by the backend.
export interface TaskGoal {
  id: string;
  text: string;
  vulnclass?: string;
  state: string; // GoalState
  origin?: string;
  ts: string;
}

// Constraint-card data: allow permits and deny prohibits.
export type ConstraintKind = "allow" | "deny";
export interface TaskConstraint {
  id: string;
  kind: ConstraintKind;
  text: string;
  origin?: string;
  ts?: string;
}

// ---- Findings ----
export type Severity = "critical" | "high" | "medium" | "low";

// Resolution states: pending, in progress, confirmed, resolved, fixed, false positive, ignored, duplicate, risk accepted.
export type FindingStatus =
  | "pending"
  | "in_progress"
  | "confirmed"
  | "resolved"
  | "fixed"
  | "false_positive"
  | "ignored"
  | "duplicate"
  | "risk_accepted";

// FindingAsset references a bound asset with its label prerendered by the backend.
export interface FindingAsset {
  id: string;
  type: string;
  label: string;
}

export interface Finding {
  traffic_count?: number;
  evidence_version?: number;
  report_evidence_version?: number;
  report_stale?: boolean;
  id: string;
  finding_id?: string; // Independent findings-table row ID used for updates/details; legacy task nodes may lack it.
  vulnclass: string;
  name?: string; // Vulnerability name; empty display falls back to vulnclass.
  severity: Severity;
  status: FindingStatus;
  summary: string;
  evidence: string;
  report?: string; // Detailed Markdown report returned only by details, empty in lists.
  intent_id?: string;
  param_id?: string;
  task_id?: string;
  task_description?: string;
  source_task_id?: string;
  inherited?: boolean;
  assets?: FindingAsset[];
  in_change?: boolean; // Only for findings of a change-scoped review: false means the cited code was not changed.
  ts: string;
}

// FindingsPage is the server-paginated findings response.
export interface FindingsPage {
  items: Finding[];
  total: number;
  page: number;
  page_size: number;
}

export interface FindingGroup {
  task_id: string | number | null;
  task_name?: string; // Optional task name; blank/omitted means unnamed.
  task_description: string;
  task_status: string;
  count: number;
  critical: number;
  high: number;
  medium: number;
  low: number;
  last_found_at: string;
}

export interface FindingGroupsPage {
  items: FindingGroup[];
  total: number;
  finding_total: number;
  page: number;
  page_size: number;
}

export interface FindingDeepenResponse {
  task_id: string;
  intent_id: string;
  state: IntentState;
  queued: boolean;
}

// FindingStats aggregates all findings for cards and type options, independent of pagination.
export interface FindingStats {
  total: number;
  pending: number;
  critical: number;
  high: number;
  medium: number;
  low: number;
  vulnclasses: string[];
  tasks: FindingTaskOption[];
}

// FindingTaskOption lists tasks with findings, descriptions, and counts. Empty description indicates deletion,
// so the frontend falls back to the task ID.
export interface FindingTaskOption {
  id: string | number;
  name?: string; // Optional task name; blank/omitted means unnamed.
  description: string;
  count: number;
}

// FindingQuery contains pagination, filters, and sort parameters.
export interface FindingQuery {
  page: number;
  pageSize: number;
  severity?: "all" | Severity;
  status?: "all" | FindingStatus;
  vulnclass?: string;
  task?: string; // Task ID; all or empty disables task filtering.
  query?: string;
  sort?: "severity" | "time";
  // Asset-tree node key selects its entire subtree; empty disables asset filtering.
  assetScope?: string;
}

// Findings by asset.
export type FindingAssetKind = "company" | "root_domain" | "subdomain" | "ip" | "service" | "app" | "endpoint" | "none";

// FindingAssetNode keys include a:<id> assets, c:<id> companies,
// r:<domain> roots without asset rows, and __none__ for unassigned assets.
export interface FindingAssetNode {
  key: string;
  parent?: string;
  kind: FindingAssetKind;
  label: string;
  asset_id?: number;
  company_id?: number;
  self: number; // Findings attached directly to this asset.
  total: number; // Includes descendants, deduplicated by finding.
  critical: number;
  high: number;
  medium: number;
  low: number;
  last_found_at: string;
}

export interface FindingAssetTree {
  nodes: FindingAssetNode[];
  finding_total: number;
  truncated: boolean;
  dropped_kinds?: string[];
}

// FINDING_UNASSIGNED_ASSET corresponds to backend db.FindingUnassignedAsset.
export const FINDING_UNASSIGNED_ASSET = "__none__";

// ---- Activity / sessions ----
export type ActivityKind =
  | "tool_use"
  | "tool_result"
  | "text"
  | "thinking"
  | "result"
  | "user"
  | "intent" // LLM-generated exploration objective leading a worker session (UI-synthesized)
  | "round" // planner round boundary marker (engine-emitted)
  | "usage" // live cumulative token usage (per model turn); not rendered
  | "llm_switch" // automatic/manual task-level LLM switch
  | "llm_failover" // task-level provider switch / chain exhaustion audit event
  | "intercept_request"; // user-approval request from the intercept layer

// ChatAttachment path is relative to the session/task working directory, the Agent's CWD.
export interface ChatAttachment {
  name: string;
  path: string;
  size: number;
  abs?: string; // Absolute path returned for staging uploads and appended to descriptions before task creation.
}

export interface Activity {
  seq: number;
  intent_id?: string;
  worker: string; // session owner: planner | mainagent | work#1 ...
  ts: string;
  kind: ActivityKind;
  tool?: string;
  tool_use_id?: string;
  is_error?: boolean;
  summary: string;
  detail?: string;
  metadata?: {
    llm_transition?: LLMTransition;
  };
  source_task_id?: string;
  inherited?: boolean;
  main_seg?: number; // main-agent conversation segment (present only on worker="mainagent" rows)
  // token usage (present only on kind='result')
  input_tokens?: number;
  output_tokens?: number;
  cache_read_tokens?: number;
  cache_write_tokens?: number;
}

export interface LLMAuditProfile {
  id: number;
  name: string;
  format: string;
  model: string;
}

export interface LLMTransition {
  mode: "automatic" | "manual" | "exhausted";
  reason: string;
  previous?: LLMAuditProfile;
  next?: LLMAuditProfile;
}

export interface TaskLLMResolution {
  profile_id?: number;
  name: string;
  format: string;
  model: string;
  source: "task_chain" | "agent_binding" | "global_profile" | "environment" | "global";
  available: boolean;
  reason?: string;
}

export interface TaskLLMResolutions {
  mainagent: TaskLLMResolution;
  planner: TaskLLMResolution;
  worker: TaskLLMResolution;
}

// Agent triggers: P3 scheduling for custom Agents only.
export interface AgentTrigger {
  id: number;
  agent_key: string;
  enabled: boolean;
  interval_sec: number; // Timer every N seconds; 0 disables scheduling.
  on_finding: boolean; // Trigger when any task discovers a finding.
  on_goal_met: boolean; // Trigger when any task meets an objective.
  on_task_timeout: boolean; // Trigger when any task times out.
  on_tool_call: boolean; // Trigger when a selected tool call finishes.
  on_task_create: boolean; // Trigger when any task is created.
  interval_message: string; // Independent user messages for each trigger condition.
  finding_message: string;
  goal_message: string;
  task_timeout_message: string;
  tool_call_message: string;
  task_create_message: string;
  tool_names: string[]; // Selected on_tool_call tool keys, at least one.
  last_fire?: string;
}

// ---- Conversations (chat page) ----
export interface ActiveFindingRetest {
  id: number;
  finding_id: string;
  conversation_id: number;
  status: "pending" | "running";
}

// The retest, verification or fix run a chat conversation belongs to.
export interface ConversationFindingRun {
  id: number;
  kind: string;
  status: "pending" | "running" | "completed" | "failed" | "stopped";
  finding_id: number;
  task_id?: string;
}

export interface FindingRetest {
  id: number;
  finding_id: number;
  conversation_id: number | null;
  status: "pending" | "running" | "completed" | "failed" | "stopped";
  verdict:
    | ""
    | "reproduced"
    | "fixed"
    | "inconclusive"
    | "confirmed"
    | "needs_validation"
    | "rejected"
    | "patched"
    | "cannot_fix"
    | "approve"
    | "changes_needed"
    | "reject";
  /** "verify" marks the automatic independent verification of a code-review finding;
   *  "mitigate" and "patch_review" are a fix attempt and its review (Mitigation tab). */
  kind?: "retest" | "verify" | "mitigate" | "patch_review" | "recheck";
  notes: string;
  summary: string;
  evidence: string;
  error: string;
  created_at: string;
  started_at: string | null;
  finished_at: string | null;
}

export type MitigationState =
  | "drafting"
  | "in_review"
  | "ready"
  | "pr_open"
  | "merged"
  | "pr_closed"
  | "blocked"
  | "cannot_fix"
  | "failed"
  | "review_failed"
  | "changes_needed"
  | "rejected";

export interface FindingMitigation {
  run: FindingRetest;
  review: FindingRetest | null;
  // Latest post-merge re-check: confirmed = still present, rejected = fixed.
  recheck: FindingRetest | null;
  branch: string;
  base_commit: string;
  diff: string;
  blocked: string;
  pr_url: string;
  pr_state: "none" | "open" | "merged" | "closed";
  published_at: string | null;
  state: MitigationState;
}

export interface MitigationFinding {
  id: string;
  name: string;
  vulnclass: string;
  severity: Severity;
  status: FindingStatus;
  /** Latest completed independent verification verdict, "" if none. */
  verification: "" | "confirmed" | "needs_validation" | "rejected";
  mitigations: FindingMitigation[];
}

// Jira Cloud integration (server/jira.go). The API token is never returned.
export interface JiraConfig {
  project_key: string;
  issue_type_id: string;
  epic_type_id: string;
  priorities: Record<string, string>; // severity -> Jira priority id
  sla_days: Record<string, number>; // severity -> days to fix; absent or 0 = no due date
  labels: string[];
  security_level_id: string;
  sync: boolean;
}

export interface JiraStatus {
  configured: boolean;
  site?: string;
  email?: string;
  account?: string;
  hint?: string;
  config: JiraConfig;
}

export interface JiraProject {
  id: string;
  key: string;
  name: string;
  style: string;
}

export interface JiraIssueType {
  id: string;
  name: string;
  subtask: boolean;
  hierarchyLevel: number; // 1 = epic level
}

export interface JiraAllowedValue {
  id: string;
  name?: string;
  value?: string;
}

export interface JiraProjectMeta {
  issue_types: JiraIssueType[];
  fields?: {
    priorities: JiraAllowedValue[];
    security_levels: JiraAllowedValue[];
    has_due_date: boolean;
    has_labels: boolean;
    has_parent: boolean;
    unsupported_required: string[];
  };
}

export interface JiraLink {
  key: string;
  url: string;
  created: boolean;
  project_key: string; // the Jira space (project) the issue lives in
  issue_type_id?: string; // epics: issue type of the task's findings
  security_level_id?: string; // epics: security level of the task's issues
  status_name?: string;
  status_category?: string; // new | indeterminate | done
}

export interface JiraTaskLinks {
  configured: boolean;
  default_project: string;
  default_issue_type: string;
  default_security_level: string;
  epic: JiraLink | null; // once filed, the task's findings stay in its space
  findings: Record<string, JiraLink>;
}

// Where an export files issues; the task's epic fixes the space once it exists.
export interface JiraTarget {
  project_key: string;
  issue_type_id: string;
  security_level_id: string; // "" = the space's default visibility
}

export interface JiraTaskExport {
  epic: JiraLink | null;
  results: {
    finding_id: string;
    title: string;
    severity: string;
    issue?: JiraLink;
    skipped?: string;
    error?: string;
  }[];
}

export interface GitHubTokenStatus {
  configured: boolean;
  login?: string;
  hint?: string;
  expires_at?: string;
  expired: boolean;
}

// One-time invitation to star the project; direct means a stored GitHub token can set the star.
export interface StarPrompt {
  show: boolean;
  direct: boolean;
  url: string;
}

// Per-task code review automation; both default to off (manual).
export interface TaskAutomation {
  verify: boolean;
  mitigate: boolean;
  code_review: boolean;
}

export interface TaskMitigations {
  code_review: boolean;
  repository?: string;
  github: GitHubTokenStatus;
  findings: MitigationFinding[];
}

export interface Conversation {
  id: number;
  running?: boolean; // live server state, returned with the conversation list
  agent_key: string;
  title: string;
  llm_profile_id?: number;
  pinned?: boolean;
  pinned_at?: string | null;
  created_at: string;
  updated_at: string;
}

// ---- Backend logs (/logs page) ----
export interface LogLine {
  seq: number;
  db_id?: number; // server_logs.id; present for DB-persisted lines
  ts: string;
  level: "info" | "warn" | "error";
  tag: string;
  text: string;
}

export type SessionRole = "mainagent" | "planner" | "worker" | "system";
export type SessionStatus = "running" | "paused" | "done" | "blocked" | "exhausted" | "pending" | "stopped" | "deleted";

// Daily token aggregate bucket (GET /api/tokens/daily).
export interface DailyTokenBucket {
  date: string; // "YYYY-MM-DD"
  input_tokens: number;
  output_tokens: number;
  cache_read_tokens: number;
  cache_write_tokens: number;
}

// Per-worker token usage (GET /api/exploration/tokens).
export interface TokenUsage {
  worker: string;
  input_tokens: number;
  output_tokens: number;
  cache_read_tokens: number;
  cache_write_tokens: number;
}

export interface SessionTokenUsage {
  session: string;
  input_tokens: number;
  output_tokens: number;
  cache_read_tokens: number;
  cache_write_tokens: number;
}

export interface BatchControlItem {
  id: string;
  ok: boolean;
  status?: string;
  queued?: boolean;
  error?: string;
}

// Per-task bulk-category results. Only deleted tasks can fail; category writes are atomic.
export interface BatchCategoryItem {
  id: string;
  ok: boolean;
  error?: string;
}

// Whole-task (all agents) token aggregate.
export interface TokenTotal {
  input_tokens: number;
  output_tokens: number;
  cache_read_tokens: number;
  cache_write_tokens: number;
}

// Global per-profile token spend from the llm_usage ledger (GET /api/tokens/usage).
export interface ProfileUsage {
  profile_name: string;
  calls: number;
  tasks: number;
  input_tokens: number;
  output_tokens: number;
  cache_read_tokens: number;
  cache_write_tokens: number;
}

// One (profile, UTC day) token bucket for the dashboard's daily chart (new source).
export interface ProfileDayUsage {
  profile_name: string;
  date: string; // YYYY-MM-DD
  input_tokens: number;
  output_tokens: number;
  cache_read_tokens: number;
}

// Response of GET /api/tokens/usage — the dashboard's "new" (llm_usage) token view.
export interface UsageStats {
  by_profile: ProfileUsage[];
  daily: ProfileDayUsage[];
}

// Per-model token usage for one task (GET /api/llm/records/by-model), from the
// always-on llm_usage metering ledger. calls = number of LLM calls on this model.
export interface ModelTokenStat {
  model: string;
  calls: number;
  input_tokens: number;
  output_tokens: number;
  cache_read_tokens: number;
  cache_write_tokens: number;
}

export interface Session {
  id: string;
  role: SessionRole;
  title: string;
  status: SessionStatus;
  live: boolean;
  last_activity: string;
  intent_id?: string;
  worker_no?: number;
  source_task_id?: string;
  inherited?: boolean;
  seg?: number; // main-agent session: which conversation segment (0 = original)
}

// ---- Security ----
export interface AuditEntry {
  ts: string;
  tool: string;
  action: "allow" | "block";
  reason?: string;
  command?: string;
}

export interface Audit {
  entries?: AuditEntry[];
  attributions?: Record<string, number>;
}

// ---- Traffic ----
export interface TrafficExchange {
  id: string;
  ts: string;
  host: string;
  method: string;
  url: string;
  status: number;
  content_type: string;
  resp_len: number;
}

export interface TrafficResp {
  enabled: boolean;
  proxy?: string;
  count?: number; // global total (unfiltered)
  total?: number; // rows matching the current filter (for pagination)
  page?: number;
  size?: number;
  exchanges?: TrafficExchange[];
}

// Full raw request/response of one exchange (lazy-loaded on row select).
export interface TrafficDetail {
  req: string;
  resp: string;
}

// One distinct recorded host with its exchange count (target picker).
export interface TrafficHost {
  host: string;
  count: number;
}

// ---- App settings (runtime toggles) ----
export interface Settings {
  traffic_capture: boolean;
  agent_traffic_binding: boolean; // Automatic Agent traffic-evidence binding, off by default; manual binding is unaffected.
  llm_record: boolean; // LLM recording switch, off by default; disabled means no calls recorded.
  // Web search. brave_key_set / tavily_key_set reflect whether a key is stored
  // (the values are never returned). On PUT, send the corresponding field to set/clear.
  web_search_enabled: boolean;
  web_search_backend: string; // "ddgs" | "brave-free" | "tavily" | "deepseek"
  brave_key_set: boolean;
  tavily_key_set: boolean;
  // write-only: only sent on PUT to store/clear the key.
  brave_search_api_key?: string;
  tavily_search_api_key?: string;
  // Independent HTTP/HTTPS/SOCKS5 search proxy, separate from MITM recording; empty means direct.
  web_search_proxy?: string;
  // Global target-traffic proxy supports HTTP/HTTPS/SOCKS5 and user:pass. With capture enabled it is
  // the MITM upstream; otherwise injected into Agent Bash/WebFetch. Empty means direct.
  global_proxy?: string;
  python_interpreter?: string; // Custom-script Python interpreter path; empty means runtime detection.
  workers?: number; // Worker concurrency defaults to 3 and affects subsequently started tasks.
  // Task concurrency cap: disabled means unlimited; excess new tasks queue and start when slots open.
  task_concurrency_enabled?: boolean; // Defaults to false.
  task_concurrency_limit?: number; // Defaults to 5 when enabled.
  // LLM failover rotation defaults off. Unbound Agents switch to the next profile when the current one fails
  // due to insufficient balance, invalid key, rate limiting, or service errors.
  llm_pool_enabled?: boolean; // Defaults to false.
  // Whether bound Agent/task profiles also fall back to rotation; false means exclusive binding.
  llm_pool_bind_fallback?: boolean;
  // Constraint injection defaults on for both targets, appending task allow/deny constraints to Agent system prompts.
  constraints_inject_planner?: boolean;
  constraints_inject_worker?: boolean;
  // Experimental model-driven noa compression defaults off. When enabled, planner,
  // worker, main-Agent, and chat contexts use noa instead of built-in compaction. Read once per run,
  // so changes affect subsequent runs.
  noa_compaction?: boolean;
  // Absent on older backends. True (default) confines file tools to the task's own folders.
  isolation?: boolean;
  // "off", "confined-files" or "container"; takes precedence over isolation when both are sent.
  isolation_mode?: "off" | "confined-files" | "container" | string;
  // Token budget applied to new tasks that do not set one; 0 means no limit. Absent on older backends.
  default_task_token_budget?: number;
  // Vulnerability notifications: channels are separate /api/notify/* resources; only three global settings live here.
  notify_enabled?: boolean; // Notification master switch defaults on and can pause all delivery during maintenance.
  notify_public_base_url?: string; // Externally accessible finding-detail backlink base; empty omits backlinks.
  notify_digest_interval_min?: number; // Digest interval in minutes, default 30.
}

// Vulnerability instant-message notifications.

// NotificationFilter fields are optional; omitted fields do not filter.
// Backend does not validate these fields; malformed filters match rather than risk missing notifications.
export interface NotificationFilter {
  min_severity?: string; // "" | low | medium | high | critical
  task_ids?: number[]; // Empty means unrestricted; otherwise require intersection with finding task IDs.
  asset_ids?: number[]; // Empty means unrestricted; otherwise require intersection with anchored asset IDs.
  vulnclass_include?: string[]; // Empty accepts all; otherwise require a case-insensitive vulnerability-type keyword substring.
  vulnclass_exclude?: string[]; // Any matching exclusion keyword excludes; exclusion overrides inclusion.
  on_status_change?: boolean; // Whether to receive vulnerability resolution-status changes too.
}

// NotificationChannel is one channel instance with kind-specific configuration;
// credential values read back as __masked__ prefixes and are preserved when returned unchanged.
export interface NotificationChannel {
  id: number;
  name: string;
  kind: string;
  enabled: boolean;
  mode: "realtime" | "digest";
  config: Record<string, unknown>;
  filter: NotificationFilter;
  rate_per_min: number;
  created_at: string;
  updated_at: string;
  // Backend secret_keys determines password inputs and unchanged-value hints per channel,
  // without hardcoded channel-specific knowledge.
  secret_keys: string[];
}

// NotificationKind is channel metadata returned by /api/notify/meta.
export interface NotificationKind {
  kind: string;
  default_rate_per_min: number;
  secret_keys: string[];
}

export interface NotificationMeta {
  kinds: NotificationKind[];
  enabled: boolean;
  public_base_url: string;
  digest_interval_min: string;
  defaults: { digest_interval_min: number };
  stats: {
    channels: number;
    channels_on: number;
    pending: number;
    failed: number;
    sent_today: number;
    backlog_age_ms: number;
  };
}

// NotificationDelivery represents delivery history and failed-message retries.
export interface NotificationDelivery {
  id: number;
  finding_id: string;
  event_kind: string; // finding_created | finding_status_changed
  channel_id: number;
  channel_name: string;
  channel_kind: string;
  state: "pending" | "sending" | "sent" | "failed" | "skipped";
  attempts: number;
  last_error: string;
  batch_id?: number;
  created_at: string;
  sent_at?: string;
  next_attempt_at: string;
  title: string;
  severity: string;
}

// ---- LLM config ----
export interface ChatGPTAccount {
  id: string;
  email: string;
  connected: boolean;
}
export interface ChatGPTModel {
  cyber_programs?: string[];
  slug: string;
  display_name: string;
}
export interface ChatGPTLogin {
  id: string;
  url?: string;
  status: "pending" | "exchanging" | "connected" | "failed" | "cancelled" | "expired";
  error?: string;
  account?: ChatGPTAccount;
  expires_at: string;
}

export interface LLMProfile {
  cyber_access_program?: string;
  subscription_id?: string;
  id: string;
  name: string;
  format: "openai" | "anthropic" | "openai-responses";
  base_url?: string;
  proxy?: string;
  model: string;
  api_key_hint?: string;
  rate_per_second: number;
  rate_per_minute: number;
  context_window_k?: number;
  // thinking.type: empty omits (default), disabled turns off, enabled turns on.
  thinking_type?: string;
  // Reasoning effort: empty omits (default), or low/medium/high/xhigh/max.
  reasoning_effort?: string;
  is_default: boolean;
  // Rotation priority: higher first; active profile always leads regardless.
  priority?: number;
  // True excludes failover selection but permits explicit Agent/task binding.
  pool_exclude?: boolean;
  // True (default) uses SSE streaming; false uses stream:false with a complete response.
  streaming?: boolean;
  // Per-response output token limit; 0 omits the field for server defaults.
  // Different from context_window_k, total model capacity used locally for compression thresholds.
  max_tokens?: number;
  // Output-limit request field name, relevant only for format=openai:
  // Empty defaults to max_tokens; max_completion_tokens is required by OpenAI reasoning models.
  max_tokens_field?: string;
  // Custom HTTP header name; nonempty sends the current conversation/intent session ID on every request.
  // Empty omits it. Used by gateways with session-based prompt caching or sticky routing.
  session_header_key?: string;
  // Profile overrides for connection/empty-response/safe-window retries; blank/all zeros inherit global policy.
  retry?: LLMRetryOverride;
  // Optional prices per million tokens, used only to estimate cost; null or absent means unset.
  price_input_per_million?: number | null;
  price_cached_input_per_million?: number | null;
  price_output_per_million?: number | null;
  price_currency?: string;
}

// LLM retry policy.
// Two controls per retry layer; both use 0 for unset:
// attempts: 0 uses defaults, -1 disables the layer, positive values set retry count.
// interval_ms: 0 uses default exponential backoff, positive values set fixed milliseconds.
export interface LLMRetryRule {
  attempts: number;
  interval_ms: number;
}

// Three endpoint-specific layers can be overridden per LLM profile.
export interface LLMRetryOverride {
  connect: LLMRetryRule; // Connection retries: reset/timeout/429/5xx before streaming starts.
  empty: LLMRetryRule; // Empty-response retries: completed calls without content, OpenAI format only.
  stream: LLMRetryRule; // Same-provider safe-window retries replay interrupted streams before any output delivery.
}

// Global policy combines defaults for those layers with two process-wide layers:
// breaker: consecutive transient-failure threshold and optional fixed cooldown interval.
// intent: rerun the entire intent after a Worker ends with model_error.
export interface LLMRetryPolicy extends LLMRetryOverride {
  breaker: LLMRetryRule;
  intent: LLMRetryRule;
}

// LLM rotation/failover.
// A profile's rotation position and health state:
// ok: healthy.
// degraded: consecutive failures below the breaker threshold.
// tripped: circuit open, skipped during cooldown; cooldown_secs is remaining time.
export interface LLMPoolMember {
  profile_id: string;
  name: string;
  model: string;
  format: string;
  priority: number;
  active: boolean; // Whether this is the active profile, always first in the chain.
  excluded: boolean; // pool_exclude opts out of rotation.
  state: "ok" | "degraded" | "tripped";
  fails: number;
  trips: number;
  cooldown_secs: number;
  last_error?: string;
  last_at?: string;
}

export interface LLMPoolStatus {
  enabled: boolean;
  bind_fallback: boolean;
  chain: LLMPoolMember[];
}

// ---- Agents ----
export interface Agent {
  id: string;
  key: string; // Built-ins use goals/planner/mainagent/worker; custom Agents use user-defined keys.
  name: string;
  description?: string;
  role: string;
  builtin: boolean;
  enabled: boolean;
  llm_profile_id?: number | null; // Bound LLM profile; null/absent follows task/conversation/global settings.
  max_turns?: number; // 0 means unlimited.
  run_seconds?: number; // Worker per-run wall-clock limit in seconds; 0 means unlimited.
  web_search?: boolean; // Web search enabled state, gated by the global master switch.
  interactive_shell?: boolean; // Interactive-shell persistent PTY tool enablement.
  // P3 post-trigger policy, meaningful only for custom Agents.
  trigger_run_mode?: "serial" | "parallel"; // Serial queue or separate concurrent conversation for each trigger.
  trigger_merge_mode?: "by_task" | "all" | "none"; // Serial only: merge by task, merge all, or do not merge.
  trigger_max_parallel?: number; // Parallel only: per-Agent concurrency limit; 0 means unlimited.
  // List-only counts for visible MCP servers, visible skills, and bound tools.
  mcp_count?: number;
  skill_count?: number;
  tool_count?: number;
}

export interface PromptVar {
  name: string;
  description: string;
  example: string;
  source: "exploration" | "runtime" | "distilled";
}

export interface PromptVersion {
  version: number;
  ts: string;
  note: string;
  template_text: string;
}

export interface AgentDetail {
  agent: Agent;
  prompt: string;
  variables: PromptVar[];
  versions: PromptVersion[];
  visibility: { mcp: number[]; skill: string[] };
  // LLM candidates for the default-model dropdown; current binding is agent.llm_profile_id.
  llm_profiles?: { id: number; name: string; model: string; is_default: boolean }[];
  wrapup_prompt?: string; // Saved wrap-up prompt; empty uses the built-in default.
  wrapup_default?: string; // Built-in wrap-up prompt for placeholder/reset.
  wrapup_max_turns?: number; // Saved wrap-up round count; 0 uses the built-in default.
  wrapup_max_turns_default?: number; // Built-in round count for the 0=default N hint.
  // Task-timeout wrap-up applies only to worker/planner; show when task_timeout_wrapup_supported=true.
  task_timeout_wrapup_supported?: boolean;
  task_timeout_wrapup_prompt?: string;
  task_timeout_wrapup_default?: string;
  task_timeout_wrapup_max_turns?: number;
  task_timeout_wrapup_max_turns_default?: number;
}

// ---- MCP ----
// The API returns every stored, non-empty env/header value as this placeholder;
// sending it back unchanged keeps the stored value (server/server_mgmt.go).
export const MCP_SECRET_PLACEHOLDER = "********";

export interface MCPServer {
  id: number;
  name: string;
  transport: "stdio" | "http" | "sse";
  command?: string;
  args: string[];
  env: Record<string, string>; // values masked as MCP_SECRET_PLACEHOLDER in responses
  url?: string;
  enabled: boolean;
  insecure?: boolean; // http: skip TLS cert verification (self-signed servers)
  tools?: string[]; // mcp_tools_cache (names only, for the count)
}

export interface MCPTool {
  name: string;
  description: string;
}

// ---- Skills ----
// Fields align with the agentskills.io open specification.
// description covers both "what the skill does" and "when to use it".
export interface SkillItem {
  name: string; // unique key = directory name
  description?: string; // required per spec; covers what + when to use
  license?: string; // optional: SPDX identifier or free text
  compatibility?: string; // optional: environment requirements
  mcps?: string[]; // MCP server names this skill unlocks on load
  files: string[]; // files in the skill directory
  // Usage statistics from skill_usage; unused skills have calls=0 and omit last_used.
  calls: number;
  tasks: number; // Number of tasks that loaded the skill, excluding chat sessions.
  usage_agents: string[]; // Agent keys that loaded the skill.
  last_used?: string;
}

// SkillCall is one Skill() invocation in a skill's recent-call list.
export interface SkillCall {
  ts: string;
  agent_key: string;
  task_id: number; // 0 identifies non-task contexts such as chat sessions.
  session_id: string;
  args_len: number;
}

// MissingSkill tracks requested but nonexistent skills, identifying capability gaps.
export interface MissingSkill {
  skill: string;
  calls: number;
  agents: string[];
  last_used?: string;
}

// Built-in tool catalog.
// key + handler live in Go; only these fields are page-editable. system tools lock
// the key and the parameter *structure* (name/type/required) — the per-param
// description/default and the agent binding are what move.
export interface Tool {
  key: string;
  system: boolean;
  description: string;
  // eslint-disable-next-line @typescript-eslint/no-explicit-any
  schema: Record<string, any>; // full JSON-Schema (object with properties)
  agents: string[]; // bound agent keys
  enabled: boolean;
  kind?: "builtin" | "shell" | "command" | "script" | "http"; // Custom tool kind.
  // eslint-disable-next-line @typescript-eslint/no-explicit-any
  exec?: Record<string, any>; // Custom tool execution specification for kind!=builtin.
  deferred?: boolean; // Deferred schemas via SearchExtraTools/ExecuteExtraTool.
  calls?: number; // persistent runtime invocation count (older APIs may omit it)
}

// ---- Stats ----
export interface Stats {
  assets: number;
  engine_mode: EngineMode;
  llm_configured: boolean;
  roe_enabled: boolean;
  findings_confirmed: number;
  active_task?: Partial<Task>;
}

// ---- Intercept Rules ----
export type InterceptAction = "allow" | "deny" | "ask";
export type InterceptMatchTarget = "tool_name" | "tool_input" | "tool_call";
export type InterceptMatchType = "string" | "regex";

export interface InterceptRule {
  id: number;
  name: string;
  enabled: boolean;
  priority: number;
  match_target: InterceptMatchTarget;
  match_type: InterceptMatchType;
  pattern: string;
  action: InterceptAction;
  message: string;
  timeout_enabled: boolean;
  timeout_seconds: number;
  timeout_action: "deny" | "allow";
  created_at: string;
  updated_at: string;
}

// Global asset blocklist rules.
export type AssetInterceptKind =
  | "exact_domain"
  | "exact_ip"
  | "exact_url"
  | "fuzzy_domain"
  | "fuzzy_ip"
  | "fuzzy_url"
  | "cidr";

// action applies only to task rules: block prohibits testing, allow defines the allowlist.
export type AssetInterceptAction = "block" | "allow";

// Task asset rule input for task creation and detail editing.
export interface AssetInterceptRuleInput {
  action: AssetInterceptAction;
  kind: AssetInterceptKind;
  pattern: string;
  note: string;
  enabled: boolean;
}

export interface AssetInterceptRule {
  id: number;
  enabled: boolean;
  action?: AssetInterceptAction; // Global rules omit action and always block; task rules distinguish block/allow.
  kind: AssetInterceptKind;
  pattern: string;
  note: string;
  builtin: boolean;
  created_at: string;
  updated_at: string;
}

export interface TaskToolGrant {
  id: number;
  task_id: string;
  scope: "command" | "input" | "tool" | "all";
  tool_name: string;
  command: string;
  created_at: string;
}

export interface InterceptPending {
  decision_source?: "rule" | "model" | "unknown" | "";
  id: number;
  rule_id?: number;
  conversation_id?: number;
  task_id?: string;
  agent_name: string;
  tool_name: string;
  // eslint-disable-next-line @typescript-eslint/no-explicit-any
  tool_input: Record<string, any>;
  status: "pending" | "allowed" | "denied" | "timeout";
  reason: string; // Rule message or model decision reason, with a model prefix for model decisions.
  decided_at?: string;
  created_at: string;
}

// JudgeConfig globally configures fallback model approval when no interception rule matches.
export interface JudgeConfig {
  enabled: boolean;
  profile_id: number; // 0 follows the active/default profile.
  prompt: string; // Decision prompt; unset GET responses return the full built-in template.
  timeout_seconds: number; // Model-call timeout.
  fail_action: "allow" | "ask" | "deny"; // Fallback for model errors, timeout, or unparseable results.
  ask_timeout_seconds: number; // Manual approval wait timeout after the model selects ask.
  ask_timeout_action: "allow" | "deny"; // Default action after approval timeout.
}

export interface InterceptApprovalFilter {
  status?: InterceptPending["status"];
  decision_source?: "rule" | "model" | "unknown";
}

// InterceptApprovalRow enriches InterceptPending with conversation/task and rule context.
export interface InterceptApprovalRow extends InterceptPending {
  conv_title: string; // "" if no linked conversation
  conv_agent_key: string; // "" if no linked conversation
  rule_name: string; // "" if rule was deleted
}

// Asset synchronization from ScopeSentry.
export interface SSProject {
  id: string; // MongoDB ObjectID — used as filter.project
  name: string;
  logo?: string;
  AssetCount?: number;
  tag?: string;
}

export interface SSTask {
  id: string;
  name: string; // used as filter.task
  status?: number;
  progress?: number;
  creatTime?: string;
  endTime?: string;
}

// ConvTokenSummary — one conversation's token total (+ profile/date) for merging
// chat usage into the dashboard token stats. GET /api/tokens/conversations.
export interface ConvTokenSummary {
  llm_profile_id: number | null;
  created_at: string;
  input_tokens: number;
  output_tokens: number;
  cache_read_tokens: number;
  cache_write_tokens: number;
}

// ---- Command recording (Bash execution history) ----
export interface CommandRecord {
  id: number;
  exploration_id: number;
  worker: string;
  tool: string;
  command: string; // raw tool input (JSON)
  output: string;
  is_error: boolean;
  created_at: string;
}

// Per-tool /commands/stats counts; errors is the failed-call subset.
export interface ToolStat {
  tool: string;
  total: number;
  errors: number;
}

// ---- LLM recording ----
export interface LLMRecordItem {
  id: number;
  ts: string;
  model: string;
  profile_name: string;
  session_id: string;
  task_id: string;
  worker: string;
  latency_ms: number;
  input_tokens: number;
  output_tokens: number;
  cache_read: number;
  cache_write: number;
  status: string;
  error?: string;
}

export interface LLMRecordDetail extends LLMRecordItem {
  request_body: string;
  response_body: string;
  // Raw provider HTTP: the complete buildBody() request, including tool schemas,
  // and original SSE response frames. Normalized request_body/response_body above
  // omit schemas and tool_use blocks. Older records leave these empty.
  raw_request?: string;
  raw_response?: string;
}

// One distinct task with its LLM-record count (task picker on the records page).
export interface LLMTask {
  task_id: string;
  count: number;
}

// The exact JSON sent to the review model, retained for all model verdicts.
export interface InterceptReviewInput {
  version: number;
  background?: {
    // worker_summary is retained only for immutable v2/v3 snapshots.
    source: "user_message" | "worker_summary";
    text: string;
    truncated?: boolean;
  };
  // Version 1 snapshots are immutable and remain readable in historical audits.
  task?: {
    task_id: number;
    description: string;
    goal: string;
    constraints: { id: number; kind: string; text: string; origin: string; created_at: number }[];
    truncated?: boolean;
  };
  working_directory?: string;
  worker_intent?: string;
  turn_input?: string;
  background_truncated?: boolean;
  // Legacy v1/v2 snapshots only; v3 never sends execution history.
  history?: {
    tool_use_id: string;
    tool: string;
    arguments_preview: string;
    result: string;
    status: "succeeded" | "failed";
    truncated?: boolean;
  }[];
  history_truncated?: boolean;
  correlation?: "exact" | "ambiguous" | "unavailable";
  tool_name: string;
  arguments: Record<string, unknown>;
}

// Immutable review snapshot plus separately recorded execution outcome.
export interface InterceptAudit {
  model_input?: InterceptReviewInput;
  model_input_digest?: string;
  run_id?: string;
  tool_use_id?: string;
  correlation: "exact" | "ambiguous" | "unavailable";
  input_digest: string;
  user_message: string;
  user_truncated?: boolean;
  context:
    | { kind: string; tool?: string; tool_use_id?: string; text: string; is_error?: boolean; truncated?: boolean }[]
    | null;
  context_truncated?: boolean;
  captured_at: string;
  model_fallback?: boolean;
  initial_action: "allow" | "ask" | "deny";
  initial_reason: string;
  effective_action?: "allow" | "deny";
  decision_reason?: string;
  rule_name?: string;
  grant_scope?: "once" | "command" | "input" | "tool" | "all" | "";
  config_digest?: string;
  profile_id?: number;
  execution_status: "not_started" | "not_executed" | "awaiting_result" | "succeeded" | "failed" | "unknown";
  output?: string;
  output_truncated?: boolean;
  execution_ended_at?: string;
}
export interface InterceptDetail extends InterceptApprovalRow {
  audit: InterceptAudit | null;
}

export type TrafficEvidenceRole = "baseline" | "proof" | "verification" | "supporting";
export interface TrafficEvidenceRef {
  traffic_id: string;
  role?: TrafficEvidenceRole;
  note?: string;
}
export interface TrafficEvidenceSnapshot {
  id: string;
  source_traffic_id: string;
  captured_at: number;
  url: string;
  method: string;
  status: number;
  content_type: string;
  req_head?: string;
  resp_head?: string;
  req_hash: string;
  resp_hash: string;
  req_len: number;
  resp_len: number;
}
export interface FindingTrafficBinding {
  id: string;
  finding_id: string;
  snapshot_id: string;
  role: TrafficEvidenceRole;
  note: string;
  position: number;
  created_at: string;
  snapshot: TrafficEvidenceSnapshot;
}
export interface FindingTraffic {
  finding_id: string;
  version: number;
  report_version: number;
  bindings: FindingTrafficBinding[];
}
export interface EvidenceBodyPreview {
  content: string;
  offset: number;
  total: number;
  next_offset: number;
  truncated: boolean;
  binary: boolean;
}
export interface FindingTrafficDetail {
  binding: FindingTrafficBinding;
  request: EvidenceBodyPreview;
  response: EvidenceBodyPreview;
}

/* GET /api/update/check compares the current version with GitHub's latest stable release. */
export interface UpdateCheck {
  /* Current running version; development builds use dev or suffixed git describe values. */
  current: string;
  /* Runtime form. Docker updates affect only the writable layer; recreation restores the image version. */
  mode: "docker" | "binary";
  os: string;
  arch: string;
  repo: string;
  /* Whether a previous binary (counterweight.old) is available for rollback. */
  has_backup: boolean;
  /* Whether this build verifies release signatures; absent on older backends. */
  verifies_signatures?: boolean;
  /* Self-update bootstrap result, such as failed replacement or rollback; empty if nothing occurred. */
  boot_notice?: string;
  rolled_back?: boolean;
  /* GitHub query failure reason; subsequent fields are absent on failure. */
  error?: string;
  latest?: string;
  notes?: string;
  html_url?: string;
  published_at?: string;
  /* Release-package name for the platform and whether the release actually contains it. */
  asset?: string;
  asset_available?: boolean;
  size?: number;
  has_update?: boolean;
  /* Whether versions are comparable; false for development builds, disabling one-click updates. */
  comparable?: boolean;
  /* Explanation when comparable is false. */
  reason?: string;
}

/* One update-progress event from /api/update/stream. */
export interface UpdateProgress {
  phase: "idle" | "downloading" | "verifying" | "extracting" | "staged" | "failed";
  /* 0–100 only during download; -1 for other stages. */
  percent: number;
  message: string;
  version?: string;
  error?: string;
}

/* One cataloged recon tool's install state, from /api/recontools/status. */
export interface ReconToolStatus {
  key: string;
  repo: string;
  description: string;
  installed: boolean;
  /* true = installed into Counterweight's own managed directory by this installer; false = found on PATH already. */
  managed: boolean;
  path?: string;
  version?: string;
}

/* One install-progress event from /api/recontools/stream?tool=<key>. */
export interface ReconToolProgress {
  tool: string;
  phase: "downloading" | "verifying" | "extracting" | "done" | "failed";
  /* 0–100 only during download; -1 for other stages. */
  percent: number;
  message: string;
  version?: string;
  error?: string;
}

// Original execution selected from an approval, never submitted to the reviewer.
export interface InterceptExecution {
  conversation_id: number | null;
  task_id: string | null;
  session: string;
  seq: number;
  items: Activity[];
}

// Structured task report for the console (GET /api/report?format=json).
export interface ReportCount {
  key: string;
  count: number;
}

export interface ReportCodeLocation {
  file: string;
  lines: string[];
}

export interface ReportClassInfo {
  key: string;
  label: string; // "" when the finding has no class
  cwe?: string; // e.g. "CWE-89"
  owasp?: string; // e.g. "A03:2021 Injection"
}

// Recorded evidence split into its source-to-sink trace and labelled fields.
export interface ReportEvidenceTrace {
  parsed: boolean;
  steps: { kind: string; location?: string; detail: string }[];
  fields: { label: string; text: string }[];
}

export interface ReportKeyAction {
  finding_id: number;
  title: string;
  severity: string;
  action: string;
}

export interface ReportFinding {
  id: number;
  title: string;
  class: string;
  class_info: ReportClassInfo;
  severity: string;
  status: string;
  status_label: string;
  next_action?: string; // absent once the finding is closed
  created_at: string;
  summary: string;
  evidence: string;
  trace: ReportEvidenceTrace;
  report: string;
  report_stale: boolean;
  code_locations: ReportCodeLocation[];
  traffic: { id: number; role: string; method: string; url: string; status: number; note: string }[];
  // Code reviews only: what happened after the finding was reported.
  lifecycle?: ReportFindingLifecycle;
  stage?: string;
}

export interface ReportFindingLifecycle {
  verification: string; // confirmed | rejected | needs_validation | ""
  fix: string; // Mitigation tab state of the attempt shown, or ""
  pr_url?: string;
  pr_state?: "open" | "merged" | "closed";
  recheck: string; // rejected = gone after merge, confirmed = still present
  verified_at?: string;
  pr_opened_at?: string;
  rechecked_at?: string;
}

export interface ReportRecord {
  id: number;
  state: string;
  origin: string;
  created_at: string;
  summary: string;
  issue?: string;
  removal?: string;
}

export interface ReportView {
  kind: "code_review" | "pentest";
  title: string;
  goal: string; // task instructions as stored
  objective: string; // reader-facing statement of what the task set out to do
  task_id: string;
  status: string;
  status_label: string;
  document_id: string;
  fingerprint: string; // SHA-256 of the reported findings
  posture: string;
  key_actions: ReportKeyAction[];
  version: string;
  models: string[];
  generated_at: string;
  created_at?: string;
  first_run_at?: string;
  completed_at?: string;
  execution: { kind: string; headline: string; detail: string };
  execution_isolation?: string; // How the task was isolated, as the exports word it; absent on older backends.
  draft: boolean;
  severity: { critical: number; high: number; medium: number; low: number; info: number };
  review: ReportCount[];
  open_reviews: number;
  code_review?: {
    repository: string;
    url: string;
    ref: string;
    commit: string;
    mode: string;
    inferred: boolean;
    target_source: string;
    mode_label: string;
    blob_base?: string; // "<repo>/blob/<commit>/": append "<file>#L<a>-L<b>"
  };
  cited_files: { file: string; lines: string[]; findings: number }[];
  scope: { kind: string; target: string; source: string }[];
  assets: ReportCount[];
  coverage?: { tested: number; total: number };
  findings: ReportFinding[];
  lifecycle?: ReportCount[]; // findings per lifecycle stage (code reviews only)
  conclusions: ReportRecord[];
  attempts: ReportRecord[];
  limitations: string[];
  truncated: boolean;
}
