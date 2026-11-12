#!/usr/bin/env bash
# Counterweight cross-platform release builder.
#
# The default mode builds one target and embeds the already-exported frontend.
# `./build.sh --release` builds and packages all supported desktop/server targets.
#
# Environment variables:
#   COUNTERWEIGHT_TARGET_OS=linux             One target OS in single-target mode.
#   COUNTERWEIGHT_TARGET_ARCH=amd64           One target arch in single-target mode.
#   COUNTERWEIGHT_TARGETS=linux/amd64,...     Comma-separated targets for multi-target mode.
#   COUNTERWEIGHT_BUILD_VERSION=v0.3.3        Version embedded in the binary and archive name.
#   COUNTERWEIGHT_OUTPUT=/path/to/counterweight       Explicit binary path in single-target mode.
#   COUNTERWEIGHT_OUTPUT_DIR=dist             Directory for default binary paths.
#   COUNTERWEIGHT_PACKAGE=1                   Create a zip archive for each target.
#   COUNTERWEIGHT_PACKAGE_DIR=dist            Directory for release archives.
#   COUNTERWEIGHT_COMPRESS=off                UPX mode: off, auto, or required.
#   COUNTERWEIGHT_UPX_ARGS="--best --lzma"    Arguments passed to UPX.
#   COUNTERWEIGHT_SKIP_FRONTEND=1             Reuse server/webui/dist (for CI artifact builds).
#   COUNTERWEIGHT_SKIP_NPM_CI=1               Skip npm ci while rebuilding the frontend.
#   COUNTERWEIGHT_GOSUMDB=sum.golang.org      Go checksum database.
set -euo pipefail
msg(){ printf '%s' "$1"; }


cd "$(cd "$(dirname "$0")" && pwd)"

info() { printf '\033[36m[*]\033[0m %s\n' "$*"; }
ok() { printf '\033[32m[+]\033[0m %s\n' "$*"; }
warn() { printf '\033[33m[!]\033[0m %s\n' "$*" >&2; }
die() { printf '\033[31m[x]\033[0m %s\n' "$*" >&2; exit 1; }

usage() {
    cat <<'EOF'
Usage: ./build.sh [--target OS/ARCH | --release] [--upx | --no-compress]
Default: build the current system and architecture.
--release: build and zip Linux/macOS amd64/arm64 and Windows amd64.
--target OS/ARCH: select a target, for example windows/amd64.
--upx: require UPX compression (may reduce compatibility on some Linux systems).
--no-compress: use Go linker stripping and zip compression without UPX.
--help: show help.
Override targets with COUNTERWEIGHT_TARGETS=linux/amd64,windows/amd64 ./build.sh --release.
EOF
}

RELEASE_TARGETS_DEFAULT="linux/amd64,linux/arm64,darwin/amd64,darwin/arm64,windows/amd64"
COUNTERWEIGHT_RELEASE="${COUNTERWEIGHT_RELEASE:-0}"
COUNTERWEIGHT_COMPRESS="${COUNTERWEIGHT_COMPRESS:-off}"
COUNTERWEIGHT_PACKAGE="${COUNTERWEIGHT_PACKAGE:-0}"

while [ "$#" -gt 0 ]; do
  case "$1" in
    --release)
      COUNTERWEIGHT_RELEASE=1
      COUNTERWEIGHT_PACKAGE=1
      shift
      ;;
    --target)
      [ "$#" -ge 2 ] || die "$(msg "--target requires OS/ARCH")"
      target_arg="$2"
      case "$target_arg" in
        */*)
          COUNTERWEIGHT_TARGET_OS="${target_arg%%/*}"
          COUNTERWEIGHT_TARGET_ARCH="${target_arg##*/}"
          COUNTERWEIGHT_TARGETS="$target_arg"
          ;;
        *) die "$(msg "Target must be OS/ARCH, e.g. linux/amd64")" ;;
      esac
      shift 2
      ;;
    --no-compress)
      COUNTERWEIGHT_COMPRESS=0
      shift
      ;;
    --upx)
      COUNTERWEIGHT_COMPRESS=required
      shift
      ;;
    --help|-h)
      usage
      exit 0
      ;;
    *) die "$(msg "Unknown argument: $1 (see --help)")" ;;
  esac
done

command -v go >/dev/null 2>&1 || die "$(msg "Go not found (Go 1.26 or later required)")"

COUNTERWEIGHT_GOSUMDB="${COUNTERWEIGHT_GOSUMDB:-sum.golang.org}"
if [ -z "${COUNTERWEIGHT_BUILD_VERSION:-}" ]; then
  if command -v git >/dev/null 2>&1 && git rev-parse --is-inside-work-tree >/dev/null 2>&1; then
    COUNTERWEIGHT_BUILD_VERSION="$(git describe --tags --always --dirty)"
  else
    COUNTERWEIGHT_BUILD_VERSION="dev"
  fi
