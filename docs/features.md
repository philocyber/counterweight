# Feature guide

A closer look at the workflows behind the console. For setup, start with the
[README](../README.md#quickstart).

- [Task workspace](#task-workspace)
- [White-box code review](#white-box-code-review)
- [Findings, evidence and reports](#findings-evidence-and-reports)
- [Retesting a finding](#retesting-a-finding)
- [Asset sync from ScopeSentry](#asset-sync-from-scopesentry)
- [Coding-agent integration](#coding-agent-integration)
- [Extending Counterweight](#extending-counterweight)

## Task workspace

Each task opens a workspace with these tabs:

| Tab | What it shows |
| --- | --- |
| **Session** | The planner, worker and main-agent conversations, step by step |
| **Overview** | Goal, status, scope, and coverage proposals awaiting approval |
| **Results ledger** | Searchable list of the intents, facts and findings the agents recorded |
| **Exploration paths** | The exploration graph: which direction came from which facts |
| **Broadcast board** | Live feed of what the agents publish to the task graph, with neighboring nodes |
| **Findings** | Findings recorded in this task |
| **Retest** | Past retest conclusions and evidence; start a new retest |
| **Mitigation** | Code review tasks only: generate a verified fix and open a pull request |
| **Test assets** | Assets in this task's scope |
| **Asset coverage graph** | In-scope assets with tested ones highlighted |
| **Interception approvals** | Commands waiting for, or decided by, a human |
| **Report** | The task report (Markdown preview, download, PDF) |

## White-box code review

Start a review from **Chat**:

```text
/code-review owner/repo [light|balanced|comprehensive]
```

You can also ask in plain language ("review the source of owner/repo"); the chat agent starts the
same kind of task. The repository can be given as `owner/repo` or a `github.com` URL.

1. **Clone.** The repository is cloned into the data directory and the exact commit is recorded,
   so the review is reproducible. The clone is anonymous first, so public reviews never present a
   credential. If that fails and a GitHub token is stored under **System settings → GitHub**, the
   clone is retried with it: give the token *Contents: read* on a private repository to review it.
   The token travels in the git process environment, never in the URL or the command line.
2. **Review.** The planner and workers read the source with the bundled
   `whitebox-code-review-methodology` skill. `light` covers only security-critical files,
   `balanced` is the default, `comprehensive` covers all files with the full workflow.
3. **Verify.** A finding can be checked by an independent verifier against the source. Automatic
   verification can be switched on per review.
4. **Mitigate.** In the **Mitigation** tab, generate a fix: a separate fixer agent writes it and an
   automatic patch review checks it. Nothing is published until you click **Open pull request**.
5. **Pull request.** Opening a PR needs a GitHub fine-grained token with *Contents* and *Pull
   requests* read/write on the reviewed repository, saved in **System settings → GitHub**. It is
   stored privately on the server and never sent to a model.
6. **Post-merge re-check.** After the PR is merged, **Re-check merged code** reads the merged source
   and marks the finding fixed if the vulnerability is gone (otherwise *Still present* or
   *Inconclusive*).

## Findings, evidence and reports

- **Findings** lists every finding across tasks, with flat, grouped-by-task and by-asset views,
  severity and status filters, and a workflow status (pending, confirmed, fixed, false positive).
- **Traffic evidence.** HTTP requests and responses that pass through the recording proxy can be
  bound to a finding, with roles (`baseline`, `proof`, `verification`, `supporting`) and notes. See
  [traffic-evidence.md](traffic-evidence.md).
- **Exports.** Findings export to Markdown (single file or ZIP), CSV, JSON or PDF (credentials
  redacted). Each task's **Report** tab downloads Markdown or PDF. An illustrative report is in
  [example-engagement.md](example-engagement.md).

### SARIF export

The task **Report** tab (**Download SARIF**) and the Findings **Export** dialog (**SARIF**) produce a
SARIF 2.1.0 file for GitHub code scanning and other tools that read it. The same files come from
`GET /api/report?task=<id>&format=sarif` and
`GET /api/exploration/findings/export?format=sarif&scope=all` (both need a session or API key).

- One rule per vulnerability class (with its CWE tag and a `security-severity` score) and one result
  per finding. Findings that are fixed, resolved, risk accepted, ignored, false positive or duplicate
  stay in the file with a `suppressions` entry, so nothing is dropped silently.
- Code-review findings carry the file and line range cited in their evidence (the first one is the
  primary location, the rest are related locations) plus the repository and commit.
- Black-box findings have no source file. They are exported without `locations`, with the affected
  URL or asset in the message and properties. GitHub code scanning ignores results without a
  location, so only code-review findings appear there; other SARIF viewers show all of them.
- Credentials and local checkout paths are redacted, as in the other shareable exports.

To upload a file for the reviewed commit, in a GitHub Actions job with the permission
`security-events: write`:

```yaml
- uses: github/codeql-action/upload-sarif@v3
  with:
    sarif_file: counterweight.sarif
    category: counterweight
```

The action uploads for the commit the workflow runs on. To upload for a specific commit from a
terminal, pass `ref` and `sha` to the action, or use the REST endpoint
`POST /repos/{owner}/{repo}/code-scanning/sarifs`, which takes `commit_sha`, `ref` and the SARIF
file gzipped and base64-encoded in `sarif`.

## Retesting a finding

Click **Retest** on a finding (in the findings list, the finding detail or the task's **Retest**
tab), optionally add a fix version, test conditions or limits, and start it.

- A separate `retester` agent session runs; it does not restart the original task.
- Conclusions are **Still reproducible**, **Fixed** or **Inconclusive**. Each conclusion, its
  evidence and the session link are saved in the finding detail.
- A successful retest that concludes **Fixed** sets the finding's status to Fixed automatically.
  Other outcomes keep the original status. The original evidence and report are always kept.
- The `retester` agent is created on first start and can be configured in **Agents** (prompt, model,
  run budget, tools). Without its own model binding it uses the active profile.
- Retest history is not yet included in the findings export or the task archive, and is not
  auto-linked to traffic captures.

## Asset sync from ScopeSentry

If you already collect assets with [ScopeSentry](https://github.com/Autumn-27/ScopeSentry), import
them instead of collecting twice:

1. Open **Asset sync**, enter the ScopeSentry address and API key.
2. Choose what to sync by project or by task, and pick asset types (domain, subdomain, IP, port,
   site, endpoint, ...).
3. Import. Assets are merged into the company scope and appear in the asset graph.

## Coding-agent integration

Claude Code, Codex and Pi can list tasks, read coverage and findings, and (when explicitly enabled)
create, pause or resume tasks through the [agent adapter](../adapters/agent/README.md). Claude Code
and Codex use a local stdio MCP server; Pi uses a native extension. Reads are enabled by default;
writes require `COUNTERWEIGHT_ALLOW_WRITES=true`.

## The "star on GitHub" invitation

After an installation has created at least two tasks and recorded a finding, the console shows one
small card inviting you to star the project. **Later** hides it for two weeks; the close button or
any answer ends it for good, for every browser, because the answer is stored on the server.

- With a GitHub token stored under **System settings → GitHub**, the button sets the star through
  the GitHub API (`PUT /user/starred`). A fine-grained token needs the account permission
  *Starring: read and write* for that; without it, or without a token, the button opens the
  repository page instead.
- Before showing the card to an installation that has a token, Counterweight asks GitHub once
  whether you have already starred the project, and never shows the card if you have.
- Nothing else is sent anywhere. Counterweight has no telemetry, and an installation without a
  GitHub token makes no request to GitHub for this feature.

## Extending Counterweight

- **Skills**: a bundled library in the `agentskills.io` format (vulnerability classes, methodology,
  recon, code review). Add your own folders with a `SKILL.md`. See [NOTICE](../NOTICE) for the
  provenance of imported skills.
- **Tools**: custom shell, command, script and HTTP tools with typed parameters. See the rules in
  [security-model.md](security-model.md#custom-command-and-script-tools).
- **MCP**: local and remote MCP servers ([configuration](configuration.md#remote-mcp-servers)).
- **Notifications**: alert channels for findings and approvals.
- **Recon tools**: optional one-click installs under **Tools**, added to the agents' `PATH`.
