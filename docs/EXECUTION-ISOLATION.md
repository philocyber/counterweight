# Restricted execution boundary

Status: **file confinement implemented and on by default; per-task containers implemented as an
opt-in mode for native installs; network confinement not implemented.**

What exists today:

- **File confinement (implemented, on by default).** The file tools of every task agent (read,
  list, search, write, edit) are confined to that agent's own roots: the task's directory, the
  repository clone for a code review (read-only), the fix worktree for the fix writer. Absolute
  paths, `..` and symbolic links cannot leave those roots. A chat agent, whether an operator opened
  the conversation or a trigger started it from task data, is confined in the same way to that
  conversation's own directory. A self-test at startup plants a file
  outside a temporary workspace and checks that the tools can neither read nor overwrite it; if
  the self-test fails, tasks do not start. Every task records the mode it ran in, and its report
  states it. The setting, its live status and the self-test are in **System settings**.
- **Per-task containers (implemented, opt-in, native installs).** With the mode `container`
  selected in **System settings**, the shell commands of a task run inside that task's own Docker
  container instead of on the server. The container runs as an unprivileged user (UID 65532) with
  every capability dropped, `no-new-privileges`, a read-only runtime filesystem, a private `/tmp`,
  and limits on processes, memory and CPU. It mounts only the task's own directory (and, where the
  agent's file roots allow it, the repository clone and the fix worktree). Each command starts from
  an empty environment: nothing from the server's environment is inherited, so model-provider keys
  and database credentials are not there to read. A command that exceeds its timeout is stopped by
  removing the whole container, which also stops anything it left running. The container is removed
  when the task finishes, is paused, deleted or archived, and when the server shuts down; leftovers
  from a crash are removed at the next start. A conversation has a container of its own, which is
  removed when each of its turns ends. Before the mode can be selected, a self-test starts a real
  container and checks, from inside it, that a file elsewhere on the host is unreachable, that a
  server environment variable is absent, that writing outside the workspace fails, that the process
  has no privileges, that a timeout stops the command and its children, and that files are shared
  with the server in both directions. If the backend is not ready, tasks do not start: there is no
  silent fallback to running on the server. See [limits of container mode](#limits-of-container-mode).
- **No shell for code reviews (implemented).** The agents of a `code_review` task receive no shell,
  no file-writing tools and no network tools, enforced where tools are assembled. The same holds
  for the standalone review chat agent and for the agents that verify a finding, write its fix
  (which may write only its fix worktree) and review that fix.
- **Policy checks.** Scope, interception rules, the approval gate, destination checks for
  structured HTTP tools, and credential masking in the environment of executed processes.

What does not exist yet: enforcement of where the processes of a black-box assessment may connect,
container mode for a server that itself runs under Docker Compose, and routing of the remaining
process-starting tools through the container. This document states what the complete boundary must
enforce, what has been checked so far, where it has to be integrated, and how it will be accepted.

Run Counterweight on a dedicated host, VM or container that holds nothing else of value, whichever
mode you use. See the [security model](security-model.md#known-limits).

## Choosing a mode

| Mode | Files | Commands | Use it when |
| --- | --- | --- | --- |
| `off` | Server files | Run on the server | You are debugging the tool itself. Not recommended. |
| `confined-files` (default) | Limited to the task's folders | Run on the server, with known server credentials masked | Docker is not available to the server. |
| `container` | Limited to the task's folders | Run in the task's own container, without the server's environment | Native install with Docker Engine or Docker Desktop. |

To enable container mode on a native install, build the image once from the checkout and select the
mode in **System settings**:

```bash
docker build -t counterweight:local .
```

`COUNTERWEIGHT_SANDBOX_IMAGE` can name another local image. The server never pulls an image by
itself. A change of mode applies from each task's next start or resume, and every task records the
mode, the capabilities and the image digest it ran with; the report states them. A task that has
finished is admitted again under the selected mode by its next chat turn, retest or fix. While a
running task still uses the mode it started with, **System settings** names it.

## Limits of container mode

- **The network is not restricted.** The container can open outbound connections, including to
  services on the host or the local network that the Docker bridge can reach. Container mode
  protects the server's files and credentials; it is not a boundary against data leaving.
- **Traffic capture does not record commands run in the container yet.** Requests the server makes
  itself (structured HTTP tools) are captured as before.
- **Some tool families are withheld while the mode is on**, because they would still start a
  process on the server: background tasks, interactive shells, custom command and script tools,
  MCP servers, and the platform-management and cross-task tools. Web fetch is withheld as well: it
  would send its requests from the server process, on the host's network, and so reach services the
  container cannot, such as those bound to the host's loopback; commands in the container have
  `curl`. Structured HTTP tools, file tools, findings and graph tools, and web search stay
  available. **System settings** lists what is withheld. One exception: a chat the operator opens,
  not started from a task, keeps the platform-management and cross-task tools, which act on the
  server's records and start no process; its commands still run in that conversation's container.
- **A server running under Docker Compose cannot use the mode yet.** It needs a small broker that
  holds the Docker access; mounting the Docker socket into the application would hand it the host.
- **Verified so far on Docker Desktop for Windows** with a real container built from the
  application image. The Linux path (which grants the container user access to the task folder
  through ACLs and needs `setfacl`) and macOS have not been verified end to end, and the assessment
  tools in the image have not each been exercised as the container's unprivileged user.
- It is a container, not a virtual machine: it does not defend against a kernel or Docker escape,
  and credentials that the operator placed in the task's own files remain visible to that task.

## Required boundary

Three properties must be enforced outside the model:

1. **Filesystem and process confinement.** Work that needs a writable filesystem or process
   execution is constrained to the task's own workspace.
2. **Network destination enforcement.** The destination a tool actually connects to is checked
   independently, on every path: shell commands, HTTP tools, custom tools and MCP processes.
3. **Coexistence with operation review.** Literal parameter insertion and the review of the
   effective operation keep working inside the boundary. Parsing a command cannot in general prove
   that an arbitrary interpreted program is harmless, so review complements confinement and does
   not replace it.

Restricting only the Bash tool by name is insufficient. Foreground commands, background jobs,
interactive sessions, custom command and script execution, child processes and process-based MCP
or skill adapters must inherit the same task boundary. Enforcement must fail closed when it is
unavailable. Privileged server, database and provider credentials must stay outside the worker's
filesystem and environment.

## What has been checked

- **Kernel support is necessary but not sufficient.** A capability probe on a recent Linux kernel
  reported Landlock support. That describes what the kernel offers; it applies no restriction and
  does not prove that a prohibited effect is blocked.
- **Self-service namespaces cannot be assumed.** Inside a default container policy, creating new
  user, mount and network namespaces from an unprivileged process was refused. A design must not
  rely on a worker isolating itself, nor silently request broader privileges as a fallback.
- **The agent SDK offers no sandbox hook.** The SDK (norma v0.4.3) exposes the tool list, a working
  directory and a child-process environment. Its foreground shell, background task manager and
  interactive shell sessions start processes directly. A working directory and injected proxy
  variables neither remove server credentials nor prevent a connection to another destination.
- **The Docker image is packaging.** The image places the application, research tools and skill
  files in one runtime, and Compose runs it with provider and database credentials in its
  environment and a shared data directory. It is not a per-task worker boundary.

## Integration points

Implemented prerequisite: agent session setup and the native custom command and Python executors
mask a fixed set of server credential variables. Tests on Windows (foreground, background, custom
tools) and Linux (foreground, background, PTY) observe controlled fake credentials becoming empty
while ordinary task variables and the parent's provider environment remain intact. This does not
remove unknown secrets, and it does not stop filesystem reads, sockets or unbound MCP processes.
It is not evidence that the boundary below is implemented.

| Path | Required integration | Container mode today |
| --- | --- | --- |
| Agent worker, planner, main and chat session assembly | Attach a code-owned task execution capability to every executable role; default SDK registrations must not restore a local executor. | Done for task agents and conversations. |
| Foreground and background Bash | Delegate execution to the confined worker; preserve timeout, output spill, cancellation, task output and process-tree cleanup. | Foreground routed. Background withheld. |
| Interactive shell | Keep session identity and the stdin/stdout lifecycle inside the same worker boundary; preserve the existing open, send and close workflow. | Withheld. |
| Custom command and script tools | Keep frozen schemas, literal interpolation and effective-operation review; execute the reviewed program and data through the same worker boundary. | Withheld (custom HTTP tools stay). |
| Read, Write, Edit and process-based skills or MCP | Resolve paths and run processes inside the worker's permitted filesystem rather than the credential-bearing server. | File tools confined; MCP withheld. |
| Native HTTP and WebFetch | Retain the independently checked URL, redirect, peer, Host and SNI behavior; worker networking must not be able to bypass it with a direct socket. | Checks retained for native HTTP; WebFetch withheld; container network not restricted. |
| Remote MCP and extensions | Treat remote execution authority separately; a request wrapper cannot attest a remote server's actual egress or filesystem effects. | Withheld. |
| Task lifecycle | Capabilities, sessions and processes stop on cancellation, deletion and policy revocation; retries and resume must not inherit stale authority. | Container removed on pause, delete, archive and shutdown. |

## Acceptance checks

1. An allowed job can read and write its task workspace and use supported programs, but cannot
   read a controlled credential fixture or alter a marker outside that workspace, even through
   children, symlinks, background jobs or interactive sessions.
2. A prohibited TCP or UDP destination cannot receive a marker through Bash, Python, custom
   scripts, MCP or child processes when lexical and model checks miss the operation. The check
   validates actual connection and packet delivery, not just a denied dispatch stub.
3. URL-only grants cannot silently become arbitrary raw-socket or IP authority. Redirects, DNS
   changes, Host and SNI changes, proxies and alternate transports obey the same original operator
   policy. Mutable discovery scope does not expand that authority.
4. Allowed target workflows remain usable: HTTP and TLS capture, structured target tools, research
   programs, file uploads and artifacts, interactive stdin, output persistence, and task resume and
   cancel. Any necessary capability distinction is explicit and reviewable.
5. A missing or failed backend never falls back to the local SDK executor. Environment
   sanitization is a server-controlled allowlist; model parameters cannot restore provider or
   database credentials or gain process or container-management authority.
6. Native Windows support is proven independently if it is offered. A successful Linux or
   container fixture does not prove Windows process, filesystem or network confinement.

## Remaining work

The worker backend is Docker: one Linux container per task, on every host platform. In order:

1. A broker for servers running under Docker Compose, so the application never holds the Docker
   socket.
2. Background tasks and interactive shells through the container.
3. Custom command and script tools through the container.
4. MCP processes through the container, and an explicit decision on remote MCP.
5. Network destination enforcement for container processes, with traffic capture.
6. Image provisioning with an explicit trust and version policy.

None of these may change host security policy or start privileged services implicitly.
Contributions and design discussion are welcome through the issue tracker.
