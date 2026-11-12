# Contributing to Counterweight

Thanks for helping. Bug reports, documentation, skills, tests and code are all welcome.

- **Questions and bugs:** open an [issue](https://github.com/Philocyber/counterweight/issues/new/choose).
- **Security vulnerabilities:** never in public issues; follow [SECURITY.md](SECURITY.md).
- **Larger changes:** open an issue first so we can agree on the approach before you invest time.

By contributing you agree that your contribution is licensed under the
[AGPL-3.0](LICENSE), like the rest of the project.

## Development setup

**Requirements:** Go 1.26.3+ (see `go.mod`), Node.js 22 with npm, PostgreSQL 16 (a local Docker
container is fine) and Git.

```bash
git clone https://github.com/Philocyber/counterweight.git
cd counterweight
(cd web && npm ci)
```

Point the backend at a **development** database with `config.json` (copy `config.example.json`) or
`COUNTERWEIGHT_PG_DSN`. See [docs/configuration.md](docs/configuration.md#database).

**Run the full stack with hot reload:**

```bash
go run ./cmd/counterweight dev      # or ./dev.sh
```

This starts the backend on `127.0.0.1:8787`, the recording proxy on `127.0.0.1:8788` and the
Next.js dev server, which proxies `/api/*` to the backend (override with `COUNTERWEIGHT_API`). Open
<http://127.0.0.1:3000>. Ctrl-C stops everything.

Pieces separately:

| What | Command |
| --- | --- |
| Backend only (no embedded console) | `go run ./cmd/counterweight` |
| Frontend only | `cd web && npm run dev` |
| Frontend with mock data, no backend | `cd web && NEXT_PUBLIC_MOCK=1 npm run dev` |
| Single binary with console embedded | `go run ./cmd/counterweight build` |
| Release archives for all platforms | `./build.sh --release` |

The first-owner setup token for a dev backend: `go run ./cmd/counterweight setup-token`.

## Tests

### Go

> [!CAUTION]
> Go tests write to and clear PostgreSQL tables. **Never run them against a database that holds data
> you care about.** Use a disposable database whose name ends in `_test`.

The guard is built in ([`db/testguard.go`](db/testguard.go)):

- Inside a test binary, `db.Open` refuses any database whose name does not end in `_test`.
- Tests first resolve the normal database configuration (`COUNTERWEIGHT_PG_DSN`, else
  `config.json`). If `COUNTERWEIGHT_TEST_PG_DSN` is set, they use it instead; otherwise they append
  `_test` to the configured database name, so a dev database `counterweight` becomes
  `counterweight_test` on the same server (created on first use).

The simplest safe setup is a throwaway PostgreSQL container, with both variables pointing at it so
nothing can fall back to a real database:

```bash
docker run -d --rm --name counterweight-test-pg -p 127.0.0.1:55432:5432 \
  -e POSTGRES_USER=counterweight_test -e POSTGRES_PASSWORD=test_only_password \
  -e POSTGRES_DB=counterweight_test postgres:16-alpine

export COUNTERWEIGHT_PG_DSN='postgres://counterweight_test:test_only_password@127.0.0.1:55432/counterweight_test?sslmode=disable'
export COUNTERWEIGHT_TEST_PG_DSN="$COUNTERWEIGHT_PG_DSN"
go test -p 1 ./server/ -count=1     # one package
go test -p 1 ./... -count=1         # everything (slow)

docker stop counterweight-test-pg   # removes it (--rm)
```

Run packages serially (`-p 1`): they share the test database. CI goes further: for each package it
creates a fresh `counterweight_<n>_test` database, initializes the supplementary LLM usage/records
tables that normal server startup creates, runs the package and drops the database (see
[.github/workflows/ci.yml](.github/workflows/ci.yml)). Mirror that if you see missing-table errors
or cross-package interference.

Known state: the tests that currently fail, and why, are listed under "Known failing tests" in
[docs/VERIFICATION.md](docs/VERIFICATION.md). Do not disable or weaken tests to make a run green.

### Frontend

```bash
cd web
npm test               # node:test unit and i18n tests
npm run check          # Biome lint + format check
npm run check:fix      # apply safe fixes
npm run build:static   # static export, as used by the release build
```

`npm run lint` and `npm run format` run Biome's linter and formatter separately. Configuration is
in [`web/biome.json`](web/biome.json).

### Agent adapter

```bash
cd adapters/agent
npm ci
npm test && npm run check
```

### Shell scripts

```bash
for script in *.sh; do bash -n "$script"; done
sh -n start.sh
```

## Pull requests

1. Branch from `main` and keep each PR focused on one change.
2. Add or update tests for behavior you change, and keep existing tests enabled.
3. Run the relevant checks above (CI runs Go, frontend and adapter jobs on every PR).
4. Update documentation when you change flags, environment variables, setup steps or UI flows.
5. User-facing UI text lives in `web/src/i18n/messages/`; add both the English and Spanish strings.
6. Never commit secrets, real target data, captured traffic or `config.json` / `.env` / `tls/` /
   `data/` contents. A [gitleaks](https://github.com/gitleaks/gitleaks) configuration is included.
7. Fill in the pull request template, including how you tested.

## Skills

Skills live in `skills/<name>/SKILL.md` (`agentskills.io` format). When you adapt material from
another project, keep its license and add a provenance note in the skill and, if it is a new
upstream, in [NOTICE](NOTICE).

## Responsible use

Test only against systems you own or are authorized to test, ideally local fixtures. Contributions
whose main purpose is causing harm, such as denial-of-service techniques, will not be accepted.
