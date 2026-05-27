#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
cd "$ROOT_DIR"

COMPOSE_FILE="${COMPOSE_FILE:-$ROOT_DIR/docker-compose.yml}"
DOCKERFILE="${DOCKERFILE:-$ROOT_DIR/Dockerfile}"
ENTRYPOINT_FILE="${ENTRYPOINT_FILE:-$ROOT_DIR/docker-entrypoint.sh}"
CONTAINER_CLI="${CONTAINER_CLI:-}"

SERVICE_NAME="${SERVICE_NAME:-upimg}"
CONTAINER_NAME="${CONTAINER_NAME:-upimg}"
IMAGE_NAME="${IMAGE_NAME:-upimg:dist}"
GITHUB_LATEST_RELEASE_API="https://api.github.com/repos/cgistar/upimg/releases/latest"

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

archive_relpath() {
  local archive_path="$1"
  case "$archive_path" in
    "$ROOT_DIR"/*)
      printf '%s\n' "${archive_path#"$ROOT_DIR"/}"
      ;;
    *)
      echo "archive must be inside project root: $archive_path" >&2
      exit 1
      ;;
  esac
}

detect_release_arch() {
  local arch="${RELEASE_ARCH:-${ARCH:-$(uname -m)}}"
  case "$arch" in
    amd64 | x86_64)
      printf '%s\n' "amd64"
      ;;
    arm64 | aarch64)
      printf '%s\n' "arm64"
      ;;
    *)
      echo "unsupported release arch: $arch (expected amd64 or arm64)" >&2
      exit 1
      ;;
  esac
}

prompt_yes_no() {
  local question="$1"
  local default_answer="${2:-no}"
  local prompt="y/N"
  local answer

  if [[ "$default_answer" == "yes" ]]; then
    prompt="Y/n"
  fi

  while true; do
    printf '%s [%s] ' "$question" "$prompt" >&2
    IFS= read -r answer || answer=""

    case "${answer:-$default_answer}" in
      y | Y | yes | YES)
        return 0
        ;;
      n | N | no | NO)
        return 1
        ;;
      *)
        echo "please answer yes or no" >&2
        ;;
    esac
  done
}

download_proxy_arg() {
  local proxy_url="${SOCKS5_PROXY:-${HTTPS_PROXY:-${https_proxy:-}}}"
  local answer

  if [[ -n "$proxy_url" ]]; then
    printf '%s\n' "$proxy_url"
    return
  fi

  if ! prompt_yes_no "use proxy for GitHub download?" "no"; then
    return
  fi

  printf '%s' "proxy url (example: socks5://127.0.0.1:1080, empty for direct): " >&2
  IFS= read -r answer || answer=""
  printf '%s\n' "$answer"
}

curl_fetch() {
  local url="$1"
  local output="${2:-}"
  local proxy_url="${3:-}"
  local curl_args=(-fL --retry 3 --retry-delay 2 -H "User-Agent: upimg-build-docker")

  require_command curl

  if [[ -n "$proxy_url" ]]; then
    curl_args+=(--proxy "$proxy_url")
  fi

  if [[ -n "$output" ]]; then
    curl "${curl_args[@]}" -o "$output" "$url"
    return
  fi

  curl "${curl_args[@]}" "$url"
}

find_local_archives() {
  local release_arch="$1"
  local archive
  shopt -s nullglob
  for archive in \
    "$ROOT_DIR"/upimg-linux-"$release_arch".tar.gz \
    "$ROOT_DIR"/upimg-v*-linux-"$release_arch".tar.gz; do
    [[ -f "$archive" ]] && printf '%s\n' "$archive"
  done
  shopt -u nullglob
}

choose_from_list() {
  local title="$1"
  shift
  local items=("$@")
  local choice

  if [[ "${#items[@]}" -eq 0 ]]; then
    return 1
  fi
  if [[ "${#items[@]}" -eq 1 ]]; then
    printf '%s\n' "${items[0]}"
    return
  fi

  echo "$title" >&2
  local index=1
  local item
  for item in "${items[@]}"; do
    echo "  $index) $(basename "$item")" >&2
    index=$((index + 1))
  done

  while true; do
    printf 'select archive [1-%d]: ' "${#items[@]}" >&2
    IFS= read -r choice || choice=""
    if [[ "$choice" =~ ^[0-9]+$ ]] && ((choice >= 1 && choice <= ${#items[@]})); then
      printf '%s\n' "${items[$((choice - 1))]}"
      return
    fi
    echo "invalid selection: $choice" >&2
  done
}

asset_urls_from_release_json() {
  local release_json="$1"
  if command -v jq >/dev/null 2>&1; then
    jq -r '.assets[].browser_download_url' "$release_json"
    return
  fi

  grep -Eo '"browser_download_url"[[:space:]]*:[[:space:]]*"[^"]+"' "$release_json" |
    sed -E 's/^.*"([^"]+)"$/\1/'
}

select_release_asset_url() {
  local release_json="$1"
  local release_arch="$2"
  local url
  local name

  while IFS= read -r url; do
    name="$(basename "$url")"
    if [[ "$name" =~ ^upimg(-v[^/]*)?-linux-${release_arch}\.tar\.gz$ ]]; then
      printf '%s\n' "$url"
      return
    fi
  done < <(asset_urls_from_release_json "$release_json")

  echo "release asset not found for linux-$release_arch" >&2
  echo "available upimg linux assets:" >&2
  asset_urls_from_release_json "$release_json" |
    sed -n 's#.*/##p' |
    grep -E '^upimg(-v[^/]*)?-linux-(amd64|arm64)\.tar\.gz$' >&2 || true
  return 1
}

