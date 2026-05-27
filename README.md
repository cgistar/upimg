# upimg

`upimg` 是一个轻量级图片/文件上传服务，支持 HTTP API、命令行上传、本地文件系统存储、S3 兼容对象存储、WebDAV 存储和 SFTP 存储。

服务启动时会优先使用 `defaultTarget` 指定的默认存储；如果未配置 `defaultTarget`，则回退到本地目录存储。

本服务可轻量化替代PicGo app，在 obsidian 插件 Image auto upload 中配置 https://www.demo.com/upload 就可以上传图片到当前服务器了

可使用在 typora 的图片上传中，参数填写 /path/to/upimg

## 功能

- 上传本机路径文件：通过 JSON API 或命令行上传服务所在机器上的文件。
- 上传客户端文件：通过 `multipart/form-data` 直接上传文件内容。
- 文件列表：列出当前后端中的对象。
- 文件删除：按对象路径删除文件。
- 本地文件访问：本地存储模式下可通过 `/files/{path}` 访问文件。
- S3 兼容存储：支持 AWS S3 或带自定义 `endpoint` 的 S3 兼容服务。
- WebDAV 存储：支持 Basic Auth 或无认证的 WebDAV 远程上传、列表、删除和预览。
- SFTP 存储：支持密码或私钥认证、主机指纹校验、远程上传、列表、删除和预览。
- Web 管理界面：通过 `/admin/` 登录后管理 local/S3/WebDAV/SFTP 文件、上传、删除、编辑配置和测试远程连通性。

## 快速开始

本地运行：

```bash
go run ./cmd/upimg
```

本地联调 Web 管理界面：

```bash
./bin/run.sh --web-dev
```

该模式会启动 Vite dev server，并让 Go 服务把 `/admin/` 代理到 Vite。默认 Vite 端口为 `5173`，可用 `WEB_DEV_PORT=5174 ./bin/run.sh --web-dev` 覆盖。也可以直接访问 Vite 地址 `http://127.0.0.1:5173/admin/`，Vite 会把 `/api/*` 代理回 Go 服务；如果 Go 端口不是默认值，使用 `PORT=18081 ./bin/run.sh --web-dev` 或显式设置 `UPIMG_API_TARGET=http://127.0.0.1:18081`。

指定端口和本地存储目录：

```bash
PORT=17788 FILEPATH=/tmp/upimg-files go run ./cmd/upimg
```

构建发布包：

```bash
./bin/build.sh linux-amd
```

发布包内置 `/admin/` Web UI。构建脚本会先安装并构建 `web` 目录下的 Vite React 管理界面，再把构建产物嵌入 Go 二进制。若只需要调试 Go 编译，可使用：

```bash
SKIP_WEB=1 ./bin/build.sh linux-amd
```

命令行上传本机文件：

```bash
go run ./cmd/upimg /path/to/demo.png -t /mnt/www
```

`-t` 只对命令行上传生效，表示上传到指定相对目录；未指定时 S3/WebDAV/SFTP 使用当前配置的 `uploadPath` 作为上传目录，本地直接使用 `rename` 渲染结果。

## 配置

程序会按以下顺序查找配置文件：

1. 如果设置了 `DATA`，读取 `${DATA}/config.json`。
2. 读取当前工作目录下的 `config.json`。
3. 读取可执行文件同目录下的 `config.json`。
4. 如果配置文件不存在，则使用默认值和环境变量。

示例：

```json
{
  "host": "0.0.0.0",
  "port": 17788,
  "basePath": "",
  "key": "secret",
  "rename": "{md5}.{extName}",
  "filePath": "/var/www",
  "urlPrefix": "https://example.com/upimg",
  "defaultTarget": "webdav:0",
  "s3": [
    {
      "bucket": "my-bucket",
      "region": "us-east-1",
      "accessKeyID": "ACCESS_KEY",
      "secretAccessKey": "SECRET_KEY",
      "endpoint": "https://s3.amazonaws.com",
      "urlPrefix": "https://cdn.example.com",
      "uploadPath": "apps/{year}/{month}/{day}",
      "name": "default"
    }
  ],
  "webdav": [
    {
      "name": "nas",
      "endpoint": "https://dav.example.com/remote.php/dav/files/user",
      "username": "user",
      "password": "PASS",
      "rootPath": "upimg",
      "urlPrefix": "https://cdn.example.com/upimg",
      "uploadPath": "apps/{year}/{month}/{day}"
    }
  ],
  "sftp": [
    {
      "name": "server",
      "host": "sftp.example.com",
      "port": 22,
      "username": "user",
      "password": "PASS",
      "privateKey": "",
      "passphrase": "",
      "hostKeyFingerprint": "SHA256:xxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxx",
      "rootPath": "upimg",
      "urlPrefix": "https://cdn.example.com/upimg",
      "uploadPath": "apps/{year}/{month}/{day}"
    }
  ]
}
```