fi
# Release tags are commonly passed as v0.3.3; keep the binary version consistent.
COUNTERWEIGHT_BUILD_VERSION="${COUNTERWEIGHT_BUILD_VERSION#v}"
COUNTERWEIGHT_OUTPUT_DIR="${COUNTERWEIGHT_OUTPUT_DIR:-dist}"
COUNTERWEIGHT_PACKAGE_DIR="${COUNTERWEIGHT_PACKAGE_DIR:-$COUNTERWEIGHT_OUTPUT_DIR}"
COUNTERWEIGHT_UPX_ARGS="${COUNTERWEIGHT_UPX_ARGS:---best --lzma}"

if [ "${COUNTERWEIGHT_RELEASE}" = "1" ]; then
  COUNTERWEIGHT_TARGETS="${COUNTERWEIGHT_TARGETS:-$RELEASE_TARGETS_DEFAULT}"
else
  COUNTERWEIGHT_TARGET_OS="${COUNTERWEIGHT_TARGET_OS:-$(GOSUMDB="$COUNTERWEIGHT_GOSUMDB" go env GOOS)}"
  COUNTERWEIGHT_TARGET_ARCH="${COUNTERWEIGHT_TARGET_ARCH:-$(GOSUMDB="$COUNTERWEIGHT_GOSUMDB" go env GOARCH)}"
  COUNTERWEIGHT_TARGETS="${COUNTERWEIGHT_TARGETS:-${COUNTERWEIGHT_TARGET_OS}/${COUNTERWEIGHT_TARGET_ARCH}}"
fi

if [ "${COUNTERWEIGHT_SKIP_FRONTEND:-0}" = "1" ]; then
  [ -d server/webui/dist ] || die "$(msg "COUNTERWEIGHT_SKIP_FRONTEND=1 but server/webui/dist is missing")"
else
  command -v npm >/dev/null 2>&1 || die "$(msg "npm not found (Node.js/npm required for frontend export)")"
  command -v rsync >/dev/null 2>&1 || die "$(msg "rsync not found")"
  info "$(msg "Building frontend static assets")"
  if [ "${COUNTERWEIGHT_SKIP_NPM_CI:-0}" != "1" ]; then
    (cd web && npm ci)
  fi
  (cd web && npm run build:static)
  info "$(msg "Syncing frontend to server/webui/dist")"
  mkdir -p server/webui/dist
  rsync -a --delete web/out/ server/webui/dist/
fi

compress_binary() {
  binary="$1"
  goos="$2"
  case "$COUNTERWEIGHT_COMPRESS" in
    0|off|false|none)
      info "$(msg "Skipping UPX: $binary")"
      return 0
      ;;
    auto|required|true|1) ;;
    *) die "$(msg "COUNTERWEIGHT_COMPRESS must be off, auto, or required")" ;;
  esac

  if ! command -v upx >/dev/null 2>&1; then
    if [ "$COUNTERWEIGHT_COMPRESS" = "required" ]; then
      die "$(msg "COUNTERWEIGHT_COMPRESS=required but upx was not found")"
    fi
    warn "$(msg "upx not found; retaining linker output: $binary")"
    return 0
  fi

  before=$(wc -c < "$binary" | tr -d ' ')
  upx_args="$COUNTERWEIGHT_UPX_ARGS"
  [ "$goos" = "darwin" ] && upx_args="$upx_args --force-macos"
  # shellcheck disable=SC2086
  if ! upx $upx_args -- "$binary"; then
    if [ "$COUNTERWEIGHT_COMPRESS" = "required" ]; then
      die "$(msg "UPX compression failed: $binary")"
    fi
    warn "$(msg "UPX does not support this format; retaining binary: $binary")"
    return 0
  fi
  after=$(wc -c < "$binary" | tr -d ' ')
  ok "$(msg "UPX complete: $binary (${before} -> ${after} bytes)")"
}

