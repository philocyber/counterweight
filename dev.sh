#!/usr/bin/env bash
# Thin shim: the dev stack now lives in the Go CLI so it runs the same on every
# platform. Equivalent to: go run ./cmd/counterweight dev
set -euo pipefail
cd "$(dirname "$0")"
exec go run ./cmd/counterweight dev "$@"
