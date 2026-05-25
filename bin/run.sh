#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
WEB_DEV=0
WEB_DEV_PORT="${WEB_DEV_PORT:-5173}"

usage() {
  cat <<'USAGE'
Usage:
  bin/run.sh [--web-dev] [upimg args...]

Options:
  --web-dev        start Vite dev server and proxy /admin/ to it

Environment:
  WEB_DEV_PORT     Vite dev server port, default 5173
  UPIMG_API_TARGET Go API target for Vite proxy, default http://127.0.0.1:$PORT
USAGE
}

ARGS=()
while [[ $# -gt 0 ]]; do
  case "$1" in
    --web-dev)
      WEB_DEV=1
      shift
      ;;
    -h|--help)
      usage
      exit 0
      ;;
    *)
      ARGS+=("$1")
      shift
      ;;
  esac
done

cd "$ROOT_DIR"

if [[ "$WEB_DEV" != "1" ]]; then
  DATA="${DATA:-$ROOT_DIR}" exec go run ./cmd/upimg "${ARGS[@]+"${ARGS[@]}"}"
fi

if [[ ! -f "$ROOT_DIR/web/package.json" ]]; then
  echo "web/package.json not found" >&2
  exit 1
fi

NPM_CACHE="${NPM_CACHE:-$ROOT_DIR/.npm-cache}"
npm --cache "$NPM_CACHE" install --prefix "$ROOT_DIR/web"

UPIMG_API_TARGET="${UPIMG_API_TARGET:-http://127.0.0.1:${PORT:-17788}}" \
  npm --cache "$NPM_CACHE" run dev --prefix "$ROOT_DIR/web" -- --port "$WEB_DEV_PORT" &
WEB_PID=$!

cleanup() {
  kill "$WEB_PID" >/dev/null 2>&1 || true
}
trap cleanup EXIT INT TERM

UPIMG_WEB_DEV_URL="${UPIMG_WEB_DEV_URL:-http://127.0.0.1:${WEB_DEV_PORT}}" \
  DATA="${DATA:-$ROOT_DIR}" \
  go run ./cmd/upimg "${ARGS[@]+"${ARGS[@]}"}"