download_archive_from_github() {
  local release_arch="$1"
  local proxy_url="$2"
  local release_json
  local asset_url=""
  local output_path

  release_json="$(mktemp)"

  echo "fetching latest release metadata: $GITHUB_LATEST_RELEASE_API" >&2
  if curl_fetch "$GITHUB_LATEST_RELEASE_API" "$release_json" "$proxy_url" >/dev/null; then
    asset_url="$(select_release_asset_url "$release_json" "$release_arch" || true)"
  else
    echo "failed to fetch release metadata; falling back to GitHub latest download URL" >&2
  fi
  rm -f "$release_json"

  if [[ -z "$asset_url" ]]; then
    asset_url="https://github.com/cgistar/upimg/releases/latest/download/upimg-linux-${release_arch}.tar.gz"
  fi
  output_path="$ROOT_DIR/upimg-linux-${release_arch}.tar.gz"

  echo "downloading archive: $asset_url" >&2
  if ! curl_fetch "$asset_url" "$output_path" "$proxy_url" >/dev/null; then
    exit 1
  fi
  printf '%s\n' "$output_path"
}

select_archive() {
  local release_arch="$1"
  local local_archives=()
  local selected_archive
  local proxy_url

  if [[ -n "${DIST_ARCHIVE:-}" ]]; then
    if [[ ! -f "$DIST_ARCHIVE" ]]; then
      echo "DIST_ARCHIVE not found: $DIST_ARCHIVE" >&2
      exit 1
    fi
    DIST_ARCHIVE="$(cd "$(dirname "$DIST_ARCHIVE")" && pwd)/$(basename "$DIST_ARCHIVE")"
    printf '%s\n' "$DIST_ARCHIVE"
    return
  fi

  while IFS= read -r selected_archive; do
    local_archives+=("$selected_archive")
  done < <(find_local_archives "$release_arch")
  if [[ "${#local_archives[@]}" -gt 0 ]]; then
    choose_from_list "found local linux-$release_arch upimg archives:" "${local_archives[@]}"
    return
  fi

  echo "linux-$release_arch upimg archive not found in project root" >&2
  if ! prompt_yes_no "download latest release from https://github.com/cgistar/upimg?" "no"; then
    echo "archive is required to build Docker image" >&2
    exit 1
  fi

  proxy_url="$(download_proxy_arg)"
  selected_archive="$(download_archive_from_github "$release_arch" "$proxy_url")"
  printf '%s\n' "$selected_archive"
}

generate_dockerfile() {
  local archive_rel="$1"
  cat >"$DOCKERFILE" <<EOF
FROM alpine:3.22

# 安装 HTTPS 证书与时区数据，保证 S3 HTTPS 请求和时间模板在最小镜像中可用。
RUN echo -e https://mirrors.ustc.edu.cn/alpine/v3.22/main/ > /etc/apk/repositories && \\
    apk --no-cache add ca-certificates tzdata && \\
    ln -snf /usr/share/zoneinfo/Asia/Shanghai /etc/localtime && \\
    echo "Asia/Shanghai" > /etc/timezone && \\
    mkdir -p /app /data /opt/upimg-defaults

WORKDIR /data

COPY ${archive_rel} /tmp/upimg-linux.tar.gz
COPY docker-entrypoint.sh /usr/local/bin/docker-entrypoint.sh

RUN tar -xzf /tmp/upimg-linux.tar.gz -C /app && \\
    chmod +x /app/upimg /usr/local/bin/docker-entrypoint.sh && \\
    rm -f /tmp/upimg-linux.tar.gz

ENV DATA=/data
ENV FILEPATH=/data/files
ENV PORT=17788

EXPOSE 17788

ENTRYPOINT ["/usr/local/bin/docker-entrypoint.sh"]
EOF
}

