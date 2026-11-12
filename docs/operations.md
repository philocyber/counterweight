# Operations

Running Counterweight day to day: starting and stopping, first-owner setup, password recovery,
updates, rollback, backups and troubleshooting.

- [Running the service](#running-the-service)
- [First-owner setup](#first-owner-setup)
- [Resetting the admin password](#resetting-the-admin-password)
- [Updating](#updating)
- [Backups](#backups)
- [Troubleshooting](#troubleshooting)

## Running the service

**Docker Compose**

```bash
docker compose up -d                      # start (builds the image the first time)
docker compose logs -f counterweight      # follow logs
docker compose stop                       # stop, keeping containers and data
docker compose down                       # remove containers; the pgdata volume and ./data stay
```

**Native binary**

Start with `start.sh` (Linux/macOS) or `start.bat` (Windows), not `./counterweight` directly. Both
run `counterweight -supervise`, the restart loop built into the binary: exit code 0 stops it, exit
code 75 applies a staged update and relaunches, other exit codes back off and retry. The in-app
updater relies on this loop. `start.sh` also frees busy listen ports first (it needs `lsof` for
that).

```bash
./start.sh                                          # Windows: .\start.bat
nohup ./start.sh > counterweight.log 2>&1 &         # run in the background
./start.sh -addr 127.0.0.1:9000 -proxy 127.0.0.1:9001   # flags pass through
```

The default console URL is `http://127.0.0.1:8787` for a native install and
`https://localhost:8787` for Docker.

## First-owner setup

The first visit lands on `/setup`. Print the one-time token on the machine that runs the backend:

| Install | Command |
| --- | --- |
| Docker | `docker compose exec counterweight /app/counterweight -setup-token` |
| Release binary | `./counterweight -setup-token` (Windows: `.\counterweight.exe -setup-token`) |
| Source checkout | `go run ./cmd/counterweight setup-token` |

Paste the token with your new password (at least 8 characters). Sign in as `COUNTERWEIGHT`.
Existing installations with a password keep using normal login.

## Resetting the admin password

`reset-password.sh` detects the deployment and prompts for the new password. Earlier sessions are
invalidated on their next request; no restart is needed.

**Native install** (uses the current backend binary; no `psql` or `pgcrypto` needed):

```bash
./reset-password.sh -m local --backend ./counterweight     # Windows: --backend ./counterweight.exe
```

It reads the database settings from `COUNTERWEIGHT_PG_DSN` or `config.json` (override with `--dsn`,
`--config` or the `-H/-P/-U/-W/-d` fields) and passes them to the binary through environment
variables. You can also call the binary directly with `COUNTERWEIGHT_RESET_NEWPASS` set:

```bash
COUNTERWEIGHT_RESET_NEWPASS='new-password' ./counterweight -reset-password
```

New passwords must be at least 8 characters and at most 72 bytes. An older binary without `-reset-password` fails without
changing anything.

**Docker** (runs `psql` inside the PostgreSQL container; reads `POSTGRES_*` from `.env`):

```bash
./reset-password.sh -m docker
```

Docker mode creates the `pgcrypto` extension if needed, which requires a role allowed to do so (the
Compose database owner is). Run `./reset-password.sh -h` for every option.

## Updating

Updates replace the program only. The PostgreSQL volume (`pgdata`), the `keys` volume (session
signing key and credential encryption key), `./data` (SQLite traffic store, code-review clones) and
`./skills` are kept. The database schema is re-applied
idempotently on every start, so a restart is the migration. **Back up first** (see
[Backups](#backups)); the database schema does not roll back.

### In-app update (release binaries)

**System settings → Version and updates** checks for and installs new releases:

1. Downloads the archive for your platform from GitHub Releases (HTTPS, GitHub domains only; the
   global proxy on the same page is used if set).
2. Verifies it against the release's `SHA256SUMS`.
3. Smoke-tests the new binary with `-h` and stages it as `counterweight.new`.
4. Exits; `start.sh` / `start.bat` relaunches and completes the swap. The page waits and refreshes.

- **A failed update leaves nothing broken.** If verification or the smoke test fails, the staged file
  is discarded. If a swapped-in version fails to start three times in a row, it rolls back to
  `counterweight.old` automatically and keeps the failed build as `counterweight.failed`.
- **Roll back anytime** with the **Roll back to previous version** button (`counterweight.old`).
- **An update is a restart**, so running tasks are interrupted. Update when idle.
- **Development builds are never updated**: the updater is disabled when the version is `dev` or a
  `git describe` string with a suffix.
- **Under Docker only the binary changes**, not the image's toolchain, and recreating the container
  reverts to the binary baked into the image. Rebuild the image instead (below).

### Release signatures

Release archives are listed with their SHA256 digests in `SHA256SUMS`. The maintainer also signs that
file with an Ed25519 key and publishes the signature as `SHA256SUMS.sig`. The public keys are compiled
into the binary (`selfupdate/release-keys.pub`).

**What is verified.** The in-app updater downloads `SHA256SUMS` and `SHA256SUMS.sig` over the same
HTTPS/GitHub-only channel as the archive and checks the signature over the exact bytes of
`SHA256SUMS` before anything else. It then requires the archive for your platform to be listed in that
signed file and to have the listed digest. Only after that is anything extracted or run.

**Two states, shown in the update progress and the server log:**

- *This build embeds release keys.* The progress shows "Release signature verified (key ...)". A missing
  `SHA256SUMS.sig`, a malformed one, or one that does not verify against an embedded key stops the
  update with an error, and nothing is extracted or executed. No setting or environment variable turns
  this off. Install that release manually after checking it (below), or build from source.
- *This build embeds no keys* (a build whose `selfupdate/release-keys.pub` lists none; this
  repository lists the project's key, id `60061ba203b7d679`). Updates work as before, and the progress and log say "This build does not verify release
  signatures; only the SHA256SUMS checksum was checked". The checksum protects against corruption, but
  not against someone who can replace both release files.

**Verify a manual download.** Put the archive, `SHA256SUMS` and `SHA256SUMS.sig` in one directory:

```bash
./counterweight release-verify SHA256SUMS       # signature, against the keys in your binary
sha256sum -c --ignore-missing SHA256SUMS         # archive digest (macOS: shasum -a 256 -c)
```

`release-verify` exits non-zero and says why when the signature is missing or wrong, or when the
binary embeds no keys. Run it with a binary you already trust, not with the one you just downloaded.

**Maintainer procedure.**

1. Once, on a trusted machine: `./counterweight release-keygen`. It prints the private key (stdout),
   the public key line and the key id.
2. Add the public key line to `selfupdate/release-keys.pub` and commit it. Never commit the private key.
3. Store the private key as the `RELEASE_SIGNING_KEY` secret of a `release-signing` environment in
   the repository settings, with required reviewers and a deployment rule for `v*` tags. The release
   workflow signs `SHA256SUMS` in a job of its own that runs in that environment with read-only
   permissions and no third-party action, using `counterweight release-sign` built from the tagged
   source (the key is read from `COUNTERWEIGHT_RELEASE_SIGNING_KEY`, never an argument), and checks
   the result with `release-verify`. A separate job without access to the secret publishes the
   archives and `SHA256SUMS.sig`. Without the secret the workflow still succeeds but prints an
   "Unsigned release" warning and publishes no signature. Every action in the workflows is pinned to
   a commit; Dependabot proposes updates.
4. Rotation: run `release-keygen` again, add the new public line to `selfupdate/release-keys.pub` next to
   the old one and publish a release signed with the old key, so installations learn the new key. Then
   replace the `RELEASE_SIGNING_KEY` secret with the new private key. Remove the old public line in a
   later release once installations have moved on.

### Docker Compose

```bash
git pull                                          # or check out a reviewed tag
docker compose up -d --build counterweight        # rebuild from source and restart
docker image prune -f                             # optional: remove old images
```

Or run `./update.sh` and choose **Rebuild Docker deployment**; it offers `git pull --ff-only`,
then runs `docker compose build counterweight` and `docker compose up -d counterweight`.

Compose builds from the checked-out source by default. The release workflow also publishes images to
`ghcr.io/philocyber/counterweight`; to use one, set the `image:` of the `counterweight` service to a
specific published tag and remove its `build:` section, then use
`docker compose pull counterweight && docker compose up -d counterweight`.

### Release archive

Download the new `counterweight-<version>-<os>-<arch>.zip`, stop the old process, replace the binary
and `skills/` (keep `config.json` and `data/`), then start again:

```bash
cp -r <unpacked>/skills ./ && cp <unpacked>/counterweight ./
./start.sh
```

### Source checkout

```bash
git pull
go run ./cmd/counterweight build      # or ./update.sh and choose "Rebuild local binary"
./start.sh                            # restart to apply
```

## Backups

Back up both stores before upgrades and on a schedule:

| What | Where | How |
| --- | --- | --- |
| PostgreSQL (tasks, assets, findings, settings) | Compose volume `pgdata`, or your server | `docker compose exec -T postgres pg_dump -U counterweight counterweight > counterweight.sql` (or `pg_dump` against your server) |
| Data directory (traffic evidence, clones, task files) | `./data` (Docker bind mount) or `data/` beside the binary | Copy while the service is stopped |
| Configuration | `.env` or `config.json`, `tls/` | Store with your other secrets |
| Credential encryption key and session signing key | Compose volume `keys` (`/app/keys`), or `secrets.key` and `jwt.key` beside the binary, or `COUNTERWEIGHT_SECRETS_KEY` | `docker compose cp counterweight:/app/keys ./keys-backup`; store it separately from the database backup (see below) |
| Custom skills | `./skills` | Copy, or keep in version control |

Adjust the user and database names if you changed `POSTGRES_USER` / `POSTGRES_DB`. Treat backups as
sensitive: they contain findings, captured traffic and credentials.

### Credential encryption key

Stored service credentials (LLM profile API keys and proxy URLs, MCP server environment and header
values, notification channel settings, web-search API keys, the web-search and global proxy URLs)
are encrypted in PostgreSQL with AES-256-GCM. The key is not in the database: it is created on first
start as `secrets.key`, readable only by the service account, in the key directory: beside the
binary for a native install, and the `keys` volume (`/app/keys`, set through
`COUNTERWEIGHT_KEY_DIR`) under Docker Compose, where it survives rebuilding or recreating the
container. The key directory is deliberately not the data directory, which agents and the file
browser can read. For deployments that inject secrets, set `COUNTERWEIGHT_SECRETS_KEY` to a base64
key instead (generate one with `openssl rand -base64 32`); the file is then neither read nor created.

- **Back up the key with the database, but store it separately and securely.** A database dump
  without the key does not reveal the credentials; the key without the dump is useless.
- **If the key is lost**, the server refuses to start and names the missing key file. Restore the
  key and restart. If it cannot be recovered, start once with
  `COUNTERWEIGHT_RESET_STORED_CREDENTIALS=1`: the unreadable credentials are cleared (notification
  channels that lose their settings are disabled), a new key is used, and you re-enter the API keys,
  proxies and channel settings in the console. Tasks, findings, assets and all other data are not
  affected. Remove the variable afterwards.
- **To rotate the key**, stop the service, append a new line with `openssl rand -base64 32` to
  `secrets.key` (or append the new key to `COUNTERWEIGHT_SECRETS_KEY`, separated by a comma), and
  start it. The last key encrypts; every stored credential is re-encrypted with it at startup. Keep
  the older lines as long as you keep database backups made before the rotation, because restoring
  one of them needs the key it was written with.

## Troubleshooting

<details>
<summary><b>The Docker container exits immediately</b></summary>

Check `docker compose logs counterweight`. The most common cause is a missing or invalid
`tls/server.pem` / `tls/server-key.pem`; see
[Docker certificates](configuration.md#docker-certificates). Compose also refuses to start without
`POSTGRES_PASSWORD` in `.env`.
</details>

<details>
<summary><b>The browser shows a certificate warning</b></summary>

Expected with a self-signed certificate. Accept it once, or create a locally trusted certificate
with mkcert as shown in [configuration.md](configuration.md#docker-certificates).
</details>

<details>
<summary><b>"management HTTP requires a loopback address"</b></summary>

You passed a non-loopback `-addr` without TLS. Either bind to `127.0.0.1` and use an SSH tunnel or
reverse proxy, or add `-tls-cert` and `-tls-key`.
</details>

<details>
<summary><b>Port 8787 or 8788 is already in use</b></summary>

`start.sh` closes an existing listener on those ports (it needs `lsof`). Otherwise stop the other
process, or choose new ports: `./start.sh -addr 127.0.0.1:9000 -proxy 127.0.0.1:9001`. For Docker,
change the host side of the `ports:` mappings in `docker-compose.yml`.
</details>

<details>
<summary><b>"Configuration file ... not found; using COUNTERWEIGHT_PG_DSN only"</b></summary>

The binary did not find `config.json` in the working directory or next to the executable. Create one
from `config.example.json`, set `COUNTERWEIGHT_CONFIG`, or set `COUNTERWEIGHT_PG_DSN`.
</details>

<details>
<summary><b>Cannot connect to the database</b></summary>

Check host, port (the standard is `5432`), user, password and
`sslmode`. With Docker Compose the backend reaches PostgreSQL at `postgres:5432` on the internal
network; the database port is not published to the host.
</details>

<details>
<summary><b>Tasks do not start or the model test fails</b></summary>

A task needs an active LLM profile. Open **LLM**, click **Test connection** on your profile and read
the hint appended to the error. Gateway-specific hints are listed in
[llm-providers.md](llm-providers.md#built-in-error-guidance).
</details>

<details>
<summary><b>The update button is missing or disabled</b></summary>

Self-update only works for release builds started through `start.sh` / `start.bat`. Builds whose
version is `dev` or a `git describe` string are never updated.
</details>

<details>
<summary><b>Signed out after switching between http:// and https://</b></summary>

The two origins have separate browser storage. Sign in again.
</details>
