# Devbox REST API 文档（中文）

> 适用版本：当前 `server` 实现（更新于 2026-04-14）

## 1. 基础信息

- 服务地址：`https://devbox-server.staging-usw-1.sealos.io`
- API 前缀：`/api/v1/devbox`
- 鉴权方式：`Authorization: Bearer <JWT>`（除 `/healthz` 外均需要）
- JWT 算法：`HS256`
- 命名空间来源：仅从 JWT claims 的 `namespace` 读取，接口不接收 `namespace` 参数
- 请求体类型：`application/json`（文件上传接口除外）

建议先设置公共变量：

```bash
HOST='https://devbox-server.staging-usw-1.sealos.io'
TOKEN='你的JWT'
DEVBOX_NAME='demo-devbox'
```

## 2. JWT 要求

JWT payload 至少包含：

```json
{
  "namespace": "ns-test",
  "iat": 1772089401,
  "exp": 1779289401
}
```

说明：

- `namespace` 必填，且必须符合 k8s DNS1123 label 规则
- `exp` 可选但建议配置，服务端会校验是否过期
- `nbf` 可选，若存在服务端会校验是否已生效

签发脚本示例：

```bash
JWT_SIGNING_KEY='replace-with-your-hs256-secret' \
  ./v2/server/scripts/issue-token.sh ns-test 86400
```

## 3. 响应约定

### 3.1 JSON 接口统一结构

```json
{
  "code": 200,
  "message": "ok",
  "data": {}
}
```

### 3.2 下载接口特殊说明

`GET /api/v1/devbox/{name}/files/download` 成功时直接返回二进制文件流，不走上述 JSON 包装。

## 4. 接口总览

| 方法 | 路径 | 鉴权 | 说明 |
|---|---|---|---|
| GET | `/healthz` | 否 | 健康检查 |
| POST | `/api/v1/devbox` | 是 | 创建 Devbox |
| GET | `/api/v1/devbox` | 是 | 列表 Devbox（支持按 `upstreamID` 过滤） |
| GET | `/api/v1/devbox/{name}` | 是 | 查询 Devbox 信息（状态 + SSH 信息） |
| POST | `/api/v1/devbox/{name}/pause/refresh` | 是 | 刷新自动暂停时间 |
| POST | `/api/v1/devbox/{name}/pause` | 是 | 暂停 Devbox（设为 Paused） |
| POST | `/api/v1/devbox/{name}/resume` | 是 | 恢复 Devbox（设为 Running） |
| DELETE | `/api/v1/devbox/{name}` | 是 | 销毁 Devbox |
| POST | `/api/v1/devbox/{name}/exec` | 是 | 经 Pod 内 `devbox-sdk-server` 执行命令 |
| POST | `/api/v1/devbox/{name}/files/upload` | 是 | 经 Pod 内 `devbox-sdk-server` 上传文件 |
| GET | `/api/v1/devbox/{name}/files/download` | 是 | 经 Pod 内 `devbox-sdk-server` 下载文件 |

## 5. 接口详情

### 5.1 健康检查

- 方法：`GET`
- 路径：`/healthz`
- 鉴权：否

示例：

```bash
curl -sS "${HOST}/healthz"
```

成功响应：

```json
{
  "code": 200,
  "message": "ok",
  "data": {
    "status": "healthy"
  }
}
```

### 5.2 创建 Devbox

- 方法：`POST`
- 路径：`/api/v1/devbox`
- 鉴权：是
- 请求体：

```json
{
  "name": "demo-devbox",
  "image": "registry.example.com/devbox/runtime:custom-v2",
  "upstreamID": "session-123",
  "env": {
    "FOO": "bar",
    "NODE_ENV": "production"
  },
  "pauseAt": "2026-03-03T09:00:00Z",
  "archiveAfterPauseTime": "24h",
  "labels": [
    {
      "key": "app.kubernetes.io/component",
      "value": "runtime"
    }
  ]
}
```

字段说明：

