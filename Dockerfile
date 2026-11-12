# syntax=docker/dockerfile:1
# Build the frontend and binary from this checkout; no published image is required.
# Base images are pinned by index digest (all platforms); the tag is kept for readability and for Dependabot.
FROM --platform=$BUILDPLATFORM node:24-bookworm-slim@sha256:dc26ae92c9fb15cf6b1d63e20f0944b2efe0f115b20e00be48fb23fceb061c54 AS frontend
WORKDIR /src/web
COPY web/package*.json ./
RUN npm ci
COPY web/ ./
RUN npm run build:static

FROM --platform=$BUILDPLATFORM golang:1.26.3-bookworm@sha256:386d475a660466863d9f8c766fec64d7fdad3edac2c6a05020c09534d71edb4b AS backend
ARG TARGETOS=linux
ARG TARGETARCH
ARG VERSION=dev
WORKDIR /src
COPY go.mod go.sum ./
COPY third_party/ ./third_party/
RUN go mod download
COPY . .
COPY --from=frontend /src/web/out/ ./server/webui/dist/
RUN CGO_ENABLED=0 GOOS=${TARGETOS} GOARCH=${TARGETARCH} go build -tags embedui -trimpath \
    -ldflags "-s -w -X main.version=${VERSION}" -o /counterweight ./cmd/counterweight

FROM python:3.12-slim-bookworm@sha256:935c3c5b12357a7e10caa9c1e6ca5692344d97cf229832ef584be2cec9fc03df
# Runtime tools. Browser installation occurs at build time.
RUN apt-get update && apt-get install -y --no-install-recommends \
      ca-certificates ripgrep curl wget vim git jq unzip \
      dnsutils iputils-ping netcat-openbsd inetutils-telnet whois nmap \
    && rm -rf /var/lib/apt/lists/*
# Node 20 for Playwright, from the official release archive of a fixed version, verified against the
# checksum below (taken from the SHASUMS256.txt that nodejs.org publishes for that version). To move
# to another release, change the version and both checksums together.
ARG TARGETARCH
ARG NODE_VERSION=20.20.2
RUN set -eu; \
    case "${TARGETARCH:-amd64}" in \
      amd64) node_arch=x64;   node_sum=19e56f0825510207dd904f087fe52faa0a4eb6b2aab5f0ea7a33830d04888b8b ;; \
      arm64) node_arch=arm64; node_sum=47ef73d543ecf6eb19435f6c03a0ac4809b3bf0dd6b26c7c571efc2a6572a74d ;; \
      *) echo "unsupported architecture: ${TARGETARCH}" >&2; exit 1 ;; \
    esac; \
    curl -fsSLo /tmp/node.tgz "https://nodejs.org/dist/v${NODE_VERSION}/node-v${NODE_VERSION}-linux-${node_arch}.tar.gz"; \
    echo "${node_sum}  /tmp/node.tgz" | sha256sum -c -; \
    tar -xzf /tmp/node.tgz -C /usr/local --strip-components=1 --no-same-owner \
      --exclude='*/CHANGELOG.md' --exclude='*/LICENSE' --exclude='*/README.md'; \
    rm /tmp/node.tgz; \
    node --version; \
    npm --version
# Browser tooling comes from a committed lockfile: `npm ci` checks the version and integrity hash of
# every package in the dependency tree. The three tools are then linked into the global prefix from
# that verified tree, without a second download, so their commands are on the PATH as after a global
# install. Raise the three versions in docker/browser-tools/package.json and refresh the lockfile
# together.
COPY docker/browser-tools/ /opt/browser-tools/
RUN cd /opt/browser-tools && npm ci --no-audit --no-fund \
    && cd / && npm install -g --offline --no-audit --no-fund \
         /opt/browser-tools/node_modules/@playwright/mcp \
         /opt/browser-tools/node_modules/@playwright/cli \
         /opt/browser-tools/node_modules/playwright \
    && playwright-cli --help \
    && playwright install --with-deps chromium \
    && rm -rf /var/lib/apt/lists/*
# Supplementary scanners for code reviews (dependency advisories and committed
# secrets). Pinned releases, verified against the checksums their projects publish.
RUN set -eu; \
    case "${TARGETARCH:-amd64}" in \
      amd64) gl_arch=x64;   gl_sum=551f6fc83ea457d62a0d98237cbad105af8d557003051f41f3e7ca7b3f2470eb; \
             osv_arch=amd64; osv_sum=fb7dab19a8bd9bbe5c3189f09d4b7ee1820c9553a23bac202648a56efd2f6ee8 ;; \
      arm64) gl_arch=arm64; gl_sum=e4a487ee7ccd7d3a7f7ec08657610aa3606637dab924210b3aee62570fb4b080; \
             osv_arch=arm64; osv_sum=983d22fbad9724514e3fcc6acc83b5fa64c5ffa6b0d9f510028f22c1b223ff81 ;; \
      *) echo "unsupported architecture: ${TARGETARCH}" >&2; exit 1 ;; \
    esac; \
    curl -fsSLo /tmp/gitleaks.tgz "https://github.com/gitleaks/gitleaks/releases/download/v8.30.1/gitleaks_8.30.1_linux_${gl_arch}.tar.gz"; \
    echo "${gl_sum}  /tmp/gitleaks.tgz" | sha256sum -c -; \
    tar -xzf /tmp/gitleaks.tgz -C /usr/local/bin gitleaks; \
    rm /tmp/gitleaks.tgz; \
    curl -fsSLo /usr/local/bin/osv-scanner "https://github.com/google/osv-scanner/releases/download/v2.7.1/osv-scanner_linux_${osv_arch}"; \
    echo "${osv_sum}  /usr/local/bin/osv-scanner" | sha256sum -c -; \
    chmod 0755 /usr/local/bin/gitleaks /usr/local/bin/osv-scanner; \
    gitleaks version; \
    osv-scanner --version
WORKDIR /app
COPY --from=backend /counterweight /app/counterweight
# The supervisor forwards SIGTERM and restarts after a self-update.
COPY start.sh /app/start.sh
RUN chmod +x /app/counterweight /app/start.sh
COPY skills/ /app/skills/
COPY LICENSE /app/LICENSE
VOLUME ["/app/data"]
EXPOSE 8787 8788
ENTRYPOINT ["/app/start.sh"]
CMD ["-addr", ":8787", "-tls-cert", "/app/tls/server.pem", "-tls-key", "/app/tls/server-key.pem", "-proxy", ":8788"]
