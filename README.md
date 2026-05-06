# upimg

`upimg` 是一个轻量级图片/文件上传服务，支持 HTTP API、命令行上传、本地文件系统存储和 S3 兼容对象存储。

服务启动时会优先使用 `config.json` 中 `selected: true` 且可连通的 S3 配置；如果 S3 配置缺失、无效或探测失败，则自动回退到本地目录存储。

本服务可轻量化替代PicGo app，在 obsidian 插件 Image auto upload 中配置 https://www.demo.com/upload，就可以上传图片到当前服务器了

## 功能

- 上传本机路径文件：通过 JSON API 或命令行上传服务所在机器上的文件。
- 上传客户端文件：通过 `multipart/form-data` 直接上传文件内容。
- 文件列表：列出当前后端中的对象。
- 文件删除：按对象路径删除文件。
- 本地文件访问：本地存储模式下可通过 `/files/{path}` 访问文件。
- S3 兼容存储：支持 AWS S3 或带自定义 `endpoint` 的 S3 兼容服务。

## 快速开始

本地运行：

```bash
go run ./cmd/upimg
```

指定端口和本地存储目录：

```bash
PORT=17788 FILEPATH=/tmp/upimg-files go run ./cmd/upimg
```

构建发布包：

```bash
./bin/build.sh linux-amd
```

命令行上传本机文件：

```bash
go run ./cmd/upimg -target avatar /path/to/demo.png
```

`-target` 或 `-t` 只对命令行上传生效，表示上传到指定相对目录；未指定时使用 `rename` 模板生成对象路径。

## 配置

程序会按以下顺序查找配置文件：

1. 如果设置了 `DATA`，读取 `${DATA}/config.json`。
2. 否则读取可执行文件同目录下的 `config.json`。
3. 如果配置文件不存在，则使用默认值和环境变量。

示例：

```json
{
  "host": "0.0.0.0",
  "port": 17788,
  "key": "secret",
  "rename": "apps/{year}/{month}/{day}/{filename}",
  "filePath": "/var/www",
  "url_prefix": "https://example.com/upimg",
  "s3": [
    {
      "bucket": "my-bucket",
      "region": "us-east-1",
      "access_key": "ACCESS_KEY",
      "secret_key": "SECRET_KEY",
      "endpoint": "https://s3.amazonaws.com",
      "url_prefix": "https://cdn.example.com",
      "selected": true,
      "name": "default"
    }
  ]
}
```

配置字段：

| 字段 | 类型 | 默认值 | 说明 |
| --- | --- | --- | --- |
| `host` | string | `0.0.0.0` | HTTP 监听地址 |
| `port` | number | `17788` | HTTP 监听端口，可被环境变量 `PORT` 覆盖 |
| `key` | string | 空 | 上传和删除鉴权密钥，可被环境变量 `KEY` 覆盖；为空时不校验 |
| `rename` | string | `{fname}.{ext}` | 对象路径模板 |
| `filePath` | string | 当前工作目录 | 本地存储根目录，可被环境变量 `FILEPATH` 覆盖 |
| `url_prefix` | string | 空 | 本地存储返回 URL 的固定前缀；为空时根据请求 Host 生成 `/files` 地址 |
| `s3` | array | 空 | S3 配置列表 |

S3 字段：

| 字段 | 必填 | 说明 |
| --- | --- | --- |
| `bucket` | 是 | S3 bucket 名称 |
| `region` | 是 | S3 region |
| `access_key` | 是 | Access Key |
| `secret_key` | 是 | Secret Key |
| `endpoint` | 否 | S3 兼容服务 endpoint；设置后使用 path-style 请求 |
| `url_prefix` | 否 | 返回给客户端的 URL 前缀 |
| `selected` | 是 | 只有 `true` 且配置完整的项会被选中 |
| `name` | 否 | 配置名称，仅用于标识 |

`rename` 支持变量：

| 变量 | 说明 |
| --- | --- |
| `{year}` | 当前年份，例如 `2026` |
| `{month}` | 当前月份，例如 `05` |
| `{day}` | 当前日期，例如 `06` |
| `{unix_ts}` | 当前 Unix 时间戳 |
| `{fname_hash}` | 原文件名的 16 位短哈希 |
| `{filename}` | 完整文件名，例如 `demo.png` |
| `{fname}` | 不含扩展名的文件名，例如 `demo` |
| `{ext}` | 不含点号的扩展名，例如 `png` |

## HTTP API

默认地址为 `http://127.0.0.1:17788`。如果配置了 `key`，上传和删除接口必须带 `?key=...`。

所有接口都允许跨域请求，上传请求最大体积为 1 GiB。

### 上传客户端文件

```bash
curl -X POST "http://127.0.0.1:17788/upload?key=secret" \
  -F "file=@/path/to/demo.png"
```