- `name` 必填，需满足 DNS1123 subdomain 规则
- `image` 可选，指定本次创建使用的 Devbox 镜像；不传时使用服务端默认镜像（`devbox.createDefaults.image`）
- `upstreamID` 可选，若传入会写入固定 label：`devbox.sealos.io/upstream-id=<upstreamID>`
- `env` 可选，透传到 Devbox CR 的 `spec.config.env`（对象格式：`{变量名: 变量值}`），变量名需符合 Kubernetes 环境变量命名规则；与默认环境变量重名时会覆盖默认值
- `pauseAt` 可选，RFC3339 时间；到点后服务端会自动把 Devbox 设为 `Paused`
- `archiveAfterPauseTime` 可选，Go duration 格式（如 `2h`、`30m`）；从 `Paused` 开始计时，到点后自动设为 `Shutdown`（走现有归档/提交流程）
- `labels` 可选，额外透传到 Devbox CR 的 labels，格式为 `{key,value}` 列表

示例：

```bash
curl -sS -X POST "${HOST}/api/v1/devbox" \
  -H "Authorization: Bearer ${TOKEN}" \
  -H "Content-Type: application/json" \
  --data '{"name":"demo-devbox","image":"registry.example.com/devbox/runtime:custom-v2","upstreamID":"session-123","env":{"FOO":"bar","NODE_ENV":"production"},"pauseAt":"2026-03-03T09:00:00Z","archiveAfterPauseTime":"24h"}'
```

成功响应（201）：

```json
{
  "code": 201,
  "message": "ok",
  "data": {
    "name": "demo-devbox",
    "namespace": "ns-test",
    "state": "Running"
  }
}
```

### 5.2.1 列表 Devbox（按 upstreamID 过滤）

- 方法：`GET`
- 路径：`/api/v1/devbox`
- 鉴权：是
- 查询参数：
  - `upstreamID`：可选，按固定 label `devbox.sealos.io/upstream-id=<upstreamID>` 过滤

示例（列出当前 namespace 下全部 Devbox）：

```bash
curl -sS -X GET "${HOST}/api/v1/devbox" \
  -H "Authorization: Bearer ${TOKEN}"
```

示例（按 upstreamID 过滤）：

```bash
curl -sS -X GET "${HOST}/api/v1/devbox?upstreamID=session-123" \
  -H "Authorization: Bearer ${TOKEN}"
```

成功响应（200）：

```json
{
  "code": 200,
  "message": "ok",
  "data": {
    "items": [
      {
        "name": "demo-devbox",
        "creationTimestamp": "2026-03-02T08:20:30Z",
        "deletionTimestamp": null,
        "state": {
          "spec": "Running",
          "status": "Running",
          "phase": "Running"
        }
      }
    ]
  }
}
```

### 5.3 查询 Devbox 信息

- 方法：`GET`
- 路径：`/api/v1/devbox/{name}`
- 鉴权：是

示例：

```bash
curl -sS -X GET "${HOST}/api/v1/devbox/${DEVBOX_NAME}" \
  -H "Authorization: Bearer ${TOKEN}"
```

成功响应（200）：

```json
{
  "code": 200,
  "message": "ok",
  "data": {
    "name": "demo-devbox",
    "creationTimestamp": "2026-02-26T07:20:30Z",
    "deletionTimestamp": null,
    "state": {
      "spec": "Running",
      "status": "Running",
      "phase": "Running"
    },
    "ssh": {
      "user": "devbox",
      "host": "staging-usw-1.sealos.io",
      "port": 2233,
      "target": "devbox@staging-usw-1.sealos.io -p 2233",
      "link": "ssh://devbox@staging-usw-1.sealos.io:2233",
      "command": "ssh -i <private-key-file> devbox@staging-usw-1.sealos.io -p 2233",
      "privateKeyEncoding": "base64",
      "privateKeyBase64": "<base64-private-key>"
    },
    "gateway": {
      "url": "https://devbox-gateway.staging-usw-1.sealos.io/codex/demo-unique-id",
      "token": "<signed-gateway-jwt>",
      "port": 1317,
      "uniqueID": "demo-unique-id"
    }
  }
}
```