配置字段：

| 字段 | 类型 | 默认值 | 说明 |
| --- | --- | --- | --- |
| `host` | string | `0.0.0.0` | HTTP 监听地址 |
| `port` | number | `17788` | HTTP 监听端口，可被环境变量 `PORT` 覆盖 |
| `basePath` | string | 空 | 反向代理路径前缀，例如 `/upimg`，可被环境变量 `BASE_PATH` 覆盖 |
| `key` | string | 空 | 上传和删除鉴权密钥，可被环境变量 `KEY` 覆盖；为空时不校验 |
| `rename` | string | `{fname}{ext}` | 文件名模板，会追加到上传目录下 |
| `filePath` | string | 当前工作目录 | 本地存储根目录，可被环境变量 `FILEPATH` 覆盖 |
| `urlPrefix` | string | 空 | 本地存储返回 URL 的固定前缀；为空时根据请求 Host 生成 `/files` 地址 |
| `defaultTarget` | string | 空 | 默认上传后端，支持 `local`、`s3:<index>`、`webdav:<index>`、`sftp:<index>`；显式配置后目标不可用会报错 |
| `s3` | array | 空 | S3 配置列表 |
| `webdav` | array | 空 | WebDAV 配置列表 |
| `sftp` | array | 空 | SFTP 配置列表 |

S3 字段：

| 字段 | 必填 | 说明 |
| --- | --- | --- |
| `bucket` | 是 | S3 bucket 名称 |
| `region` | 是 | S3 region |
| `accessKeyID` | 是 | Access Key ID |
| `secretAccessKey` | 是 | Secret Access Key |
| `endpoint` | 否 | S3 兼容服务 endpoint；设置后使用 path-style 请求 |
| `urlPrefix` | 否 | 返回给客户端的 URL 前缀 |
| `uploadPath` | 否 | 当前 S3 配置的上传目录模板 |
| `name` | 否 | 配置名称；上传接口可通过 `/upload?name=xxx` 指定上传到该 S3 配置 |

WebDAV 字段：

| 字段 | 必填 | 说明 |
| --- | --- | --- |
| `endpoint` | 是 | WebDAV collection 地址，必须是 `http` 或 `https` |
| `username` | 否 | Basic Auth 用户名；为空时不加认证头 |
| `password` | 否 | Basic Auth 密码；为空时不加认证头 |
| `rootPath` | 否 | WebDAV 远程根目录 |
| `urlPrefix` | 否 | 返回给客户端的 URL 前缀；为空时返回 endpoint/rootPath/object_path |
| `uploadPath` | 否 | 当前 WebDAV 配置的上传目录模板 |
| `name` | 否 | 配置名称；上传接口可通过 `/upload?name=xxx` 指定上传到该 WebDAV 配置 |

SFTP 字段：

| 字段 | 必填 | 说明 |
| --- | --- | --- |
| `host` | 是 | SFTP 主机名或 IP |
| `port` | 否 | SFTP 端口，未填或为 `0` 时使用 `22` |
| `username` | 是 | SFTP 用户名 |
| `password` | 条件必填 | 密码认证；`password` 和 `privateKey` 至少填写一个 |
| `privateKey` | 条件必填 | PEM/OpenSSH 格式私钥内容，不是公钥，也不是文件路径 |
| `passphrase` | 否 | 加密私钥的口令；私钥没有加密时留空 |
| `hostKeyFingerprint` | 否 | 服务端主机公钥指纹，格式如 `SHA256:...`；管理界面不需要手工填写，首次测试会自动捕获并保存 |
| `rootPath` | 否 | SFTP 远程根目录，支持相对路径或绝对路径 |
| `urlPrefix` | 否 | 公开访问根 URL，表示 `rootPath` 对应的外部访问地址；为空时返回 `sftp://host:port/rootPath/object_path` |
| `uploadPath` | 否 | 当前 SFTP 配置的上传目录模板 |
| `name` | 否 | 配置名称；上传接口可通过 `/upload?name=xxx` 指定上传到该 SFTP 配置 |