响应：

```json
{
  "success": true,
  "result": ["http://127.0.0.1:17788/files/demo.png"],
  "fullResult": [
    {
      "fileName": "demo.png",
      "imgUrl": "http://127.0.0.1:17788/files/demo.png",
      "type": "local"
    }
  ]
}
```

### 上传服务端本机文件

JSON 上传读取的是服务进程所在机器上的文件路径，适合可信内网或自动化场景。

```bash
curl -X POST "http://127.0.0.1:17788/upload?key=secret" \
  -H "Content-Type: application/json" \
  -d '{"list":["/path/to/demo.png"]}'
```

### 查看上传页说明

```bash
curl "http://127.0.0.1:17788/upload"
```

返回一个简单 HTML 页面，说明 `/upload` 支持 JSON 和表单上传。

### 列出文件

```bash
curl "http://127.0.0.1:17788/list"
```

响应：

```json
{
  "success": true,
  "result": [
    {
      "path": "demo.png",
      "url": "http://127.0.0.1:17788/files/demo.png",
      "size": 12345,
      "modTime": "2026-05-06T10:00:00Z",
      "type": "local"
    }
  ]
}
```

### 访问本地文件

```bash
curl "http://127.0.0.1:17788/files/demo.png"
```

本地存储模式下直接返回文件内容；S3 模式下会重定向到对象 URL。

### 删除文件

```bash
curl -X DELETE "http://127.0.0.1:17788/delete/demo.png?key=secret"
```

响应：

```json
{
  "success": true,
  "message": "deleted"
}
```

## Docker

Docker 镜像使用仓库内的 Linux amd64 发布包构建。当前 `Dockerfile` 期望构建上下文根目录存在 `upimg-linux-amd.tar.gz`：

```bash
./bin/build.sh linux-amd
cp dist/upimg-linux-amd.tar.gz ./upimg-linux-amd.tar.gz
docker compose up -d --build
```

也可以使用脚本：

```bash
./build_docker.sh
```

如果使用脚本，请确认根目录已有 `upimg-linux-amd.tar.gz`，或按上面的命令从 `dist/` 复制一份。

### docker-compose.yml

默认 compose 配置：

```yaml
services:
  upimg:
    image: upimg:dist
    ports:
      - "17788:17788"
    environment:
      DATA: /data
      FILEPATH: /data/files
      PORT: 17788
      # KEY: secret
    volumes:
      - ./data:/data
      - /home/upimg:/data/files
```

Docker 环境变量：

| 变量 | 默认值 | 说明 |
| --- | --- | --- |
| `DATA` | `/data` | 配置目录，服务会读取 `${DATA}/config.json` |
| `FILEPATH` | `/data/files` | 本地存储根目录，优先级高于 `config.json` 中的 `filePath` |
| `PORT` | `17788` | HTTP 监听端口，优先级高于 `config.json` 中的 `port` |
| `KEY` | 空 | 上传和删除鉴权密钥，优先级高于 `config.json` 中的 `key` |

Docker 卷挂载：

| 宿主机路径 | 容器路径 | 说明 |
| --- | --- | --- |
| `./data` | `/data` | 保存 `config.json` 等运行配置 |
| `/home/upimg` | `/data/files` | 本地存储文件目录 |

注意：

- 如果使用本地存储，必须持久化 `FILEPATH` 对应目录，否则容器重建后文件会丢失。
- 如果使用 S3 存储，仍建议挂载 `/data` 保存配置；文件内容会写入 S3。
- 容器入口脚本会创建 `DATA` 和 `FILEPATH` 目录。
- 只有镜像内存在 `/opt/upimg-defaults/config.json` 且 `${DATA}/config.json` 缺失时，入口脚本才会复制默认配置；当前 Dockerfile 未复制仓库根目录的 `config.json` 到该默认目录。

## 返回 URL 规则

本地存储：

- 如果配置了 `url_prefix`，返回 `url_prefix + "/" + object_path`。
- 如果没有配置 `url_prefix`，根据请求协议和 Host 返回 `http://host/files/object_path`。
- 反向代理 HTTPS 时可设置请求头 `X-Forwarded-Proto: https`，服务会用该协议生成 URL。

S3 存储：

- 如果 S3 配置了 `url_prefix`，返回 `url_prefix + "/" + object_path`。
- 如果配置了 `endpoint`，返回 `endpoint + "/" + bucket + "/" + object_path`。
- 否则返回 AWS S3 默认 URL：`https://{bucket}.s3.{region}.amazonaws.com/{object_path}`。

## 开发

运行测试：

```bash
go test ./...
```

常用构建目标：

```bash
./bin/build.sh macos-arm
./bin/build.sh linux-amd
./bin/build.sh all
```
