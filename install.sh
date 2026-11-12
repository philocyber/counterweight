#!/usr/bin/env bash
# Counterweight install script: ① all-in-Docker  ② build and run locally
set -euo pipefail
umask 077
cd "$(cd "$(dirname "$0")" && pwd)"

msg(){ printf '%s' "$1"; }

info(){ printf '\033[36m[*]\033[0m %s\n' "$*"; }
ok(){   printf '\033[32m[+]\033[0m %s\n' "$*"; }
warn(){ printf '\033[33m[!]\033[0m %s\n' "$*"; }
die(){  printf '\033[31m[x]\033[0m %s\n' "$*" >&2; exit 1; }
ask(){  local p="$1" d="${2:-}" a; read -rp "$p${d:+ [$d]}: " a; echo "${a:-$d}"; }
rand(){ head -c 18 /dev/urandom | base64 | tr -dc 'A-Za-z0-9' | head -c 24; }

# Protect before writing. Windows chmod does not remove inherited ACL grants.
# Destination directories must belong to the operator; this is not a sandbox
# against concurrent replacement of path components by another process.
private_credential(){
  [ ! -L "$1" ] && { [ ! -e "$1" ] || [ -f "$1" ]; } || die "$(msg 'Credential destination must be a regular file')"
  case "$(uname -s)" in
    MINGW*|MSYS*|CYGWIN*)
      command -v powershell.exe >/dev/null 2>&1 || die "$(msg 'Windows PowerShell is required to protect credential files')"
      powershell.exe -NoProfile -NonInteractive -WindowStyle Hidden -File "$(cygpath -w "$PWD/private-credential.ps1")" -Path "$(cygpath -w "$PWD/$1")" || die "$(msg 'Could not protect credential file')"
      ;;
    *)
      if [ ! -e "$1" ]; then (set -C; : > "$1"); fi
      chmod 600 "$1"
      ;;
  esac
}

# ── docker environment detection / auto-install ─────────────
ensure_docker(){
  if command -v docker >/dev/null 2>&1 && docker compose version >/dev/null 2>&1; then
    ok "$(msg 'Found docker and docker compose')"; return
  fi
  warn "$(msg 'docker / docker compose not found')"
  case "$(uname -s)" in
    Linux)
      if [ "$(ask "$(msg 'Install Docker automatically? (y/n)')" y)" = y ]; then
        curl -fsSL https://get.docker.com | sh
        sudo usermod -aG docker "$USER" || true
        ok "$(msg 'Docker installed (group change needs re-login to use without sudo)')"
      else
        die "$(msg 'Please install docker yourself and retry')"
      fi ;;
    Darwin) die "$(msg 'On macOS install Docker Desktop: https://www.docker.com/products/docker-desktop/')" ;;
    *)      die "$(msg 'Please install docker yourself and retry')" ;;
  esac
}

# ── HTTPS certificate for the Docker console ───────────────
# Offers a self-signed localhost certificate when none is present. Existing
# files are never replaced.
ensure_tls(){
  [ -s tls/server.pem ] && [ -s tls/server-key.pem ] && return
  [ ! -e tls/server.pem ] && [ ! -e tls/server-key.pem ] && [ ! -L tls/server.pem ] && [ ! -L tls/server-key.pem ] || die "$(msg 'Docker management requires HTTPS: place a trusted certificate in tls/server.pem and its matching key in tls/server-key.pem (see README).')"
  command -v openssl >/dev/null 2>&1 || die "$(msg 'Docker management requires HTTPS and openssl was not found: place a certificate in tls/server.pem and its key in tls/server-key.pem (see README).')"
  [ "$(ask "$(msg 'No HTTPS certificate in tls/. Create a self-signed one for localhost? (y/n)')" y)" = y ] || die "$(msg 'Place a trusted certificate in tls/server.pem and its matching key in tls/server-key.pem (see README).')"
  mkdir -p tls
  private_credential tls/server-key.pem
  # A config file instead of -addext keeps older LibreSSL (macOS) working.
  printf '[req]\ndistinguished_name=dn\nprompt=no\n[dn]\nCN=localhost\n[san]\nsubjectAltName=DNS:localhost,IP:127.0.0.1\n' > tls/.openssl.cnf
  openssl req -x509 -newkey rsa:2048 -nodes -days 365 -config tls/.openssl.cnf -extensions san \
    -keyout tls/server-key.pem -out tls/server.pem >/dev/null 2>&1 \
    || { rm -f tls/server.pem tls/server-key.pem tls/.openssl.cnf; die "$(msg 'Could not create the certificate with openssl')"; }
  rm -f tls/.openssl.cnf
  ok "$(msg 'Self-signed certificate written to tls/ (your browser will ask you to accept it once)')"
}

# ── ① all-in-Docker ─────────────────────────────────────────
install_docker(){
  ensure_tls
  ensure_docker
  [ ! -L .env ] || die "$(msg 'Refusing a symlinked .env credential file')"
  if [ ! -f .env ]; then
    [ -f .env.example ] || die "$(msg 'Missing .env.example template')"
    private_credential .env
    local pw key
    pw="$(ask "$(msg 'Postgres password (Enter for a random one)')" "$(rand)")"
    key="$(ask "$(msg 'ANTHROPIC_API_KEY (optional, can set later in the UI)')" '')"
    # Write through the protected file, without replacement/backup files or
    # treating credential characters as sed syntax or command-line arguments.
    while IFS= read -r line || [ -n "$line" ]; do
      case "$line" in
        POSTGRES_PASSWORD=*) printf 'POSTGRES_PASSWORD=%s\n' "$pw" ;;
        ANTHROPIC_API_KEY=*) printf 'ANTHROPIC_API_KEY=%s\n' "$key" ;;
        *) printf '%s\n' "$line" ;;
      esac
    done < .env.example > .env
    ok "$(msg '.env generated (POSTGRES_PASSWORD set)')"
  else
    private_credential .env
    info "$(msg 'Reusing existing .env')"
  fi
  info "$(msg 'Building Counterweight from this checkout…')"
  docker compose build counterweight
  docker compose up -d
  finish_docker
  info "$(msg 'View logs: docker compose logs -f counterweight')"
}