S3/WebDAV/SFTP `uploadPath` 和全局 `rename` 都支持变量；`uploadPath` 只表示目录，最终对象路径为 `uploadPath + "/" + rename`。本地上传默认只使用 `rename`，也可以用 `/upload?path=...` 或命令行 `-t` 指定目录：

| 变量 | 说明 |
| --- | --- |
| `{year}` | 当前年份，例如 `2026` |
| `{month}` | 当前月份，例如 `05` |
| `{day}` | 当前日期，例如 `06` |
| `{unix_ts}` | 当前 Unix 时间戳 |
| `{fname_hash}` | 原文件名的 16 位短哈希 |
| `{filename}` | 完整文件名，例如 `demo.png` |
| `{fname}` | 第一个 `.` 之前的文件名，例如 `demo` |
| `{ext}` | 从第一个 `.` 开始的完整扩展名，例如 `.png`、`.tar.gz` |
| `{extName}` | 不带点的扩展名，例如 `png`、`tar.gz` |
| `{md5}` | 上传文件内容的 MD5 |

## HTTP API

默认地址为 `http://127.0.0.1:17788`。如果配置了 `key`，上传和删除接口必须带 `?key=...`。

所有接口都允许跨域请求，上传请求最大体积为 1 GiB。

## Web 管理界面

启动服务后访问：

```text
http://127.0.0.1:17788/admin/
```

登录使用运行时有效 key：如果设置了环境变量 `KEY`，使用 `KEY`；否则使用 `config.json.key`。如果 key 为空，管理界面会进入免登录状态并显示风险提示。

管理界面能力：

- 浏览 `local`、所有 `s3[]`、`webdav[]` 和 `sftp[]` 配置对应的文件目录。
- 上传文件到指定 local/S3/WebDAV/SFTP 目标和当前目录。
- 删除指定文件或目录；目录删除会递归删除其内容。
- 编辑 `config.json` 中的 key、rename、filePath、urlPrefix、host、port、defaultTarget、S3、WebDAV 和 SFTP 配置。
- 新增、删除 S3/WebDAV/SFTP 配置，并对单条远程配置执行连通性测试。

保存配置后，服务会原子写入 `config.json` 并热更新 key、local 路径、S3/WebDAV/SFTP 列表和默认后端。`host` 和 `port` 会写入配置文件，但当前进程监听地址不会改变，需要重启服务后生效。

如果 `KEY`、`PORT` 或 `FILEPATH` 环境变量存在，它们仍然优先于 `config.json`，管理界面会提示对应字段被环境变量覆盖。

### 反向代理路径前缀

如果 nginx 使用带路径前缀的反向代理，例如外部访问 `/upimg`，启动服务时设置：

```bash
BASE_PATH=/upimg ./bin/run.sh
```

或在 `config.json` 中设置：

```json
{
  "basePath": "/upimg"
}
```

nginx 示例：

```nginx
location /upimg/ {
  proxy_set_header Host $host;
  proxy_set_header X-Forwarded-Proto $scheme;
  proxy_pass http://127.0.0.1:17788;
}
```

访问地址：

```text
https://example.com/upimg/admin/
```

如果 nginx 已经 strip 了 `/upimg` 前缀再转发到服务，可以不设置 `BASE_PATH`，但建议加上 `X-Forwarded-Prefix`，让本地文件 URL 仍带外部前缀：

```nginx
location /upimg/ {
  proxy_set_header Host $host;
  proxy_set_header X-Forwarded-Proto $scheme;
  proxy_set_header X-Forwarded-Prefix /upimg;
  proxy_pass http://127.0.0.1:17788/;
}
```

