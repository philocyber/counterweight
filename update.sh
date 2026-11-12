#!/usr/bin/env bash
# Counterweight update: rebuild the local Docker image or native binary.
# Startup applies the idempotent database schema. Back up data before upgrading.
set -euo pipefail
cd "$(cd "$(dirname "$0")" && pwd)"
msg(){ printf '%s' "$1"; }
info(){ printf '%s\n' "$*"; }
die(){ printf '%s\n' "$*" >&2; exit 1; }
ask(){ local p="$1" d="${2:-}" a; read -rp "$p${d:+ [$d]}: " a; echo "${a:-$d}"; }
sync_repo(){
  [ -d .git ] && command -v git >/dev/null 2>&1 || { info "$(msg 'Not a Git checkout; using current files')"; return; }
  [ "$(ask "$(msg 'Fetch current code with git pull --ff-only? (y/n)')" y)" = y ] || return
  git pull --ff-only || die "$(msg 'Git cannot fast-forward. Resolve local changes or divergent history, then retry.')"
}
update_docker(){
  command -v docker >/dev/null 2>&1 && docker compose version >/dev/null 2>&1 || die "$(msg 'Install Docker and Docker Compose first')"
  [ -f .env ] || die "$(msg 'Missing .env; run ./install.sh first')"
  info "$(msg 'Building Counterweight from this checkout…')"
  docker compose build counterweight
  docker compose up -d counterweight
  info "$(msg 'Updated: https://localhost:8787. Logs: docker compose logs -f counterweight')"
}
update_local(){
  command -v go >/dev/null 2>&1 || die "$(msg 'Go 1.26 or later required: https://go.dev/dl/')"
  if command -v npm >/dev/null 2>&1; then
    (cd web && npm ci && npm run build:static)
    mkdir -p server/webui/dist
    cp -R web/out/. server/webui/dist/
    CGO_ENABLED=0 go build -tags embedui -trimpath -o counterweight ./cmd/counterweight
  else
    info "$(msg 'npm unavailable: building backend only; run the frontend separately')"
    CGO_ENABLED=0 go build -o counterweight ./cmd/counterweight
  fi
  info "$(msg 'Built ./counterweight. Restart the running process to apply the update.')"
}
echo "Counterweight"
echo "1) $(msg 'Rebuild Docker deployment')"
echo "2) $(msg 'Rebuild local binary')"
case "$(ask "$(msg 'Choose')" 1)" in
  1) sync_repo; update_docker ;;
  2) sync_repo; update_local ;;
  *) die "$(msg 'Invalid choice')" ;;
esac
