# Security model

Counterweight runs autonomous agents that execute real commands and send real network traffic. This
page explains what protects the console, the operator and the targets, and, just as important,
what it does **not** protect. Read it before you run your first engagement.

- [Summary](#summary)
- [Authentication and sessions](#authentication-and-sessions)
- [Scope and authorization](#scope-and-authorization)
- [Approval gate](#approval-gate)
- [Custom command and script tools](#custom-command-and-script-tools)
- [Network destination checks](#network-destination-checks)
- [Credential masking for executed commands](#credential-masking-for-executed-commands)
- [Files, keys and permissions](#files-keys-and-permissions)
- [Known limits](#known-limits)
- [Reporting a vulnerability](#reporting-a-vulnerability)

## Summary

| Control | What it does | What it does not do |
| --- | --- | --- |
| Loopback-only HTTP, HTTPS otherwise | Keeps the console off the network unless you supply TLS | Replace a firewall or VPN |
| Single admin login, bearer tokens | Protects the API; cookies never authorize API calls | Provide multi-user roles |
| Task scope and asset blocking | Tells agents what they may touch; blocks listed assets for native HTTP tools | Confine arbitrary programs the agents run |
| Approval gate | Holds unmatched shell executions for a human when enabled | Act as an OS sandbox |
| Credential masking | Removes known server secrets from executed commands' environment | Stop filesystem reads or unknown secrets |
| Execution isolation | Confines agent file access to each task (default); optionally runs each task's commands in its own container, without the server's environment. See [EXECUTION-ISOLATION.md](EXECUTION-ISOLATION.md) | Restrict the network, or protect against a container or kernel escape |
| API tokens | Let a CI workflow start and read pull request reviews, and nothing else | Open the console or reach settings |
| Authorization check | Sends the credentials of a policy only to the one target it names: no proxy, no redirects, no model, and none of them in its report. Test accounts stored on a task are encrypted, cannot be read back, and are used by this check and, where you switch it on per account, by a tool that sends the agents' requests as that role without giving them the credentials, under the approval gate and the blocked targets. See [authorization-checks.md](authorization-checks.md) | Test a request the policy does not list, keep the target's own data out of the excerpt of a failed check, or catch a credential the target returns to an agent in an unusual transformed form |

> [!IMPORTANT]
> Agent commands run with the privileges of the Counterweight process. Run it on a dedicated host,
> VM or container that holds nothing else of value, and keep the console off the public internet.

## Authentication and sessions

- **First owner.** A fresh installation lands on `/setup`. Claiming it requires a one-time token
  printed on the backend machine (`counterweight -setup-token`, or
  `docker compose exec counterweight /app/counterweight -setup-token`). The command reads the local
  signing key without starting another server or contacting PostgreSQL. Treat the token as a
  secret.
- **Single administrator.** The username is fixed as `COUNTERWEIGHT`. Passwords must be at
  least 8 characters and at most 72 bytes; setup, password change and the reset command all enforce
  it on the server.
- **API authentication** requires `Authorization: Bearer <token>`. The token is never accepted in a
  URL. Event streams (`EventSource` cannot set headers) authenticate with a stream ticket: the
  console calls `POST /api/auth/stream-ticket` with its Bearer token and opens the stream with the
  returned `?ticket=`. A ticket is random, valid for about 60 seconds, usable once, bound to the
  caller's session, and accepted only on stream routes, so access logs never record a reusable
  credential. Cookies never authorize API requests. The web UI keeps its JWT in origin-scoped
  `localStorage` and uses only a non-secret `counterweight_session=1` cookie for page redirects.
  API clients that relied on cookie-only or `?token=` authentication must switch to the Bearer header.
- **Sessions.** Each login creates a stored session that the token names. Signing out
  (`POST /api/auth/logout`) or `POST /api/auth/logout-all` revokes it on the server, a revoked token
  is refused on its next request, and open streams close within a few seconds. Changing or resetting
  the password invalidates every token. Tokens issued before sessions existed stay valid until they
  expire or the password changes; signing out with one retires all such tokens.
- **Sign-in limits.** Password checks are limited per network (an IPv4 /24 or an IPv6 /64 counts
  as one client) and in total. When strangers use up the shared allowance, sign-in from the host
  itself and from a browser that signed in before is still admitted, each within a small bound of
  its own. The browser proves that with an `HttpOnly` cookie sent only to the sign-in endpoint; it
  authorizes nothing and a password change retires it. A browser that never signed in can still be
  kept waiting by such a flood.
- **Browser hardening.** Every response carries `X-Content-Type-Options: nosniff`,
  `Referrer-Policy: no-referrer`, `X-Frame-Options: DENY` and
  `Content-Security-Policy: frame-ancestors 'none'`, plus `Strict-Transport-Security` over TLS.
  There is no script policy yet (see Known limits).
- **Password-bound sessions.** Tokens are bound to the current password generation. Changing the
  password or running a reset invalidates earlier tokens on their next authenticated request,
  without a restart. A web password change refreshes the current browser's token; other sessions
  must sign in again. Requests already admitted, including open streams, are not cancelled.
- **Transport.** The web client refuses non-loopback HTTP management requests before reading or
  sending credentials. The [agent adapter](../adapters/agent/README.md) enforces the same
  HTTPS-or-loopback policy.
- **Terms of Use.** Signing in requires accepting the in-app Terms of Use (see the README's
  [Responsible use](../README.md#responsible-use) section).

## Scope and authorization

- **User-supplied authorization.** A task created by the user carries the original prompt as prior
  authorization. Explicit hosts, IPs and CIDRs in that prompt receive operator scope grants;
  manually selected targets or scope also count. Exact hostnames stay exact and do not authorize the
  parent domain. See [permission-holds.md](permission-holds.md).
- **Coverage proposals are not grants.** Agent and automatic scope entries are proposals an operator
  can approve in the task **Overview**. Manual asset attachments and approved scope from directly
  linked source tasks also grant reads. Generated child-task text, discoveries and agent proposals
  do not become user authorization on their own.
- **Captured traffic follows the grants.** Task agents read captured traffic, and attach it to
  findings, only for hosts covered by those grants. A host an agent discovered or recorded as an
  asset stays unreadable until an operator approves it in scope.
- **Company/ICP approval** captures the current asset identities and company network rules, so later
  attribution changes cannot expose additional stored credentials. Restored legacy company/ICP
  scopes without a recorded grant require explicit approval in Overview.
- **Intent admission.** All supplied target anchors are validated before an intent is stored;
  invalid IDs cannot silently become an unanchored direction. Workers recheck persisted anchors
  against the current task and global asset policy before each fresh or resumed model call, so an
  operator's policy change applies to queued work. Malformed IDs, deleted targets or unavailable
  policy stores stop anchored work. Unanchored reconnaissance keeps its existing behavior.
- **Built-in deny rules** ship enabled in **Interception rules** and block clearly destructive
  operations, such as recursive forced deletion, formatting or overwriting disks, shutdown/reboot,
  flushing firewall rules, dropping or truncating databases, flushing caches and HTTP `DELETE` or
  deletion/bulk-clearing endpoints. Additional defaults block cross-platform disk erasure and
  explicit network flood modes; approval rules ask before recursive Windows deletion, broad network
  scans, credential store dumping and uploads from known sensitive credential locations.
  They are pattern rules you can review, edit or disable; pattern
  matching reduces accidents but cannot catch every equivalent command.

## Approval gate

With shell interception and a restrictive tool-input rule enabled (**Interception rules**), every
unmatched Bash, `shell_open` or `shell_send` execution waits for a human decision.

- The model judge cannot approve that fallback. Cancellation or the five-minute timeout denies it.
- Your explicit allow/deny rules keep priority, so repeated authorized workflows can run under
  narrowly scoped allow rules.
- When a task tool needs permission, the task is paused with a persisted **permission hold** that
  survives restarts; approving does not resume the task by itself. See
  [permission-holds.md](permission-holds.md).
- Decisions are reviewed in **Approval records**, the in-task **Interception approvals** panel and
  the approval cards in conversations.
- A request by an agent to create or change a skill, a custom tool or an MCP server always waits
  for a person, whatever the interception settings: these definitions decide what agents run
  later. Without a decision within five minutes the request is denied.

The gate inspects parsed command syntax; **it is not an OS sandbox**. The exact rules for
`curl`/`wget`, `eval`, `source`, traps, aliases and generated programs are in
[approval-gate.md](approval-gate.md).

## Custom command and script tools

Custom command tools (**Tools**) render placeholders as data arguments, never as generated shell
syntax. Rendering follows the executor: Bash on Unix, PowerShell on Windows.

- **Declared parameters only.** Agent-invoked custom command and HTTP tools accept only top-level
  parameters declared in their schema. Extra keys are rejected before execution. Declared defaults
  and the generated args fallback remain supported. A command tool parameter declared `integer`,
  `number` or `boolean` must arrive as that type.
- **No parameter-derived code.** Parameters are refused where a shell or interpreter would treat them
  as program text: expansion and arithmetic, legacy backticks, here-document bodies, unmatched
  escapes, nested `bash -c`/`sh -c` strings (also when another program such as `xargs`, `find -exec`
  or a command name held in a variable starts the shell), `eval`, `trap` actions, `mapfile`
  callbacks, `source` operands, shell stdin programs, a script operand that is a process
  substitution or a stdin or descriptor path, Python `-c` source, `-m` module names and their
  preceding options, PowerShell `Invoke-Expression` (including aliases and module-qualified names),
  `ScriptBlock.Create` and string-to-script-block casts, nested `powershell`/`pwsh`, Unix shell and
  `wsl` programs and their piped input, `Start-Process` when it launches a command interpreter, and
  `Add-Type`.
- **No parameter-derived arithmetic or variable names.** Bash evaluates some quoted operands again
  and runs command substitutions found in an array subscript. Parameters are refused in `[[ ]]`
  numeric comparisons, `let`, `(( ))`, C-style loop headers, subscripts and substring offsets, as
  operands of `-v` tests and of `declare`, `local`, `export`, `readonly` and `typeset`, and as
  values of integer or nameref variables.
- **Conservative data-flow tracking.** Parameter influence is followed through Bash variables,
  arrays, loops, command substitutions, `set` positional arguments, `printf -v`, `unset`,
  function calls and local assignments, through variables Bash fills without an assignment (`read`,
  `mapfile`, `REPLY`, `BASH_REMATCH`, `$_`), and into the positional parameters of a child shell
  whose fixed program evaluates them. In PowerShell every variable is treated as influenced unless
  each statement that gives it a value assigns fixed data: loop, pipeline, parameter and
  cmdlet-written variables are influenced, and a fixed CMD program is refused once the template
  writes parameter data to an environment variable. Merging is conservative, so some legitimate
  templates may need rewriting to pass values through explicit data channels.
- **`cmd.exe` parameters are rejected**, because CMD can reinterpret arguments as program syntax.
  Fixed CMD programs without interpolated arguments remain available.
- **Safe patterns** remain supported: keep the program fixed and pass values as positional data,
  for example `bash -c 'printf "%s" "$1"' fixture '{value}'` or
  `python -c 'import sys;print(sys.argv[1])' '{value}'`, or use the Python script tool's JSON input
  (scalar inputs are available as `TOOL_<NAME>`). Script filenames require a fixed `--` boundary.
  A nested PowerShell host joins every word after its command into program text, so it takes data
  only as arguments of a fixed script: `powershell -File fixed.ps1 '{value}'`.
- **HTTP tool templates keep their structure.** Each value is encoded for the place its placeholder
  sits in: percent-encoded in a URL path, query or fragment, restricted to host characters next to
  the scheme or host, escaped inside a JSON string, written as one JSON value in a bare JSON
  position, and form-encoded in a form body. A template or body that consists of nothing but a
  placeholder leaves that whole value to the caller. Header values stored in the tool, and any
  user information in its URL, are replaced by a placeholder in the review record, which agents of
  the task can read; header names, destination, method and supplied values stay visible.

Limits: these checks classify the original command-line syntax. Other interpreters, native programs
with non-standard argument parsers, renamed interpreters, environment startup flags and
program-defined reparsing remain outside their coverage.

## Network destination checks

Native custom HTTP tools enforce the global and task **asset blocks** and the task allowlist
against each rendered destination and every redirect, independently of the agent's stated intent
and of ordinary tool approval.

- Direct requests resolve once and check IP/CIDR block rules before dialing an approved numeric
  address. TLS still verifies the original hostname. URL grants are checked again after DNS
  resolution; domain/URL grants cannot override explicit IP blocks.
- With IP blocks configured, HTTP/HTTPS and SOCKS5 proxies receive a locally resolved, approved
  numeric target. Resolution failure, a fully blocked result or tunnel refusal stops the request
  without falling back to proxy-side DNS or direct routing.
- Native hostname requests through the recording proxy bind the actual TCP connection to the checked
  numeric origin; the proxy rechecks the original task policy before dispatch.
- Empty or entirely disabled allowlists keep their unrestricted behavior; policy-load failures stop
  requests.

This controls the native HTTP tool path. It does not attest an external proxy's actual peer and does
not provide network isolation for shell, Python or MCP processes.

## Credential masking for executed commands

Executor environments mask these server credential names: `OPENAI_API_KEY`, `ANTHROPIC_API_KEY`,
`ANTHROPIC_AUTH_TOKEN`, `COUNTERWEIGHT_PG_DSN`, `COUNTERWEIGHT_LLM_PROXY`, `PGPASSWORD` and
`POSTGRES_PASSWORD`. Caller overrides cannot restore them; the server and model provider keep their
own environment. Supply task-specific authentication through explicitly configured tool inputs.

This filters known environment variables only. It provides no filesystem or network confinement.

The GitHub token used for fix pull requests is stored privately on the server and is never sent to a
model.

## Files, keys and permissions

- **Traffic evidence.** Opening the traffic store repairs its permissions before use: owner-only
  directories and files on Unix, protected ACLs (runtime user, SYSTEM, Administrators) on Windows.
  Symbolic links or permission failures prevent the store from opening. Export an approved copy of
  the public CA certificate for other client accounts instead of sharing the traffic tree.
- **Credential files.** The Unix installer creates `.env` and `config.json` private. Config reads
  tighten the selected file's permissions; malformed or unprotectable database config fails startup
  without echoing its contents. On Windows use `private-credential.ps1` (see
  [configuration.md](configuration.md#database)).
- **JWT signing key.** Startup protects existing keys and publishes new ones privately without
  replacing a key written by a concurrent startup. Invalid keys fail startup instead of being
  silently replaced. The key filesystem must support hard links.
- **Path safety.** Windows private-file operations reject reparse points (junctions, links) in every
  path component; Linux operations reject symbolic-link ancestors. Use physical directory paths for
  the data directory and a regular file for `config.json`.
- **Integration credentials.** The GitHub token and the Jira credential are private files in the
  data directory. The workspace file API refuses to list, read, download, overwrite or delete
  them, under any spelling of their names. GitHub authentication reaches `git` through the child
  process environment, never its command line or the remote URL. MCP server environment values
  and headers are write-only in the API: responses show a placeholder, and saving the placeholder
  keeps the stored value.
- **Model provider keys.** Keys are never returned by the API. The model HTTP client follows a
  redirect only to the same scheme, host and port it was configured with, and the model-list
  request follows none, so a redirecting gateway cannot collect the key or the prompt at another
  origin.
- **Stored credentials are encrypted.** Model API keys and proxy URLs, MCP environment and header
  values, notification channel settings and web-search keys are encrypted in PostgreSQL with
  AES-256-GCM, each value bound to its own row. The key is a private file outside the database and
  outside the data directory (the `keys` volume under Docker Compose), so a database dump alone
  does not reveal them. Startup refuses to run with a missing or wrong key rather than treating
  ciphertext as a credential. Backup, rotation and recovery are in
  [operations.md](operations.md#credential-encryption-key).
- **Secrets on the command line.** Prefer environment or configuration input; explicit secret flags
  remain visible in the caller's process arguments. Environment variables are not a boundary
  against the same OS account or administrators.

## Known limits

- **Execution isolation is partial.** In the default mode, the file tools of task agents are
  confined to the task's own files, those of chat agents to the conversation's own directory, and
  the agents that read reviewed source have no shell at all. For
  assessment types that do have a shell, commands, scripts, MCP processes and skills still run
  with the server's privileges. The optional container mode moves shell commands into a per-task
  container that cannot see the server's environment or other tasks' files; it works on native
  installs only, withholds the tools that would still start a process on the server, and has been
  verified on Docker Desktop for Windows only. Network access is not restricted in any mode. A
  file that is already hard-linked inside a task's directory to a file outside it remains
  reachable. Status, limits and remaining work are in
  [EXECUTION-ISOLATION.md](EXECUTION-ISOLATION.md). Isolate the whole deployment either way.
- **Pull request text and code are untrusted.** A review of a pull request reads content its
  author controls. The reviewers treat it as data and have no shell or network tools, but a model
  can still be misled into a wrong or missing finding. Results are leads to confirm.
- **Docker is packaging, not isolation.** The image bundles the application and research tools in
  one container with database and provider credentials in its environment.
- **Isolation can be turned off.** With the setting off, agent file tools accept any path the
  server process can read, so content in a hostile repository or target could steer an agent into
  reading files of the Counterweight host and sending them to the model. Every task records the
  mode it ran in, and the report states it.
- **Approval fallbacks.** A tool call that no rule matches is allowed when the model reviewer is
  off. The generic "ask for every shell command" prompt counts as already answered once the
  operator's task request authorized the work; that includes a `curl` or `wget` call whose only
  open point is the client's default startup file. A command that selects its executable, its
  program or its request method at run time still waits for a person in every task. A model
  reviewer that fails applies its failure
  policy, which defaults to asking a person (installations that saved the reviewer settings before
  this default changed keep the value they saved). Add explicit `ask` or `deny` rules for anything
  that must always wait for a person.
- **Secrets outside the encrypted set.** Stored credentials are encrypted (see above), but values
  typed into free-form fields are not: an MCP server's arguments or URL, a model base URL, and
  recorded model requests when recording is on. Do not put tokens in those fields.
- **Update authenticity.** In-app updates verify the archive against `SHA256SUMS` from the same
  release. When the build embeds release public keys (`selfupdate/release-keys.pub`), the updater also
  requires an Ed25519 signature (`SHA256SUMS.sig`) over that file and refuses the update otherwise.
  This repository embeds the project's key (id `5651d2fd00300539`). A build with no embedded key
  does not verify signatures: the checksum then only detects corruption, since anyone able to replace
  the release assets can replace both files. The update screen and log say which state applies.
- **Workspace symlinks.** The workspace file API validates paths lexically; a symbolic link placed
  inside the data directory is followed. Integration credential files are protected separately.
- **No script Content-Security-Policy.** The console sends a policy that denies framing and limits
  images, fonts, media and plugins to its own origin (plus `data:` and `blob:`), so text written by
  a model cannot make the browser load a remote resource. It still sends no script or style
  policy, because the static export relies on inline scripts. Company logos that point at another
  origin fall back to the company's initial.
- **AI output can be wrong.** Findings and actions may be incomplete, inaccurate or harmful. Verify
  before acting, and prefer staging environments when production impact is unacceptable.
- **Verification status.** What has and has not been validated is recorded in
  [VERIFICATION.md](VERIFICATION.md).

## Reporting a vulnerability

See [SECURITY.md](../SECURITY.md). Please do not open public issues for security problems.