# Wait until the console answers, then print what a first-time owner needs: the
# address and, while the installation is unclaimed, its one-time setup token.
finish_docker(){
  info "$(msg 'Waiting for the console to answer…')"
  local tries=0 status=''
  # --fail: an error reply (for example 503 while the database is still starting) is not readiness.
  until status="$(docker compose exec -T counterweight curl -kfsS -m 3 https://localhost:8787/api/auth/status 2>/dev/null)" \
    && case "$status" in *'"initialized":'*) true ;; *) false ;; esac; do
    tries=$((tries + 1))
    if [ "$tries" -ge 60 ]; then
      warn "$(msg 'The console did not answer within two minutes. Check: docker compose logs counterweight')"
      return
    fi
    sleep 2
  done
  ok "$(msg 'Started → https://localhost:8787')"
  case "$status" in
    *'"initialized":false'*)
      info "$(msg 'First visit: the setup page asks for this one-time token:')"
      # MSYS_NO_PATHCONV keeps Git Bash on Windows from rewriting the container path.
      MSYS_NO_PATHCONV=1 docker compose exec -T counterweight /app/counterweight -setup-token \
        || warn "$(msg 'Could not read the token. Print it with: docker compose exec counterweight /app/counterweight -setup-token')"
      info "$(msg 'Then choose a password and sign in as COUNTERWEIGHT.')" ;;
  esac
}

# ── ② build and run locally ─────────────────────────────────
install_local(){
  echo "$(msg 'Database setup:')"
  echo "  1) $(msg 'Connect to an existing PostgreSQL')"
  echo "  2) $(msg 'Start a PostgreSQL with Docker (needs docker)')"
  case "$(ask "$(msg 'Choose')" 1)" in
    2)
      ensure_docker
      local pw; pw="$(ask "$(msg 'Postgres password (Enter for a random one)')" "$(rand)")"
      # Loopback-only port; the password travels in the environment of docker, not in its argument list.
      POSTGRES_PASSWORD="$pw" docker run -d --name counterweight-pg -p 127.0.0.1:5432:5432 \
        -e POSTGRES_USER=counterweight -e POSTGRES_PASSWORD -e POSTGRES_DB=counterweight \
        -v counterweight-pg:/var/lib/postgresql/data postgres:16-alpine
      DB_HOST=127.0.0.1 DB_PORT=5432 DB_USER=counterweight DB_PASS="$pw" DB_NAME=counterweight DB_SSL=disable ;;
    *)
      DB_HOST="$(ask "$(msg 'Database host')" 127.0.0.1)"
      DB_PORT="$(ask "$(msg 'Port')" 5432)"
      DB_USER="$(ask "$(msg 'User')" counterweight)"
      DB_PASS="$(ask "$(msg 'Password')" '')"
      DB_NAME="$(ask "$(msg 'Database name')" counterweight)"
      DB_SSL="$(ask "$(msg 'sslmode (disable/require)' 'sslmode (disable/require)')" disable)" ;;
  esac

  # Generate config.json
  [ ! -L config.json ] || die "$(msg 'Refusing a symlinked config.json credential file')"
  private_credential config.json
  cat > config.json <<JSON
{
  "database": {
    "host": "${DB_HOST}",
    "port": ${DB_PORT},
    "user": "${DB_USER}",
    "password": "${DB_PASS}",
    "dbname": "${DB_NAME}",
    "sslmode": "${DB_SSL}"
  }
}
JSON
  ok "$(msg 'config.json generated')"

  # go environment check
  command -v go >/dev/null 2>&1 || die "$(msg 'Go not found, install Go (>=1.26): https://go.dev/dl/')"
  ok "Go: $(go version)"

  # Embedding the frontend needs node to produce the static output
  if command -v npm >/dev/null 2>&1; then
    info "$(msg 'Building the frontend static output…')"
    ( cd web && npm ci && npm run build:static )
    rm -rf server/webui/dist
    mkdir -p server/webui/dist
    cp -R web/out/. server/webui/dist/
    info "$(msg 'Compiling the single binary with the frontend embedded…')"
    CGO_ENABLED=0 go build -tags embedui -trimpath -o counterweight ./cmd/counterweight
  else
    warn "$(msg 'npm not found: building the backend WITHOUT the embedded frontend (run npm run dev for the frontend)')"
    CGO_ENABLED=0 go build -o counterweight ./cmd/counterweight
  fi
  ok "$(msg 'Build complete → ./counterweight')"

  info "$(msg 'Starting… (Ctrl-C to quit)')"
  ./start.sh
}

echo "=============================="
echo "  $(msg 'Counterweight install')"
echo "  1) $(msg 'All-in-Docker install')"
echo "  2) $(msg 'Local run (go build)')"
echo "=============================="
case "$(ask "$(msg 'Choose')" 1)" in
  1) install_docker ;;
  2) install_local ;;
  *) die "$(msg 'Invalid choice')" ;;
esac
