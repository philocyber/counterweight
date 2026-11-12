# Documentation

## Getting started

- [README: quickstart, first assessment, overview](../README.md) ([Español](../README.es.md))
- [Configuration reference](configuration.md): flags, `config.json`, environment variables, TLS, recording proxy, storage limits, remote MCP
- [Operations](operations.md): running, first-owner setup, password reset, updates, backups, troubleshooting
- [Feature guide](features.md): task workspace, white-box code review, findings and reports, retests, asset sync, extensions
- [Choosing and connecting a model](choose-a-model.md): hosted, gateway, local (Ollama, LM Studio, llama.cpp) and self-hosted inference, model requirements, cost controls, acceptance check
- [LLM provider templates](llm-providers.md), including the [self-hosted vLLM on RunPod](llm-providers.md#self-hosted-vllm-on-runpod)
- [RunPod setup for Counterweight](runpod.md), also in **Documentation → RunPod setup** with copyable code blocks
- [Example engagement (template)](example-engagement.md)

## Security

- [Security model](security-model.md): authentication, scope, approval gate, tool rules, destination checks, known limits
- [Approval gate and interception records](approval-gate.md)
- [Task permission holds](permission-holds.md)
- [Restricted execution boundary (status)](EXECUTION-ISOLATION.md)
- [Verification and known limitations](VERIFICATION.md)
- [Vulnerability reporting](../SECURITY.md)

## Concepts and integrations

- [Architecture](architecture.md): layers, two-graph model, engine loop
- [Traffic evidence](traffic-evidence.md)
- [Jira Cloud integration](jira.md)
- [Pull request reviews and the GitHub Action](pr-reviews.md)
- [Authorization checks from a policy file, for CI](authorization-checks.md)
- [Claude Code, Codex and Pi adapter](../adapters/agent/README.md)
- [Side questions](../sidequestion/README.md)
- [Side-question context budgets](../sidequestion/CONTEXT_BUDGET.md)

## Skills

- [AI red teaming skills](AI-RED-TEAM-SKILLS.md)
- [Recon planning guidance](../skills/recon-planning/SKILL.md)
- [ScopeSentry MCP guidance](../skills/scopesentry/SKILL.md)
- [API reconnaissance guidance](../skills/api-recon/SKILL.md) and [reference](../skills/api-recon/reference.md)
- [Playwright CLI guidance](../skills/playwright-cli/SKILL.md)
- [Dreadnode capabilities import (2026-10-22)](CAPABILITIES-IMPORT-2026-10-22.md)

## Project

- [Why Counterweight](why-counterweight.md)
- [Contributing](../CONTRIBUTING.md)
- [Provenance and modifications](PROVENANCE.md)
- [Upstream change history](../CHANGELOG.md)

English skill files are the executable skill entries. Code identifiers, commands and compatibility patterns keep their original spelling.
