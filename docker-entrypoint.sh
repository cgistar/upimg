#!/bin/sh
set -eu

DEFAULTS_DIR="/opt/upimg-defaults"
DATA_DIR="${DATA:-/data}"
FILE_DIR="${FILEPATH:-$DATA_DIR/files}"

mkdir -p "$DATA_DIR" "$FILE_DIR"

# 仅在目标缺失时补默认配置，避免覆盖用户已经调整过的运行配置
if [ -f "$DEFAULTS_DIR/config.json" ] && [ ! -f "$DATA_DIR/config.json" ]; then
  cp "$DEFAULTS_DIR/config.json" "$DATA_DIR/config.json"
fi

exec /app/upimg "$@"
