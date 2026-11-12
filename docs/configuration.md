# Configuration reference

This page collects every runtime setting Counterweight reads: command-line flags, the
`config.json` file, environment variables, TLS, the recording proxy and storage limits. Most
installations only need the [database](#database) and an [LLM provider](#llm-provider); everything
else has a working default.

- [Where settings live](#where-settings-live)
- [Command-line flags](#command-line-flags)
- [Database](#database)
- [LLM provider](#llm-provider)
- [Management listener and TLS](#management-listener-and-tls)
- [Recording proxy](#recording-proxy)
- [Traffic storage limits](#traffic-storage-limits)
- [Task archives](#task-archives)
- [Remote MCP servers](#remote-mcp-servers)
- [Other environment variables](#other-environment-variables)
- [Settings in the web console](#settings-in-the-web-console)

## Where settings live

| Source | Used for |
| --- | --- |
| Command-line flags | Listen addresses, TLS files, data directory, upstream TLS trust |
| `config.json` | Database connection and skill directory **only** |
| Environment variables (or Docker `.env`) | Database DSN override, LLM bootstrap, storage limits, proxy client CIDRs |
| Web console (**System settings**, **LLM**, **Agents**, ...) | Everything else: model profiles, concurrency, interception rules, notifications, GitHub token, global proxy |

The start scripts (`start.sh`, `start.bat`) pass every argument straight through to the binary,
so `./start.sh -addr 127.0.0.1:9000` works the same as `./counterweight -addr 127.0.0.1:9000`.

## Command-line flags

Defaults below are the binary's own defaults. The Docker image overrides some of them (see
[Docker defaults](#docker-defaults)).

| Flag | Default | Purpose |
| --- | --- | --- |
| `-addr` | `127.0.0.1:8787` | Management listener: web console and API. Plain HTTP is only allowed on loopback. |
| `-tls-cert` | (empty) | Management HTTPS certificate chain (PEM). Required together with `-tls-key`. |
| `-tls-key` | (empty) | Private key (PEM) matching `-tls-cert`. |
| `-proxy` | `127.0.0.1:8788` | Traffic-recording proxy. An empty value disables it. |
| `-data` | `data/` next to the executable | Data directory: SQLite stores (traffic evidence), task files, clones for code review. |
| `-target-tls-ca` | (empty) | Extra PEM CA trusted for upstream HTTPS through the recording proxy (private PKI). |
| `-target-tls-insecure-origins` | (empty) | Comma-separated exact HTTPS origins whose certificates are explicitly not verified. |
| `-setup-token` | | Print the one-time first-owner setup token and exit. |
| `-reset-password` | | Reset the administrator password from `COUNTERWEIGHT_RESET_NEWPASS` and exit. |

Subcommands (`counterweight help` lists them):

| Command | What it does |
| --- | --- |
| `counterweight serve` | Run the backend. This is the default, so `counterweight` with no subcommand, or with only flags, does the same. |
| `counterweight supervise` | Restart loop used by `start.sh` / `start.bat`; required for in-app updates. |
| `counterweight setup-token` | Same as `-setup-token`. |
| `counterweight reset-password` | Same as `-reset-password`. |
| `counterweight authz validate\|run` | Authorization check from a policy file. Needs no server and no database. See [authorization-checks.md](authorization-checks.md). |
| `counterweight dev` | Development stack (backend, proxy, Next.js dev server). See [CONTRIBUTING.md](../CONTRIBUTING.md). |
| `counterweight build` | Build the single binary with the frontend embedded (`-target OS/ARCH`, `-o path`, `-skip-npm-ci`, `-skip-frontend`, `-version`). |

From a source checkout, prefix commands with `go run ./cmd/counterweight`.

### Docker defaults

The Docker image starts `start.sh` with
`-addr :8787 -tls-cert /app/tls/server.pem -tls-key /app/tls/server-key.pem -proxy :8788`.
`docker-compose.yml` publishes both ports on `127.0.0.1` only, mounts `./data`, `./skills` and
`./tls` (read-only), and passes the variables listed in `.env.example` to the container.

## Database

Counterweight needs PostgreSQL (the bundled Compose file uses `postgres:16-alpine`). The schema is
embedded in the binary and applied idempotently on every start, so there is no separate migration
step.

Configure it in one of two ways:

1. **Environment variable** `COUNTERWEIGHT_PG_DSN` (takes precedence), for example
   `postgres://counterweight:secret@127.0.0.1:5432/counterweight?sslmode=disable`.
2. **`config.json`**, copied from `config.example.json`:

```json
{
  "database": {
    "host": "127.0.0.1",
    "port": 5432,
    "user": "counterweight",
    "password": "your-password",
    "dbname": "counterweight",
    "sslmode": "disable"
  }
}
```

Instead of individual fields you can set `"database": { "dsn": "postgres://..." }`; a non-empty
`dsn` overrides the fields. Use `sslmode=require` (or stricter) for a database on another host.

**Which `config.json` is read.** `COUNTERWEIGHT_CONFIG` selects an explicit path. Otherwise the first
existing file wins: `./config.json` in the current working directory, then `config.json` next to
the executable. The startup log prints the absolute path it used.

**Skill directory.** Precedence: `COUNTERWEIGHT_SKILL_DIR`, then `skill_dir` in `config.json`, then
`skills/` next to the executable. Each child directory holds a `SKILL.md`.

**Permissions.** Config reads tighten the file to owner-only access. On Unix, create it private from
the start (`install -m 600 config.example.json config.json`). On Windows, use the bundled helper
before writing credentials:

```powershell
powershell.exe -NoProfile -File .\private-credential.ps1 -Path .\config.json -Template .\config.example.json
```

The same helper protects a Docker `.env`:
`powershell.exe -NoProfile -File .\private-credential.ps1 -Path .\.env -Template .\.env.example`.

## LLM provider

The recommended way is the web console: **LLM → New**, pick a provider template, paste your key,
**Test connection**, save, then **Set active**. Templates and provider-specific notes are in
[llm-providers.md](llm-providers.md).

For headless bootstrap, the backend can also build a default profile from environment variables:

| Variable | Meaning |
| --- | --- |
| `ANTHROPIC_API_KEY` | Anthropic key. Selects the Anthropic format when no provider is set. |
| `OPENAI_API_KEY` | OpenAI (or OpenAI-compatible) key. Used when no Anthropic key is set. |
| `COUNTERWEIGHT_LLM_PROVIDER` | `anthropic`, `openai` (Chat Completions) or `openai-responses`. Inferred from the keys when empty. |
| `COUNTERWEIGHT_LLM_MODEL` | Model ID. Each format has a built-in default; set it explicitly. |
| `COUNTERWEIGHT_LLM_BASE_URL` | Custom endpoint, for gateways or self-hosted OpenAI-compatible servers. |
| `COUNTERWEIGHT_LLM_PROXY` | Proxy for model traffic (`http`, `https` or `socks5`). |
| `COUNTERWEIGHT_LLM_STREAM` | Streaming is on by default; `false`, `0` or `off` disables it. |
| `COUNTERWEIGHT_CODEX_BINARY` | Path to the Codex CLI for the ChatGPT subscription connector (otherwise `codex` on `PATH`). |

These variables are masked from every agent-executed command (see
[security-model.md](security-model.md#credential-masking-for-executed-commands)).

## Management listener and TLS

- Plain HTTP is accepted only on a loopback address. A hostname such as `localhost` is resolved
  once and pinned to the checked loopback address.
- Any non-loopback listener needs both `-tls-cert` and `-tls-key`; otherwise the binary refuses to
  start before opening any store. The HTTPS listener requires TLS 1.2 or newer and does not also
  serve plain HTTP.
- Remote access example:
  `./start.sh -addr 0.0.0.0:8787 -tls-cert /path/server.pem -tls-key /path/server-key.pem`.
  Use a certificate valid for the hostname clients type and trusted by their browsers.
- An HTTPS reverse proxy on the same host may forward to a loopback HTTP backend. Terminate HTTPS at
  the public ingress; the backend cannot verify that external hop. An ingress that forwards to an
  HTTPS backend must verify the backend certificate.
- The loopback HTTP listener refuses a browser request made from a plain-HTTP page under a name
  that is not a loopback name, which is how a page that rebinds its DNS name reaches a local
  console. A proxy that publishes the console over plain HTTP under its own name therefore needs
  that name set as the public base URL in the notification settings. API responses carry
  cross-origin headers only for the console's own origin, and sign-in, first-owner setup and
  password changes refuse cross-site browser requests.
- The web client refuses to send credentials to a non-loopback HTTP origin. If events are served
  through an ingress rather than directly on port 8787, set `NEXT_PUBLIC_SSE_BASE` to the HTTPS
  ingress origin at build time; it must use HTTPS for a remote destination.

### Docker certificates

Docker Compose always serves the console over HTTPS, so it needs `tls/server.pem` (certificate
chain) and `tls/server-key.pem` (matching key) before the first start. Missing or invalid files stop
startup. `tls/` is excluded from Git and from image builds; keep the key private.

For a trusted local certificate, [mkcert](https://github.com/FiloSottile/mkcert) is the easiest
option:

```bash
mkdir -p tls
mkcert -install
mkcert -cert-file tls/server.pem -key-file tls/server-key.pem localhost 127.0.0.1 ::1
```

A self-signed certificate also works; browsers show a warning you accept once:

```bash
mkdir -p tls
openssl req -x509 -newkey rsa:2048 -nodes -days 365 \
  -keyout tls/server-key.pem -out tls/server.pem \
  -subj "/CN=localhost" -addext "subjectAltName=DNS:localhost,IP:127.0.0.1"
```

On Windows Git Bash, prefix the `openssl` command with `MSYS_NO_PATHCONV=1` so `-subj` is not
rewritten as a path. For access from other machines, use a certificate for the real hostname,
issued by a CA your clients trust.

HTTP and HTTPS origins have separate browser storage, so switching between them requires signing
in again.

## Recording proxy

The recording proxy (`-proxy`, default `127.0.0.1:8788`) captures the HTTP(S) traffic agents send
so findings can be bound to evidence. See [traffic-evidence.md](traffic-evidence.md).

**Client access.** By default the proxy only trusts loopback TCP clients, including when it listens
on all interfaces inside Docker. To admit other clients, set `COUNTERWEIGHT_PROXY_CLIENT_CIDRS` to a
comma-separated list of trusted IPv4/IPv6 CIDRs before startup (Docker `.env` is supported).

- Docker host requests reach the container from a bridge address, so publishing on
  `127.0.0.1:8788` alone does not grant access. Authorize the source CIDR the container actually
  sees.
- Keep grants narrow: a shared NAT, forwarder or subnet grant trusts every client behind it.
- Rejected connections close before HTTP parsing or target dialing. Invalid CIDRs prevent the proxy
  from opening.
- This is network-client authorization, not per-user authentication or destination enforcement;
  local processes share loopback trust.

**Upstream certificate checks.** Recorded HTTPS, WSS and alternate HTTP connections verify the
target's certificate before sending application data.

- For private PKI, add `-target-tls-ca /path/ca.pem` to platform trust.
- For explicitly authorized self-signed targets, list exact origins:
  `-target-tls-insecure-origins https://target.example:8443,https://127.0.0.1:9443`. Entries are
  exact HTTPS authorities including the port (also used for WSS); credentials, paths and queries are
  rejected.
- Invalid configuration fails before stores open. These flags affect recording-proxy traffic only;
  direct enrichment requires platform trust. HTTPS egress proxies keep independent verification.

**Capture toggle.** Disabling capture (System settings) stops recording at the recorder itself,
including for clients that address the proxy directly. Requests still forward under the same
client-access and upstream TLS policies, and saved traffic stays readable, exportable and
deletable. Turning capture off controls persistence; it does not disable MITM interception.

## Traffic storage limits

All limits are byte counts (or a count of exchanges) read at startup; set them in the environment or
in Docker `.env`. Lowering a limit never deletes existing evidence, and saved evidence is never
evicted automatically: delete unwanted traffic to free space.

| Variable | Default | Limits |
| --- | --- | --- |
| `COUNTERWEIGHT_TRAFFIC_BLOB_BYTES` | 1 GiB (`1073741824`) | Stored request/response bodies. Duplicate bodies reuse storage. |
| `COUNTERWEIGHT_TRAFFIC_INDEX_BYTES` | 1 GiB (`1073741824`, min `262144`) | SQLite index allocation; also caps imported `traffic.json` manifests. |
| `COUNTERWEIGHT_TRAFFIC_WAL_BYTES` | 128 MiB (`134217728`, min `65536`) | SQLite write-ahead log allowance. |
| `COUNTERWEIGHT_TRAFFIC_FREE_BYTES` | 256 MiB (`268435456`) | Free space kept available on the data volume before admitting writes. |
| `COUNTERWEIGHT_TRAFFIC_EXCHANGE_BYTES` | 64 MiB (`67108864`) | Logical size of one captured exchange (bodies, headers, metadata, row overhead). |
| `COUNTERWEIGHT_TRAFFIC_HOST_BYTES` | 256 MiB (`268435456`) | Logical capture per hostname (across ports, case and trailing dots). |
| `COUNTERWEIGHT_TRAFFIC_MAX_EXCHANGES` | `100000` | Retained exchanges across all hosts. |

What happens at a limit:

- **New capture is skipped, not the request.** Target responses pass through unchanged; the drop
  is logged and counted (for example `blob_storage.dropped_captures` in the authenticated traffic
  API, which also reports `wal_storage.admission_bytes` and `wal_storage.used_bytes`).
- **Imports fail cleanly.** An over-budget archive import rolls back, reports zero committed rows and
  can be retried after raising the limit and restarting.
- **Accounting survives restarts.** Counts and per-host totals are computed from retained rows, so a
  restart cannot reset them; deletion releases capacity.
- **Explicit archive restoration** keeps its own storage budgets and may restore evidence above the
  capture limits; new capture for that host (or overall) then stays paused until usage falls below
  the limit.

Storage engine details:

- Fresh schema bootstrap uses rollback journaling under the index limit before enabling WAL. Every
  pooled traffic connection enforces the WAL allowance natively and rejects growth beyond it with
  `SQLITE_FULL`; a rejected transaction rolls back and committed evidence stays intact.
- Clear-all can recover from a WAL quota failure using a bounded rollback journal. If WAL
  restoration fails, capture and import pause while reads and exports remain available.
- Rollback journals, temporary databases and sorter spill files share a per-store scratch ceiling of
  twice the larger of the configured index allowance and its size at startup, plus 1 MiB. Oversized
  queries may fail with `SQLITE_FULL`.
- Free-space checks run before each native SQLite extension and between blob copy chunks, with an
  extra 64 KiB margin. Windows and common Unix platforms have native accounting; unsupported
  platforms refuse new writes. These checks do not reserve OS space exclusively or constrain other
  processes.

## Task archives

`COUNTERWEIGHT_TASK_ARCHIVE_BYTES` (default 4 GiB, `4294967296`, at most 128 TiB) bounds task
archive packaging and extraction: raw payload totals, snapshot manifests, compressed output, package
input and extracted files. A quota failure keeps the live task and package intact and can be retried
with a higher limit. Retained archive totals and staging metadata are not yet separately limited.

## Remote MCP servers

Remote MCP servers are added in **MCP** and can use `http` (Streamable HTTP) or `sse` (legacy SSE).

- Legacy SSE servers usually open the stream at `GET /sse` and receive JSON-RPC on the
  `/message?sessionId=...` endpoint they announce: set the URL to `/sse` and the header to
  `Authorization=Bearer <token>`.
- Redirects and legacy message endpoints must stay on the configured scheme, hostname and effective
  port. Put credentials in configured headers, not in URL user info.
- Responses have an 8 MiB budget (Streamable HTTP body or cumulative SSE bytes; per line for legacy
  SSE), and endpoint announcements a 64 KiB budget. Oversized responses fail explicitly rather than
  returning truncated data. Legacy setup has a 120-second maximum.

## Other environment variables

| Variable | Purpose |
| --- | --- |
| `COUNTERWEIGHT_CONFIG` | Explicit `config.json` path. |
| `COUNTERWEIGHT_SKILL_DIR` | Explicit skill directory. |
| `COUNTERWEIGHT_LANGUAGE` | Server language for backend messages; `en` is the only supported value. |
| `COUNTERWEIGHT_KEY_DIR` | Directory for the session signing key (`jwt.key`) and the credential encryption key (`secrets.key`). Default: next to the executable. Docker Compose sets it to the `keys` volume. |
| `COUNTERWEIGHT_SECRETS_KEY` | Base64 key (or several, comma-separated, newest last) that encrypts stored credentials, instead of the `secrets.key` file. See [operations](operations.md#credential-encryption-key). |
| `COUNTERWEIGHT_RESET_STORED_CREDENTIALS` | `1` clears stored credentials that can no longer be decrypted after the encryption key was lost; re-enter them in the console and remove the variable. |
| `COUNTERWEIGHT_RESET_NEWPASS` | New administrator password for `-reset-password` (at least 8 characters, at most 72 bytes). |
| `COUNTERWEIGHT_NOTIFY_ALLOW_LOCAL` | `1` allows notification channels (for example an SMTP relay) on local/private addresses. |
| `COUNTERWEIGHT_BTW_MAX_OUTPUT_TOKENS` | Output cap for side questions (`/btw`), between 256 and 32768. |
| `NEXT_PUBLIC_SSE_BASE` | Frontend build-time origin for event streams behind an ingress (HTTPS for remote). |

## Settings in the web console

- **LLM**: model profiles, provider templates, Codex/ChatGPT connection, activation.
- **Agents**: per-agent prompt, model binding, run budget and tools (including the `retester`
  agent used by one-click retests).
- **System settings**: work agents per task (default 3), traffic capture, global proxy (also used
  by the updater), GitHub token for fix pull requests, version and updates.
- **Interception rules**, **Asset blocking**, **Approval records**: the approval gate. See
  [approval-gate.md](approval-gate.md) and [security-model.md](security-model.md).
- **MCP**, **Skills**, **Tools**, **Notifications**: extensions and alert channels.
- **Asset sync**: import assets from [ScopeSentry](https://github.com/Autumn-27/ScopeSentry).