说明：

- `creationTimestamp` / `deletionTimestamp` 来自 Devbox CR 的 `metadata.creationTimestamp` / `metadata.deletionTimestamp`
- `deletionTimestamp` 在未进入删除流程时为 `null`
- `ssh.user/host/port` 来自服务端配置文件 `ssh` 段
- `ssh.privateKeyBase64` 来自 Secret：`<namespace>/<devbox-name>` 的配置键（默认 `SEALOS_DEVBOX_PRIVATE_KEY`）
- `gateway.token` 返回的是服务端用 DevBox Secret 中的 `SEALOS_DEVBOX_JWT_SECRET` 签发的 HS256 JWT，可直接作为 `Authorization: Bearer <token>` 访问 app gateway
- 该 JWT payload 会包含 `namespace` 和 `devboxName`，并带有过期时间
- `gateway.url` 由服务端配置项 `gateway.domain + gateway.pathPrefix + status.network.uniqueID` 生成，例如 `https://devbox-gateway.staging-usw-1.sealos.io/codex/demo-unique-id`
- 服务端内部会维护一份基于 `status.network.uniqueID` 的内存索引，供后续快速定位对应 Devbox，无需 Redis
- 当外部 ingress 把 `/codex/*` 转发到 `v2/server` 时，服务端会继续反代到 `http://{uniqueID}.{namespace}.svc.cluster.local:1317`

### 5.3.1 两层 Ingress 建议

推荐拆成两层：

- 第一层：`v2/server` API ingress，转发到 API listener（默认 `:8090`），负责 JWT 鉴权和 DevBox 管理接口
- 第二层：devbox gateway ingress，将 `/codex/*` 转发到 gateway listener（默认 `:8091`），由 `v2/server` 再反代到对应 DevBox 的 `1317`

路径模式建议：

- app gateway 域名：`https://devbox-gateway.staging-usw-1.sealos.io`
- 固定路径前缀：`/codex`
- 最终访问地址：`/codex/{status.network.uniqueID}`

`v2/server` 会根据 `{status.network.uniqueID}` 查内存索引，找到对应 DevBox 所在 namespace，然后把请求反代到 `http://{uniqueID}.{namespace}.svc.cluster.local:1317`。首版只代理固定 `1317` 端口。当前进程内使用两个独立的 `http.Server`，分别承接 API 和 gateway 流量。

### 5.4 刷新自动暂停时间

- 方法：`POST`
- 路径：`/api/v1/devbox/{name}/pause/refresh`
- 鉴权：是
- 请求体：

```json
{
  "pauseAt": "2026-03-03T12:00:00Z"
}
```

字段说明：

- `pauseAt` 必填，RFC3339 时间
- 刷新会覆盖当前 `pauseAt`，并重置该 Devbox 的自动暂停计时

示例：

```bash
curl -sS -X POST "${HOST}/api/v1/devbox/${DEVBOX_NAME}/pause/refresh" \
  -H "Authorization: Bearer ${TOKEN}" \
  -H "Content-Type: application/json" \
  --data '{"pauseAt":"2026-03-03T12:00:00Z"}'
```

成功响应（200）：

```json
{
  "code": 200,
  "message": "ok",
  "data": {
    "name": "demo-devbox",
    "namespace": "ns-test",
    "pauseAt": "2026-03-03T12:00:00Z",
    "refreshedAt": "2026-03-02T09:00:00Z"
  }
}
```

### 5.5 暂停 Devbox

- 方法：`POST`
- 路径：`/api/v1/devbox/{name}/pause`
- 鉴权：是

示例：

```bash
curl -sS -X POST "${HOST}/api/v1/devbox/${DEVBOX_NAME}/pause" \
  -H "Authorization: Bearer ${TOKEN}"
```

成功响应（200）：

```json
{
  "code": 200,
  "message": "ok",
  "data": {
    "name": "demo-devbox",
    "namespace": "ns-test",
    "state": "Paused"
  }
}
```

