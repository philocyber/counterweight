import type { ReportView } from "@/lib/types";

// Fictional structured report for the backend-free demo. No real target, repository or finding.
export function mockReportView(task?: string): ReportView {
  const id = task ?? "7";
  return {
    kind: "code_review",
    title: "Code review: acme-labs/orders-api",
    goal: "Perform a white-box security code review of the fictional repository acme-labs/orders-api. Review authentication, order handling and file export paths; record each confirmed vulnerability with a file:line data flow.",
    objective:
      "White-box security review of the source code of acme-labs/orders-api (main, commit 4f2a9c1e7b3d), comprehensive review. The goal is to find and confirm exploitable vulnerabilities, each backed by a traced code path, without sending traffic to any deployed system.",
    task_id: id,
    status: "running",
    status_label: "Running",
    document_id: `CW-${id}-20261106-183000`,
    fingerprint: "3f8a1c0d9e7b6a5f4e3d2c1b0a9f8e7d6c5b4a3f2e1d0c9b8a7f6e5d4c3b2a1f",
    posture: "3 findings: 2 open, 1 dismissed (false positive, duplicate, ignored or risk accepted).",
    key_actions: [
      {
        finding_id: 301,
        title: "SQL injection in order search sort parameter",
        severity: "critical",
        action: "Fix the confirmed issue and verify it after the merge.",
      },
      {
        finding_id: 302,
        title: "CSV export allows formula injection",
        severity: "high",
        action: "Review the evidence and confirm or reject the finding.",
      },
    ],
    version: "demo",
    models: ["Demo profile (demo-model)"],
    generated_at: "2026-11-06T18:30:00Z",
    created_at: "2026-11-06T16:05:00Z",
    execution: {
      kind: "in_progress",
      headline: "Task in progress",
      detail: "The task has not finished. Findings shown are provisional; an empty or short list is not a conclusion.",
    },
    draft: true,
    severity: { critical: 1, high: 1, medium: 1, low: 0, info: 0 },
    review: [
      { key: "pending", count: 1 },
      { key: "confirmed", count: 1 },
      { key: "false_positive", count: 1 },
    ],
    open_reviews: 1,
    code_review: {
      repository: "acme-labs/orders-api",
      url: "https://github.com/acme-labs/orders-api",
      ref: "main",
      commit: "4f2a9c1e7b3d5a6f8e0c1b2d3a4f5e6d7c8b9a0f",
      mode: "comprehensive",
      inferred: false,
      target_source: "Recorded when the review was launched.",
      mode_label: "Comprehensive review",
      blob_base: "https://github.com/acme-labs/orders-api/blob/4f2a9c1e7b3d5a6f8e0c1b2d3a4f5e6d7c8b9a0f/",
    },
    cited_files: [
      { file: "internal/export/csv.go", lines: ["41-58"], findings: 1 },
      { file: "internal/http/orders.go", lines: ["112-130", "144"], findings: 1 },
      { file: "internal/store/orders.go", lines: ["77-81"], findings: 1 },
    ],
    scope: [],
    assets: [],
    findings: [
      {
        id: 301,
        title: "SQL injection in order search sort parameter",
        class: "sqli",
        class_info: { key: "sqli", label: "SQL injection", cwe: "CWE-89", owasp: "A03:2021 Injection" },
        severity: "critical",
        status: "confirmed",
        status_label: "Confirmed",
        next_action: "Fix the confirmed issue and verify it after the merge.",
        created_at: "2026-11-06T17:10:00Z",
        summary:
          "The `sort` query parameter of `GET /orders` reaches a string-built `ORDER BY` clause without an allow-list.",
        evidence:
          'internal/http/orders.go:112-130 reads r.URL.Query().Get("sort") and passes it to store.Search.\ninternal/store/orders.go:77-81 builds: "SELECT ... ORDER BY " + sort\nLocal PoC (fixture database): sort=(CASE WHEN 1=1 THEN id ELSE total END) changed row order.',
        report:
          "## Impact\n\nAn authenticated user can run arbitrary expressions inside the `ORDER BY` clause and infer data from other tenants.\n\n## Reproduction\n\n1. Start the service against the fixture database.\n2. Request `GET /orders?sort=(CASE WHEN 1=1 THEN id ELSE total END)`.\n3. Compare the row order with `sort=id`.\n\n```sql\nSELECT id, total FROM orders WHERE tenant_id = $1 ORDER BY (CASE WHEN 1=1 THEN id ELSE total END)\n```\n\n| Request | Result |\n|---|---|\n| `sort=id` | baseline order |\n| `sort=(CASE …)` | expression evaluated |\n\n> Remediation: map `sort` to an allow-list of column names before building the query.",
        trace: {
          parsed: true,
          steps: [
            {
              kind: "entrypoint",
              location: "internal/http/orders.go:112-130",
              detail: "GET /orders reads the sort query parameter.",
            },
            {
              kind: "sink",
              location: "internal/store/orders.go:77-81",
              detail: "store.Search concatenates sort into the ORDER BY clause.",
            },
          ],
          fields: [{ label: "Remediation", text: "Map sort to an allow-list of column names." }],
        },
        report_stale: false,
        code_locations: [
          { file: "internal/http/orders.go", lines: ["112-130"] },
          { file: "internal/store/orders.go", lines: ["77-81"] },
        ],
        traffic: [],
      },
      {
        id: 302,
        title: "CSV export allows formula injection",
        class: "csv_injection",
        class_info: { key: "csv_injection", label: "CSV injection", cwe: "CWE-1236", owasp: "A03:2021 Injection" },
        severity: "high",
        status: "pending",
        status_label: "Pending review",
        next_action: "Review the evidence and confirm or reject the finding.",
        trace: { parsed: false, steps: [], fields: [] },
        created_at: "2026-11-06T17:40:00Z",
        summary:
          "Customer-controlled order notes are written to CSV exports without neutralising leading `=`, `+`, `-` or `@`.",
        evidence: "internal/export/csv.go:41-58 writes order.Note verbatim into the Notes column.",
        report: "",
        report_stale: false,
        code_locations: [{ file: "internal/export/csv.go", lines: ["41-58"] }],
        traffic: [],
      },
      {
        id: 303,
        title: "Verbose error messages on order lookup",
        class: "information_disclosure",
        class_info: {
          key: "information_disclosure",
          label: "Information disclosure",
          cwe: "CWE-200",
          owasp: "A05:2021 Security Misconfiguration",
        },
        severity: "medium",
        status: "false_positive",
        status_label: "False positive",
        trace: { parsed: false, steps: [], fields: [] },
        created_at: "2026-11-06T18:02:00Z",
        summary: "Initially suspected stack traces in 500 responses; the handler wraps errors before responding.",
        evidence: "internal/http/orders.go:144 returns a generic message; the stack trace is only logged.",
        report: "",
        report_stale: false,
        code_locations: [{ file: "internal/http/orders.go", lines: ["144"] }],
        traffic: [],
      },
    ],
    conclusions: [
      {
        id: 41,
        state: "confirmed",
        origin: "worker",
        created_at: "2026-11-06T17:20:00Z",
        summary: "Session tokens are compared with a constant-time function; no timing issue reproduced.",
      },
    ],
    attempts: [
      {
        id: 52,
        state: "done",
        origin: "planner",
        created_at: "2026-11-06T16:30:00Z",
        summary: "Map authentication and order-handling entry points.",
      },
      {
        id: 53,
        state: "blocked",
        origin: "planner",
        created_at: "2026-11-06T17:55:00Z",
        summary: "Validate the export path against the fixture database.",
        issue: "Fixture database container was not running.",
      },
    ],
    limitations: [
      "This export is generated from stored task records. It is not an independent verification of findings.",
      "Results apply to the commit shown. Later changes to the repository are not covered.",
      "Findings are supported by source-to-sink code traces and, where recorded, local proof-of-concept tests. No deployed instance was tested unless a finding says so.",
      "File-level review coverage is not tracked: a file not cited by a finding is not established as reviewed or free of issues.",
      "Asset scope and network coverage records do not apply to a source review and are omitted from this report.",
    ],
    truncated: false,
  };
}
