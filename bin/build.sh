#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
TARGET_DIR="${ROOT_DIR}/dist"

usage() {
  cat <<'USAGE'
Usage:
  bin/build.sh [target|all]

Targets:
  macos-arm      darwin/arm64
  macos-amd      darwin/amd64
  linux-arm      linux/arm64
  linux-amd      linux/amd64
  all            build all targets

Environment:
  SKIP_WEB=1      skip Vite admin UI build
USAGE
}

ensure_modules() {
  if [[ -f "${ROOT_DIR}/go.sum" ]]; then
    return
  fi

  echo "go.sum not found, running go mod download all"
  go mod download all
}

ensure_command() {
  local cmd="$1"
  if ! command -v "$cmd" >/dev/null 2>&1; then
    echo "missing required command: $cmd" >&2
    exit 1
  fi
}

build_web() {
  if [[ "${SKIP_WEB:-}" == "1" ]]; then
    echo "Skipping web UI build because SKIP_WEB=1"
    return
  fi
  if [[ ! -f "${ROOT_DIR}/web/package.json" ]]; then
    return
  fi

  ensure_command npm
  echo "Building admin web UI"
  local npm_cache="${NPM_CACHE:-${ROOT_DIR}/.npm-cache}"
  if [[ -f "${ROOT_DIR}/web/package-lock.json" ]]; then
    npm --cache "$npm_cache" ci --prefix "${ROOT_DIR}/web"
  else
    npm --cache "$npm_cache" install --prefix "${ROOT_DIR}/web"
  fi
  npm --cache "$npm_cache" run build --prefix "${ROOT_DIR}/web"
}

target_pair() {
  case "$1" in
    macos-arm) echo "darwin arm64" ;;
    macos-amd) echo "darwin amd64" ;;
    linux-arm) echo "linux arm64" ;;
    linux-amd) echo "linux amd64" ;;
    *) return 1 ;;
  esac
}

current_target() {
  local goos
  local goarch

  goos="$(go env GOOS)"
  goarch="$(go env GOARCH)"

  case "${goos}/${goarch}" in
    darwin/arm64) echo "macos-arm" ;;
    darwin/amd64) echo "macos-amd" ;;
    linux/arm64) echo "linux-arm" ;;
    linux/amd64) echo "linux-amd" ;;
    *)
      echo "Unsupported current environment: ${goos}/${goarch}" >&2
      return 1
      ;;
  esac
}

build_one() {
  local name="$1"
  local pair
  pair="$(target_pair "$name")"
  local goos="${pair%% *}"
  local goarch="${pair##* }"
  local binary="${TARGET_DIR}/upimg"
  local archive="${TARGET_DIR}/upimg-${name}64.tar.gz"

  mkdir -p "$TARGET_DIR"
  rm -f "$binary" "$archive"
  ensure_modules
  echo "Building ${name} (${goos}/${goarch})"
  CGO_ENABLED=0 GOOS="$goos" GOARCH="$goarch" \
    go build -trimpath -ldflags="-s -w" -o "$binary" ./cmd/upimg

  tar -C "$TARGET_DIR" -czf "$archive" upimg
  rm -f "$binary"
  echo "Created ${archive}"
}

main() {
  cd "$ROOT_DIR"
  local target="${1:-}"
  if [[ "$target" == "-h" || "$target" == "--help" ]]; then
    usage
    exit 0
  fi
  if [[ -z "$target" ]]; then
    target="$(current_target)"
  fi
  if [[ "$target" == "all" ]]; then
    build_web
    build_one macos-arm
    build_one macos-amd
    build_one linux-arm
    build_one linux-amd
    return
  fi
  if ! target_pair "$target" >/dev/null; then
    usage >&2
    exit 2
  fi
  build_web
  build_one "$target"
}

main "$@"