### 5.6 恢复 Devbox

- 方法：`POST`
- 路径：`/api/v1/devbox/{name}/resume`
- 鉴权：是

示例：

```bash
curl -sS -X POST "${HOST}/api/v1/devbox/${DEVBOX_NAME}/resume" \
  -H "Authorization: Bearer ${TOKEN}"
```

成功响应（200）：

```json
{
  "code": 200,
  "message": "ok",
  "data": {
    "name": "demo-devbox",
    "namespace": "ns-test",
    "state": "Running"
  }
}
```

### 5.7 销毁 Devbox

- 方法：`DELETE`
- 路径：`/api/v1/devbox/{name}`
- 鉴权：是

示例：

```bash
curl -sS -X DELETE "${HOST}/api/v1/devbox/${DEVBOX_NAME}" \
  -H "Authorization: Bearer ${TOKEN}"
```

成功响应（200）：

```json
{
  "code": 200,
  "message": "ok",
  "data": {
    "name": "demo-devbox",
    "namespace": "ns-test",
    "status": "deletion requested"
  }
}
```

### 5.8 执行命令

- 方法：`POST`
- 路径：`/api/v1/devbox/{name}/exec`
- 鉴权：是
- 后端转发：`http://<podIP>:9757/api/v1/process/exec-sync`
- 请求体：

```json
{
  "command": ["sh", "-lc", "whoami"],
  "stdin": "",
  "timeoutSeconds": 30,
  "container": ""
}
```

字段说明：

- `command` 必填，字符串数组，元素不能为空
- `stdin` 可选，写入标准输入
- `timeoutSeconds` 可选，范围 `[1,600]`，默认 `30`
- `container` 可选，默认取 Pod 第一个容器

示例：

```bash
curl -sS -X POST "${HOST}/api/v1/devbox/${DEVBOX_NAME}/exec" \
  -H "Authorization: Bearer ${TOKEN}" \
  -H "Content-Type: application/json" \
  --data '{"command":["sh","-lc","echo hello-from-devbox"],"timeoutSeconds":30}'
```

成功响应（200）：

```json
{
  "code": 200,
  "message": "ok",
  "data": {
    "podName": "demo-devbox",
    "namespace": "ns-test",
    "container": "devbox",
    "command": ["sh", "-lc", "echo hello-from-devbox"],
    "exitCode": 0,
    "stdout": "hello-from-devbox\n",
    "stderr": "",
    "executedAt": "2026-02-26T07:30:00Z"
  }
}
```

### 5.9 上传文件

- 方法：`POST`
- 路径：`/api/v1/devbox/{name}/files/upload`
- 鉴权：是
- 后端转发：`http://<podIP>:9757/api/v1/files/write`
- Query 参数：

| 参数 | 必填 | 说明 |
|---|---|---|
| `path` | 是 | 容器内目标路径 |
| `mode` | 否 | 文件权限，八进制字符串，如 `0644` |
| `timeoutSeconds` | 否 | 超时秒数，范围 `[1,3600]`，默认 `300` |
| `container` | 否 | 指定容器名（用于校验目标容器存在） |

- Body：二进制文件内容（`application/octet-stream`）

示例：

```bash
echo "hello devbox" > /tmp/a.txt
curl -sS -X POST "${HOST}/api/v1/devbox/${DEVBOX_NAME}/files/upload?path=/home/devbox/workspace/a.txt&mode=0644" \
  -H "Authorization: Bearer ${TOKEN}" \
  -H "Content-Type: application/octet-stream" \
  --data-binary @/tmp/a.txt
```

成功响应（200）：

```json
{
  "code": 200,
  "message": "ok",
  "data": {
    "name": "demo-devbox",
    "namespace": "ns-test",
    "podName": "demo-devbox",
    "container": "devbox",
    "path": "/home/devbox/workspace/a.txt",
    "sizeBytes": 13,
    "mode": "0644",
    "uploadedAt": "2026-02-26T07:35:00Z",
    "timeoutSecond": 300
  }
}
```

