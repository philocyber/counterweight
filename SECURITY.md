# Security policy

Counterweight is a security tool, so we take vulnerabilities in it seriously. Thank you for helping
keep its users safe.

## Reporting a vulnerability

**Please do not report security vulnerabilities through public GitHub issues, discussions or pull
requests.**

Report them privately through GitHub:

1. Go to the [Security tab](https://github.com/Philocyber/counterweight/security) of the repository.
2. Click **Report a vulnerability** to open a private security advisory.

Include as much of the following as you can:

- the affected version or commit, and how Counterweight was installed (Docker, release binary,
  source);
- the component involved (for example authentication, approval gate, recording proxy, custom tools,
  self-update, code review);
- step-by-step reproduction, with a minimal proof of concept against a local test installation;
- the impact you expect, and any suggested fix.

We will acknowledge your report as soon as we can, keep you informed while we investigate, and credit
you in the advisory unless you prefer otherwise. Please give us a reasonable time to release a fix
before any public disclosure.

## Scope

In scope: the Counterweight backend, web console, install/start/update scripts, Docker setup,
release artifacts and the agent adapter in this repository.

Out of scope:

- known limits documented in [docs/security-model.md](docs/security-model.md) and
  [docs/EXECUTION-ISOLATION.md](docs/EXECUTION-ISOLATION.md), most notably that agent commands are
  not sandboxed and run with the privileges of the Counterweight process (reports that bypass a
  documented control are still welcome);
- vulnerabilities in third-party model providers, MCP servers or tools you connect;
- findings that require an already compromised host or administrator account;
- deployments that expose the console to the internet without TLS or against the documented
  guidance.

Only test against installations you own. Never test against other people's Counterweight
deployments.

## Supported versions

Security fixes are made on the `main` branch and shipped in the next release. Please update to the
latest release before reporting; see [docs/operations.md](docs/operations.md#updating).

## Related statements

- Counterweight is not affected by the backdoored third-party ARTEX Docker images reported in
  November 2026: see [the advisory](docs/advisories/2026-11-artex-docker-image.txt).
