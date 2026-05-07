#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
cd "$ROOT_DIR"

COMPOSE_FILE="${COMPOSE_FILE:-$ROOT_DIR/docker-compose.yml}"
CONTAINER_CLI="${CONTAINER_CLI:-}"
DIST_ARCHIVE="${DIST_ARCHIVE:-$ROOT_DIR/upimg-linux-amd64.tar.gz}"

require_command() {
  local cmd="$1"
  if ! command -v "$cmd" >/dev/null 2>&1; then
    echo "missing required command: $cmd" >&2
    exit 1
  fi
}

detect_container_cli() {
  if [[ -n "$CONTAINER_CLI" ]]; then
    require_command "$CONTAINER_CLI"
    printf '%s\n' "$CONTAINER_CLI"
    return
  fi
  if command -v docker >/dev/null 2>&1; then
    printf '%s\n' "docker"
    return
  fi
  if command -v nerdctl >/dev/null 2>&1; then
    printf '%s\n' "nerdctl"
    return
  fi
  echo "missing required command: docker or nerdctl" >&2
  exit 1
}

detect_compose_driver() {
  case "$1" in
    docker)
      if docker compose version >/dev/null 2>&1; then
        printf '%s\n' "docker-compose-v2"
        return
      fi
      if command -v docker-compose >/dev/null 2>&1; then
        printf '%s\n' "docker-compose-v1"
        return
      fi
      ;;
    nerdctl)
      if nerdctl compose version >/dev/null 2>&1; then
        printf '%s\n' "nerdctl-compose"
        return
      fi
      ;;
  esac
  echo "missing required compose support for container CLI: $1" >&2
  exit 1
}

compose() {
  case "$COMPOSE_DRIVER" in
    docker-compose-v2)
      docker compose -f "$COMPOSE_FILE" "$@"
      return
      ;;
    docker-compose-v1)
      docker-compose -f "$COMPOSE_FILE" "$@"
      return
      ;;
    nerdctl-compose)
      nerdctl compose -f "$COMPOSE_FILE" "$@"
      return
      ;;
  esac
  echo "unsupported compose driver: $COMPOSE_DRIVER" >&2
  exit 1
}

compose_container_ids() {
  compose ps -q 2>/dev/null || true
}

compose_has_running_container() {
  local container_id=""
  while IFS= read -r container_id; do
    if [[ -n "$container_id" ]] && [[ "$("$CONTAINER_CLI" inspect -f '{{.State.Running}}' "$container_id" 2>/dev/null || true)" == "true" ]]; then
      return 0
    fi
  done < <(compose_container_ids)
  return 1
}

compose_image_names() {
  compose config 2>/dev/null | awk '
    $1 == "image:" {
      print $2
    }
  ' | awk '!seen[$0]++'
}

if [[ ! -f "$DIST_ARCHIVE" ]]; then
  echo "linux-amd dist archive not found, building it first..."
  ./bin/build.sh linux-amd
fi

CONTAINER_CLI="$(detect_container_cli)"
COMPOSE_DRIVER="$(detect_compose_driver "$CONTAINER_CLI")"

if [[ ! -f "$COMPOSE_FILE" ]]; then
  echo "compose file not found: $COMPOSE_FILE" >&2
  exit 1
fi

container_ids="$(compose_container_ids)"
if compose_has_running_container; then
  echo "stopping running compose services"
  compose down --remove-orphans
elif [[ -n "$container_ids" ]]; then
  echo "removing existing compose containers"
  compose down --remove-orphans
else
  echo "compose services are not running"
fi

while IFS= read -r image_name; do
  if [[ -z "$image_name" ]]; then
    continue
  fi
  if "$CONTAINER_CLI" image inspect "$image_name" >/dev/null 2>&1; then
    echo "removing old image: $image_name"
    "$CONTAINER_CLI" image rm -f "$image_name"
  else
    echo "old image not found: $image_name"
  fi
done < <(compose_image_names)

echo "building and starting compose service..."
compose up -d --build

echo "service started:"
compose ps