### 上传客户端文件

```bash
curl -X POST "http://127.0.0.1:17788/upload?key=secret" \
  -F "file=@/path/to/demo.png"
```

如果 `config.json` 中配置了多个 S3/WebDAV/SFTP 目标并设置了 `name`，可以通过 `name` 指定本次上传目标：

```bash
curl -X POST "http://127.0.0.1:17788/upload?key=secret&name=default" \
  -F "file=@/path/to/demo.png"
```

`name=local` 会强制上传到本地存储；`path` 可以指定本次上传目录。当前目标是 S3/WebDAV/SFTP 时，`path` 作为对象目录；当前目标是本地存储时，文件会写入 `filePath/path` 下。S3、WebDAV 和 SFTP 的 `name` 必须唯一，否则请求会返回名称歧义错误：

```bash
curl -X POST "http://127.0.0.1:17788/upload?key=secret&name=local&path=path/to" \
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

如果需要直接返回 URL 文本而不是 JSON，可以追加 `f=raw`：

```bash
curl -X POST "http://127.0.0.1:17788/upload?key=secret&f=raw" \
  -F "file=@/path/to/demo.png"
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

列出根目录下的一层文件和目录：

```bash
curl "http://127.0.0.1:17788/list"
```

列出指定目录下的一层文件和目录：

```bash
curl "http://127.0.0.1:17788/list?path=image"
curl "http://127.0.0.1:17788/list?path=xxx/yyy"
```

`/list` 不会递归列出所有文件，只返回 `path` 指定目录下的一层内容；`path` 为空时表示根目录。目录项的 `path` 以 `/` 结尾，继续下钻时把该目录路径传给 `path`，例如返回 `image/` 后请求 `/list?path=image`。

字段说明：

| 字段 | 说明 |
| --- | --- |
| `path` | 文件或目录路径；目录以 `/` 结尾 |
| `url` | 文件访问地址；目录为空字符串 |
| `size` | 文件大小；目录为 `0` |
| `modTime` | 文件或目录的修改时间 |
| `type` | 存储后端类型，例如 `local`、`aws-s3`、`webdav` 或 `sftp` |
| `isDir` | `true` 表示目录，`false` 表示文件 |

响应：

```json
{
  "success": true,
  "result": [
    {
      "path": "image/",
      "url": "",
      "size": 0,
      "modTime": "2026-05-06T10:00:00Z",
      "type": "local",
      "isDir": true
    },
    {
      "path": "demo.png",
      "url": "http://127.0.0.1:17788/files/demo.png",
      "size": 12345,
      "modTime": "2026-05-06T10:00:00Z",
      "type": "local",
      "isDir": false
    }
  ]
}
```

### 访问本地文件

```bash
curl "http://127.0.0.1:17788/files/demo.png"
```

本地存储模式下直接返回文件内容；S3/WebDAV/SFTP 模式下会重定向到对象 URL。

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

Docker 镜像使用仓库内的 Linux amd64 发布包构建。
```bash
./build_docker.sh
```

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
| `BASE_PATH` | 空 | 反向代理路径前缀，优先级高于 `config.json` 中的 `basePath` |
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

- 如果配置了 `urlPrefix`，返回 `urlPrefix + "/" + object_path`。
- 如果没有配置 `urlPrefix`，根据请求协议和 Host 返回 `http://host/files/object_path`。
- 反向代理 HTTPS 时可设置请求头 `X-Forwarded-Proto: https`，服务会用该协议生成 URL。

S3 存储：

- 如果 S3 配置了 `urlPrefix`，返回 `urlPrefix + "/" + object_path`。
- 如果配置了 `endpoint`，返回 `endpoint + "/" + bucket + "/" + object_path`。
- 否则返回 AWS S3 默认 URL：`https://{bucket}.s3.{region}.amazonaws.com/{object_path}`。

WebDAV 存储：

- 如果 WebDAV 配置了 `urlPrefix`，返回 `urlPrefix + "/" + rootPath + "/" + object_path`。
- 如果没有配置 `urlPrefix`，返回 `endpoint + "/" + rootPath + "/" + object_path`。

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