package_binary() {
  binary="$1"
  goos="$2"
  goarch="$3"
  package_name="counterweight-${COUNTERWEIGHT_BUILD_VERSION}-${goos}-${goarch}"
  package_root="${COUNTERWEIGHT_PACKAGE_DIR}/${package_name}"
  archive="${COUNTERWEIGHT_PACKAGE_DIR}/${package_name}.zip"

  command -v zip >/dev/null 2>&1 || die "$(msg "Packaging requires zip")"
  rm -rf "$package_root" "$archive"
  mkdir -p "$package_root"
  cp "$binary" "$package_root/"
  # Package the platform supervisor so updates can restart the process.
  if [ "$goos" = "windows" ]; then
    cp start.bat "$package_root/"
    cp private-credential.ps1 "$package_root/"
  else
    cp start.sh "$package_root/"
    chmod +x "$package_root/start.sh"
  fi
  cp -R skills "$package_root/"
  cp config.example.json "$package_root/"
  cp README.md LICENSE CHANGELOG.md "$package_root/"
  if [ -d docs ]; then cp -R docs "$package_root/"; fi
  # Ship the adapter sources without local dependencies, credentials or runtime state.
  mkdir -p "$package_root/adapters/agent"
  cp adapters/agent/package.json adapters/agent/package-lock.json adapters/agent/README.md adapters/agent/pi-extension.js "$package_root/adapters/agent/"
  cp -R adapters/agent/src adapters/agent/test "$package_root/adapters/agent/"
  mkdir -p "$package_root/log"
  cp log/changelog-*.md "$package_root/log/"
  (cd "$COUNTERWEIGHT_PACKAGE_DIR" && zip -q -r -9 "$(basename "$archive")" "$(basename "$package_root")")
  rm -rf "$package_root"
  ok "$(msg "Release archive: $archive")"
}

build_target() {
  target="$1"
  case "$target" in
    */*) ;;
    *) die "$(msg "Invalid target: $target (expected OS/ARCH)")" ;;
  esac
  goos="${target%%/*}"
  goarch="${target##*/}"
  case "$goos" in
    linux|darwin|windows) ;;
    *) die "$(msg "Unsupported OS: $goos (use linux, darwin, windows)")" ;;
  esac

  binary_name="counterweight"
  [ "$goos" = "windows" ] && binary_name="counterweight.exe"
  if [ -n "${COUNTERWEIGHT_OUTPUT:-}" ] && [ "$COUNTERWEIGHT_RELEASE" != "1" ]; then
    output="$COUNTERWEIGHT_OUTPUT"
  else
    output="${COUNTERWEIGHT_OUTPUT_DIR}/counterweight-${goos}-${goarch}/${binary_name}"
  fi
  mkdir -p "$(dirname "$output")"

  info "$(msg "Building ${goos}/${goarch}, version ${COUNTERWEIGHT_BUILD_VERSION}")"
  GOSUMDB="$COUNTERWEIGHT_GOSUMDB" \
  CGO_ENABLED=0 \
  GOOS="$goos" \
  GOARCH="$goarch" \
  go build \
    -tags embedui \
    -trimpath \
    -ldflags "-s -w -buildid= -X main.version=${COUNTERWEIGHT_BUILD_VERSION}" \
    -o "$output" \
    ./cmd/counterweight

  compress_binary "$output" "$goos"
  if command -v file >/dev/null 2>&1; then file "$output"; fi
  if [ "$COUNTERWEIGHT_PACKAGE" = "1" ]; then package_binary "$output" "$goos" "$goarch"; fi
  ok "$(msg "Build complete: $output")"
}

write_checksums() {
  [ "$COUNTERWEIGHT_PACKAGE" = "1" ] || return 0
  checksum_file="$COUNTERWEIGHT_PACKAGE_DIR/SHA256SUMS"
  if command -v sha256sum >/dev/null 2>&1; then
    (cd "$COUNTERWEIGHT_PACKAGE_DIR" && for archive in *.zip; do sha256sum "$archive"; done > "$(basename "$checksum_file")")
  elif command -v shasum >/dev/null 2>&1; then
    (cd "$COUNTERWEIGHT_PACKAGE_DIR" && for archive in *.zip; do shasum -a 256 "$archive"; done > "$(basename "$checksum_file")")
  else
    warn "$(msg "sha256sum and shasum unavailable; skipping SHA256SUMS")"
    return 0
  fi
  ok "$(msg "Checksum file: $checksum_file")"
}

mkdir -p "$COUNTERWEIGHT_OUTPUT_DIR"
if [ "$COUNTERWEIGHT_PACKAGE" = "1" ]; then mkdir -p "$COUNTERWEIGHT_PACKAGE_DIR"; fi

old_ifs="$IFS"
IFS=','
read -r -a targets <<< "$COUNTERWEIGHT_TARGETS"
IFS="$old_ifs"
[ "${#targets[@]}" -gt 0 ] || die "$(msg "COUNTERWEIGHT_TARGETS cannot be empty")"
for target in "${targets[@]}"; do
  target="${target//[[:space:]]/}"
  [ -n "$target" ] || continue
  build_target "$target"
done

if [ "$COUNTERWEIGHT_PACKAGE" = "1" ]; then
  write_checksums
  info "$(msg "Release archives generated in: $COUNTERWEIGHT_PACKAGE_DIR")"
fi
