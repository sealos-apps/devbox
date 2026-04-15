# Devbox REST API Server

Detailed API doc: [`v2/server/docs/api.md`](./docs/api.md)

## Run

Start with config file (YAML):

```bash
go run ./cmd/devbox-api --config ./config/config.yaml
```

Or with make:

```bash
make -C v2/server run CONFIG=/Users/yy/archary/sealos-devbox/v2/server/config/config.yaml
```

## Config File

Sample config with field comments:

- [`v2/server/config/config.yaml`](./config/config.yaml)

Key sections:

- `server.listenAddress`: HTTP listen address
- `server.gatewayListenAddress`: gateway reverse-proxy listen address
- `server.logLevel`: structured log level (`debug` / `info` / `warn` / `error`), default `info`
- `server.lifecycleResyncInterval`: lifecycle 全量兜底扫描周期（Go duration，默认 `30m`）
- `auth.jwtSigningKey`: JWT HS256 signing key (inline)
- `auth.jwtSigningKeyFile`: JWT HS256 signing key file path
- `ssh.user`: SSH 用户名（用于 info 返回）
- `ssh.host`: SSH 连接主机（用于 info 返回）
- `ssh.port`: SSH 连接端口（用于 info 返回）
- `ssh.privateKeySecretKey`: 私钥在 Secret 中的键名（默认 `SEALOS_DEVBOX_PRIVATE_KEY`）
- `gateway.domain`: DevBox 应用 ingress 的外部域名，`v2/server` 会固定使用 `https://<domain><pathPrefix>/<uniqueID>`
- `gateway.pathPrefix`: DevBox 应用 ingress 的固定路径前缀，默认 `/codex`
- `gateway.port`: 应用网关默认端口，默认 `1317`
- `gateway.ssePath`: SSE 路径，默认 `/sse`
- `devbox.createDefaults.image`: default created devbox image
- `devbox.createDefaults.storageLimit`: default created devbox storage limit
- `devbox.createDefaults.resource.cpu`: default created devbox CPU
- `devbox.createDefaults.resource.memory`: default created devbox memory

## Kubernetes Deployment

Deployment manifest mounts config and JWT key files:

- ConfigMap: `/etc/devbox-server/config/config.yaml`
- Secret: `/etc/devbox-server/secret/jwt-signing.key`
- Container args: `--config=/etc/devbox-server/config/config.yaml`
- The sample Deployment listens on `8090` for API traffic and `8091` for gateway reverse-proxy traffic.
- The sample manifest only creates the API Deployment and Service. Ingress is intentionally left to external infra.

Manifest:

- [`v2/server/deploy/devbox-api.yaml`](./deploy/devbox-api.yaml)

## Token Script

Issue JWT token:

```bash
JWT_SIGNING_KEY='replace-with-your-hs256-secret' ./v2/server/scripts/issue-token.sh default 86400
```

## Curl Smoke Test

```bash
TOKEN="$(JWT_SIGNING_KEY='replace-with-your-hs256-secret' ./v2/server/scripts/issue-token.sh default 86400)" ./v2/server/scripts/test-api.sh
```

Step-by-step mode (input `继续` / `continue` before each step):

```bash
TOKEN="$(JWT_SIGNING_KEY='replace-with-your-hs256-secret' ./v2/server/scripts/issue-token.sh default 86400)" STEP_MODE=1 ./v2/server/scripts/test-api.sh
```

## Load Test

Create->Ready 趋势测试方案（1/s 创建，创建时设置 `pauseAt=+5m`、`archiveAfterPauseTime=10m`）：

- [`v2/server/docs/loadtest-plan.md`](./docs/loadtest-plan.md)

一键执行：

```bash
TOKEN="your-jwt" HOST="https://devbox-server.staging-usw-1.sealos.io" ./v2/server/scripts/loadtest-devbox.sh
```

直接运行负载工具：

```bash
go run ./cmd/devbox-loadtest \
  -host "https://devbox-server.staging-usw-1.sealos.io" \
  -token "your-jwt" \
  -total 5000 \
  -create-interval 1s \
  -pause-after 5m \
  -archive-after-pause 10m \
  -bucket-size 500
```

## API Summary

- `POST /api/v1/devbox` (create; only needs `name`)
- `GET /api/v1/devbox/{name}` (info; returns devbox state and ssh info, including base64 private key)
- `POST /api/v1/devbox/{name}/pause`
- `POST /api/v1/devbox/{name}/resume`
- `DELETE /api/v1/devbox/{name}`
- `POST /api/v1/devbox/{name}/exec`
- `POST /api/v1/devbox/{name}/files/upload`
- `GET /api/v1/devbox/{name}/files/download`
- `/{gateway.pathPrefix}/{uniqueID}[/*rest]` (reverse proxy to `http://{uniqueID}.{namespace}.svc.cluster.local:1317`)

Notes:

- `exec` / `files/upload` / `files/download` are proxied to in-pod `devbox-sdk-server` (`http://<podIP>:9757`), no longer using `kubectl exec`.
- `exec` keeps `stdin` compatibility by wrapping command execution when non-empty `stdin` is provided.
- Recommended ingress model:
  - ingress 1: expose the API listener on `server.listenAddress` with JWT auth
  - ingress 2: expose `/codex/*` to the gateway listener on `server.gatewayListenAddress`, then let `v2/server` reverse proxy to the target DevBox headless Service on port `1317`
- For path-based ingress, configure `gateway.domain` and `gateway.pathPrefix`, then `GET /api/v1/devbox/{name}` will return routes like `https://devbox-gateway.staging-usw-1.sealos.io/codex/<status.network.uniqueID>`.
- `GET /api/v1/devbox/{name}` returns `gateway` information so callers can discover the external app route plus the devbox JWT secret used by the app gateway.
- `v2/server` maintains an in-memory `uniqueID -> devbox/gateway` index from DevBox status updates, so the route model stays aligned with `status.network.uniqueID` without Redis.
- Gateway proxy requests are forwarded to `http://{uniqueID}.{namespace}.svc.cluster.local:1317`, with path prefix stripping plus `Location` / `Set-Cookie Path` rewrites for path-based access.

Create request example:

```json
{
  "name": "demo-devbox",
  "image": "registry.example.com/devbox/runtime:custom-v2",
  "env": {
    "FOO": "bar",
    "NODE_ENV": "production"
  }
}
```

The service uses a built-in devbox spec template, and reads default values (`image`, `cpu`, `memory`, `storageLimit`) from the YAML config file.
Namespace is always derived from JWT claim `namespace`.
