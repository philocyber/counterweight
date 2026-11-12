# Verification and known limitations

What is checked, how to reproduce it, and what is not yet verified. Claims here are meant to be
checkable against the repository.

## Continuous integration

Every push to `main` and every pull request runs [`.github/workflows/ci.yml`](../.github/workflows/ci.yml):

| Job | What it runs |
| --- | --- |
| Agent adapter | `npm ci`, `npm test`, `npm run check` in `adapters/agent`, then the adapter end-to-end test against a real backend (`TestAgentAdapterE2E`) |
| Frontend | `npm ci`, `npm test` and the static production export (`npm run build:static`) in `web` |
| Backend | Shell syntax checks for every script, then every Go package against its own disposable PostgreSQL database |

Go tests never touch a configured development or production database: under `go test`, the
database layer only opens databases whose name ends in `_test` (see `db/testguard.go` and
[CONTRIBUTING.md](../CONTRIBUTING.md)).

Current results: [GitHub Actions](https://github.com/Philocyber/counterweight/actions/workflows/ci.yml).

## Reproduce locally

```bash
(cd web && npm ci && npm test && npx tsc --noEmit -p .)   # console
go run ./cmd/counterweight build                          # embedded console + binary

# Database tests: point both variables at a disposable database ending in _test
export COUNTERWEIGHT_TEST_PG_DSN='postgres://user:pass@127.0.0.1:5432/counterweight_test?sslmode=disable'
export COUNTERWEIGHT_PG_DSN="$COUNTERWEIGHT_TEST_PG_DSN"
go test ./db ./config ./locale ./report                   # example: selected packages
```

## Known limitations

- **Execution isolation is partial.** The default mode confines agent file access to each task and
  is covered by unit tests on Windows and by a self-test at every startup; Linux and macOS
  behaviour of the confined file tools is exercised by that self-test, not by a separate test run.
  In that mode, commands for assessments that use a shell still run with the privileges of the
  Counterweight process. The opt-in container mode runs those commands in a per-task container:
  its six containment properties were checked against a real container on Docker Desktop for
  Windows (`go test ./execution -run TestContainerIntegration`, skipped when Docker is absent).
  The check was run with the application image as the task image, and the mode was selected on a
  native server (Windows), which ran the same check before accepting it. One short task was then
  run there with a real model: its commands reported the container's unprivileged user, a Linux
  system and an environment without the server's variables, and the container was gone when the
  task finished and when it was deleted. It has not been verified on
  Linux or macOS, the assessment tools in the image have not each been exercised as the
  container's unprivileged user, it does not restrict the network, and it is not available to a
  server running under Docker Compose. Run Counterweight on a dedicated host, VM or container. Status and design:
  [EXECUTION-ISOLATION.md](EXECUTION-ISOLATION.md).
- **Pull request reviews.** The change computation, the API tokens and the automation API are
  covered by tests with local repositories and stub servers. The action's script passes its own
  test (`sh actions/review/test.sh`, against a local fake server) and `shellcheck`. The action has
  not been run in a real GitHub workflow against a real instance yet.
- **Authorization checks.** The policy validation, the runner and the report are covered by tests
  against a local two-tenant fixture (`go test ./authz`, no database needed): a correct target
  passes, and an exposed admin route, a cross-tenant read and a cross-tenant write each fail; an
  expired, wrong, missing or misassigned credential stops the run; a broken endpoint, a server
  error and a session lost during the run are never reported as a pass; no credential appears in
  either output. The built binary was also run once, on Windows, against a separately written
  local target with the same flaws, with the same outcomes. It has not been run against a real
  application, over HTTPS, or inside a real CI workflow. The step in which the agents draft a
  policy from their testing ledger is guidance in a skill and has not been exercised with a model.
  Inside a task: storing accounts, saving and checking a policy, running it and reading the
  result are covered by tests of the API against a test database and a local target
  (`go test ./server -run TestTaskAuthz`, `go test ./db -run TestAuthz`): secrets are sealed at
  rest and absent from every response and stored report, a policy cannot read the server's
  environment, a blocked target receives nothing, an account is reachable only through its own
  task, and the rows go with the task. The report section is covered for Markdown and PDF. Accounts
  sent with a new task are covered the same way (`TestTaskAuthzAccountsWithNewTask`): a refused
  account creates no task, and the stored ones are sealed. The matrix editor writes the policy
  text in the browser, so a shared fixture closes the loop across the two languages: the server's
  reading of a policy (`authz/testdata/editor-document.json`), the text the editor writes from it
  (`editor-emitted.yaml`, kept current by `npm test` in `web`), and a server test that reads that
  text back and requires the same policy (`go test ./authz -run TestDocument`). The console was
  walked in a browser against a throwaway instance and a local demo application with three roles,
  in English and in Spanish: entering three accounts in the New task form, then building a policy
  in the matrix with clicks only (address, roles, sign-in, three requests), saving it, reading the
  text it wrote, running it and reading the result, which showed the planted flaw. That is one
  application with one kind of sign-in (a JSON request answered with a token); the form and cookie
  variants of the sign-in form are covered by unit tests of the text they produce, not by a run
  against a target. The tool that sends an
  agent's request as a stored role is covered by tests of its handler and of the request broker
  (`go test ./server -run TestRequestAsRole`, `go test ./authz -run TestBroker`): it is not offered
  without an account switched on, a role whose account is off cannot be used, the approval gate
  sees the request without credentials and a refusal sends nothing, a request approved after the
  account was switched off is not sent, the agent cannot set credential headers or leave the
  target, and answers that repeat a credential plain, Base64-encoded or as a new token are cleaned.
  It was then used in one task with one live model against a local demo application with three
  roles and three planted access-control flaws: the agents called it 194 times, the task finished
  without a manual approval, all three flaws were reported (two of them were not in the policy of
  the deterministic check), and the task's activity record holds no password, session token or
  bearer value. That is one run with one model; it says nothing about other models or about a real
  application.
  Guide: [authorization-checks.md](authorization-checks.md).
- **Agent workflows were validated against one live model, once.** A light review of a small
  training repository produced findings with code evidence, and the scanner lead was handed over
  only after the agents' own review and resolved with a written reason. That is one run with one
  model: pull request reviews and the per-task budget pause have only been exercised with stub
  providers. Run the acceptance check in
  [choose-a-model.md](choose-a-model.md#acceptance-check) on your own model before relying on them.
- **Scanners.** The OSV-Scanner and Gitleaks invocations and parsers were checked against the
  pinned releases shipped in the Docker image, on one public repository. Git history is not
  scanned.
- **Known failing tests.** On Windows only: `recontools` `TestExtractBinaryFindsEntryAndSkipsOthers` (no
  executable bit) and the `TestSubscription...` tests that start the stand-in Codex CLI (a script
  Windows cannot run). In `server`, `TestNotifyDigestSegmentsAndDefersRemainder` has failed once in
  a whole-package run on a reused test database and passes alone. Tests of the native container
  backend skip when the suite itself runs in a container, where container mode is refused.
- **AI output can be wrong.** Findings, fixes and retest verdicts come from language models. Review
  evidence before acting on it, and review every generated fix before merging.
- **No live-provider tests in CI.** Provider integrations are tested against local HTTP/SSE
  fixtures. Account access, model availability and paid inference are not exercised in CI.
- **Adapter tests use local fixtures.** They prove the request/response contract of the Claude Code,
  Codex and Pi adapter, not live coding-agent-to-provider execution.
- **Runtimes not verified end to end:** running the published Docker image, and the Windows
  `start.bat` launcher, beyond the packaging checks.
- **Dependency advisories.** `npm audit` in `web/` reports 6 low or moderate advisories, all in the
  `shadcn` component-scaffolding CLI. They are not part of the shipped console bundle.
- **Private repository review is covered by unit tests only.** The clone retries with the stored
  GitHub token when the anonymous attempt fails; this path has not been exercised against a real
  private repository yet. Only GitHub is supported.
