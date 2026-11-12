# Counterweight v0.1.0

First public release. Counterweight is a self-hosted, AI-assisted security assessment platform:
agents test your running web applications and review your source code, a human approves risky
steps, and findings leave as reports and Jira tickets. It is derived from ARTEX at upstream commit
`160fe13`; original authorship and the AGPL-3.0 terms are preserved (see [NOTICE](../NOTICE) and
[provenance](../docs/PROVENANCE.md)). Counterweight versioning starts at v0.1.0; the upstream
changelog and the frontend package version keep their upstream history.

## What is in this release

**Assess**
- Black-box assessments of web targets with a planner and parallel workers, an asset graph and an
  exploration graph, and captured HTTP traffic bound to findings as evidence.
- Four assessment types, each with its own workflow, agents and tools.
- White-box review of GitHub repositories at three depths, public or private (with a stored token),
  by agents that cannot execute the code, with independent verification of findings against the
  source and supplementary dependency and secret scanners whose results are verified as leads.
- Pull request reviews: a review limited to what a change introduces, started from the console or
  from the bundled GitHub Action through scoped API tokens, with results in the job summary, code
  scanning and the pull request ([guide](../docs/pr-reviews.md)).
- Authorization checks: a policy (personas, requests and the expected outcome of every pair) is
  replayed against a staging target without a model. Inside a task, with test accounts stored
  encrypted on the task (entered in the New task form or later), a policy edited as a matrix of
  requests and roles or as text, a result table by role name and a section in the task report; or
  as a CI job with `counterweight authz`. Per account, the agents can be allowed to send requests as that
  role through the server, without receiving its credentials
  ([guide](../docs/authorization-checks.md)).
- A chat agent that creates tasks, starts reviews and answers questions about results.

**Fix and verify**
- Fix generation with an independent patch review, pull requests and re-check of the merged code.
- One-click retests that report still reproducible, fixed or inconclusive.

**Report and hand off**
- Task reports in Markdown and PDF (technical and executive variants, credentials redacted) and
  SARIF for GitHub code scanning.
- Jira Cloud: one epic per task and one developer-ready issue per finding, in the space you choose,
  with priorities and due dates by severity and status updates as fixes are merged and re-checked.

**Stay in control**
- Fixed: built-in system-command rules avoid URL/path false positives and ordinary disk copies; existing operator edits are preserved.
- New built-in rules block disk erasure and network flood modes, and require approval for recursive
  Windows deletion, broad scans, credential dumping and sensitive credential file uploads.
- Scope, built-in deny rules, an approval gate for unmatched commands, and task permission holds.
- Execution isolation on by default (agent file access confined to each task, self-tested at
  startup, recorded in every report), an optional container mode for native installs that runs each
  task's commands in its own unprivileged container, credentials encrypted at rest and write-only in
  the console,
  revocable sessions, a per-task usage budget, browser security headers and a same-origin redirect
  policy for model providers.
- Fixed: a blocked-target rule for an exact domain also matches the host written with a trailing
  dot (`example.com.`), which names the same host and was let through before.

**Run it**
- Docker Compose with an interactive installer that prints the address and the one-time setup
  token, or a single binary for Linux, macOS and Windows with in-app, checksum-verified updates.
- Any Anthropic, OpenAI-compatible, gateway or self-hosted model; see
  [Choosing and connecting a model](../docs/choose-a-model.md).
- Console in English and Spanish, with a first-run checklist.
- An adapter that lets Claude Code, Codex and Pi read tasks and findings and create or pause tasks.

## Downloads

Platform ZIP archives are built for Linux amd64/arm64, macOS amd64/arm64 and Windows amd64. Each
archive includes the binary with the console embedded, the supervisor script, skills, the
configuration example, documentation and the agent adapter. `SHA256SUMS` lists the archive
checksums. Docker images are built for Linux amd64/arm64 at `ghcr.io/philocyber/counterweight`.

## Verification and limitations

- Execution isolation is partial. By default it confines agent file access to each task while
  commands of assessments that use a shell run with the privileges of the Counterweight process.
  The optional container mode moves those commands into a per-task container on native installs; it
  is not available under Docker Compose yet, withholds some tools and has been verified on Docker
  Desktop for Windows only. No mode restricts network access. Run Counterweight on a dedicated
  host, VM or container. See [execution isolation](../docs/EXECUTION-ISOLATION.md).
- The GitHub Action for pull request reviews has not been run in a real workflow yet.
- Authorization checks have been run against local fixtures only. They test the requests listed
  in the policy, do not create test data, and support sign-in with a single HTTP request or a fixed
  token. Agents sending requests as a stored role has been exercised in one run with one model,
  against a local demo application.
- AI output can be wrong. Verify findings before acting on them.
- What has and has not been validated, including known failing tests, is recorded in
  [docs/VERIFICATION.md](../docs/VERIFICATION.md).
- Use only against systems and code you own or are explicitly authorized to test.

See [setup](../README.md), the [feature guide](../docs/features.md) and the
[agent adapter](../adapters/agent/README.md).