### 5.10 下载文件

- 方法：`GET`
- 路径：`/api/v1/devbox/{name}/files/download`
- 鉴权：是
- 后端转发：`http://<podIP>:9757/api/v1/files/read`
- Query 参数：

| 参数 | 必填 | 说明 |
|---|---|---|
| `path` | 是 | 容器内源文件路径 |
| `filename` | 否 | 下载后的文件名（响应头） |
| `timeoutSeconds` | 否 | 超时秒数，范围 `[1,3600]`，默认 `300` |
| `container` | 否 | 指定容器名（用于校验目标容器存在） |

示例：

```bash
curl -sS -X GET "${HOST}/api/v1/devbox/${DEVBOX_NAME}/files/download?path=/home/devbox/workspace/a.txt" \
  -H "Authorization: Bearer ${TOKEN}" \
  -o /tmp/a.download.txt
```

成功响应：

- HTTP `200`
- 响应体：二进制文件流
- 常见响应头：
  - `Content-Type: application/octet-stream`
  - `Content-Disposition: attachment; filename="a.txt"`
  - `X-Devbox-Path: /home/devbox/workspace/a.txt`

## 6. SSH 使用示例（基于 info 接口）

以下示例从 info 接口拿到私钥并登录：

依赖：`jq`、`openssl`

```bash
INFO_JSON="$(curl -sS -X GET "${HOST}/api/v1/devbox/${DEVBOX_NAME}" -H "Authorization: Bearer ${TOKEN}")"

printf '%s' "$(echo "$INFO_JSON" | jq -r '.data.ssh.privateKeyBase64')" \
  | openssl base64 -d -A > /tmp/devbox.key
chmod 600 /tmp/devbox.key

SSH_USER="$(echo "$INFO_JSON" | jq -r '.data.ssh.user')"
SSH_HOST="$(echo "$INFO_JSON" | jq -r '.data.ssh.host')"
SSH_PORT="$(echo "$INFO_JSON" | jq -r '.data.ssh.port')"

ssh -i /tmp/devbox.key "${SSH_USER}@${SSH_HOST}" -p "${SSH_PORT}"
```

## 7. 错误码与常见原因

| HTTP Code | 场景 |
|---|---|
| `400` | 参数不合法（如 `name` 不合法、`command` 为空、`path` 缺失） |
| `401` | JWT 无效（签名错误、过期、claim 非法） |
| `404` | 资源不存在（Devbox / Pod / 文件不存在） |
| `409` | 状态冲突（如 Pod 非 Running 时执行命令/文件操作） |
| `500` | 服务端内部错误 |
| `504` | 执行命令或文件传输超时 |

JSON 错误响应示例：

```json
{
  "code": 401,
  "message": "invalid token"
}
```

## 8. 调试建议

如果经由某些网关出现 TLS 或 HTTP/2 兼容问题，可临时增加 curl 参数：

- 强制 HTTP/1.1：`--http1.1`
- 忽略证书校验（仅测试环境）：`-k`

示例：

```bash
curl --http1.1 -k -sS -X GET "${HOST}/api/v1/devbox/${DEVBOX_NAME}" \
  -H "Authorization: Bearer ${TOKEN}"
```

## 9. 生命周期联调脚本

新增脚本：`v2/server/scripts/test-lifecycle-api.sh`

用途：

- 创建带 `pauseAt` / `archiveAfterPauseTime` 的 Devbox
- 调用刷新接口 `POST /api/v1/devbox/{name}/pause/refresh`
- 自动轮询并验证状态按预期进入 `Paused` 和 `Shutdown`

示例：

```bash
TOKEN="$(JWT_SIGNING_KEY='replace-with-your-hs256-secret' ./v2/server/scripts/issue-token.sh default 86400)" \
  PAUSE_AFTER_SECONDS=90 \
  REFRESH_PAUSE_AFTER_SECONDS=180 \
  ARCHIVE_AFTER_DURATION=2m \
  ./v2/server/scripts/test-lifecycle-api.sh
```