generate_entrypoint() {
  cat >"$ENTRYPOINT_FILE" <<'EOF'
#!/bin/sh
set -eu

DEFAULTS_DIR="/opt/upimg-defaults"
DATA_DIR="${DATA:-/data}"
FILE_DIR="${FILEPATH:-$DATA_DIR/files}"

mkdir -p "$DATA_DIR" "$FILE_DIR"

# 仅在目标缺失时补默认配置，避免覆盖用户已经调整过的运行配置。
if [ -f "$DEFAULTS_DIR/config.json" ] && [ ! -f "$DATA_DIR/config.json" ]; then
  cp "$DEFAULTS_DIR/config.json" "$DATA_DIR/config.json"
fi

exec /app/upimg "$@"
EOF
  chmod +x "$ENTRYPOINT_FILE"
}

generate_compose_file() {
  cat >"$COMPOSE_FILE" <<EOF
services:
  ${SERVICE_NAME}:
    build:
      context: .
      dockerfile: Dockerfile
    image: ${IMAGE_NAME}
    container_name: ${CONTAINER_NAME}
    restart: unless-stopped
    ports:
      - '\${HOST_PORT:-17788}:\${PORT:-17788}'
    environment:
      DATA: '/data'
      FILEPATH: '\${FILEPATH:-/data/files}'
      PORT: '\${PORT:-17788}'
      # 如需覆盖 config.json 中的 key，可取消注释：
      # KEY: '\${KEY:-secret}'
    volumes:
      - '\${DATA_DIR:-./data}:/data'
      - '\${FILES_DIR:-/home/upimg}:/data/files'
EOF
}

compose_container_ids() {
  compose ps -q 2>/dev/null || true
}

remove_compose_containers() {
  local container_ids
  container_ids="$(compose_container_ids)"
  if [[ -n "$container_ids" ]]; then
    echo "stopping and removing existing compose containers"
    compose down --remove-orphans
    return
  fi
  echo "compose services are not running"
}

remove_named_container() {
  local container_ids
  container_ids="$("$CONTAINER_CLI" ps -aq --filter "name=^/${CONTAINER_NAME}$" 2>/dev/null || true)"
  if [[ -z "$container_ids" ]]; then
    return
  fi

  echo "removing existing container: $CONTAINER_NAME"
  "$CONTAINER_CLI" stop $container_ids >/dev/null 2>&1 || true
  "$CONTAINER_CLI" rm -f $container_ids >/dev/null 2>&1 || true
}

remove_old_image() {
  if "$CONTAINER_CLI" image inspect "$IMAGE_NAME" >/dev/null 2>&1; then
    echo "removing old image: $IMAGE_NAME"
    "$CONTAINER_CLI" image rm -f "$IMAGE_NAME"
    return
  fi
  echo "old image not found: $IMAGE_NAME"
}

release_arch="$(detect_release_arch)"
selected_archive="$(select_archive "$release_arch")"
if [[ ! -f "$selected_archive" ]]; then
  echo "linux-$release_arch upimg archive not found: $selected_archive" >&2
  exit 1
fi
selected_archive_rel="$(archive_relpath "$selected_archive")"

echo "using release arch: $release_arch"
echo "using archive: $selected_archive_rel"
echo "generating Dockerfile: $DOCKERFILE"
generate_dockerfile "$selected_archive_rel"
echo "generating entrypoint: $ENTRYPOINT_FILE"
generate_entrypoint
echo "generating compose file: $COMPOSE_FILE"
generate_compose_file

CONTAINER_CLI="$(detect_container_cli)"
COMPOSE_DRIVER="$(detect_compose_driver "$CONTAINER_CLI")"

remove_compose_containers
remove_named_container
remove_old_image

echo "building and starting compose service..."
compose up -d --build

echo "service started:"
compose ps
