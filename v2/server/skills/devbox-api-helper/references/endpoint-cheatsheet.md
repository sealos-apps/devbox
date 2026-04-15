# Devbox API Endpoint Cheatsheet

## Source

- This cheatsheet is intended for external API clients.
- Use this file as a quick reference for endpoint behavior and curl usage.

## Environment

```bash
DEVBOX_SERVER_HOST='https://your-devbox-api.example.com'
DEVBOX_SERVER_TOKEN='your-jwt-token'
DEVBOX_NAME='demo-devbox'
```

## Auth Model

- JWT HS256
- Required claim: `namespace`
- Namespace is derived from token only

## Endpoint List

- `GET /healthz`
- `POST /api/v1/devbox`
- `GET /api/v1/devbox`
- `GET /api/v1/devbox/{name}`
- `POST /api/v1/devbox/{name}/pause/refresh`
- `POST /api/v1/devbox/{name}/pause`
- `POST /api/v1/devbox/{name}/resume`
- `DELETE /api/v1/devbox/{name}`
- `POST /api/v1/devbox/{name}/exec`
- `POST /api/v1/devbox/{name}/files/upload`
- `GET /api/v1/devbox/{name}/files/download`

## Quick Curl Templates

Create:

```bash
curl -sS -X POST "${DEVBOX_SERVER_HOST}/api/v1/devbox" \
  -H "Authorization: Bearer ${DEVBOX_SERVER_TOKEN}" \
  -H "Content-Type: application/json" \
  --data "{\"name\":\"${DEVBOX_NAME}\",\"image\":\"registry.example.com/devbox/runtime:custom-v2\",\"upstreamID\":\"session-123\",\"env\":{\"FOO\":\"bar\",\"NODE_ENV\":\"production\"},\"pauseAt\":\"2026-03-03T09:00:00Z\",\"archiveAfterPauseTime\":\"24h\"}"
```

List all in namespace:

```bash
curl -sS -X GET "${DEVBOX_SERVER_HOST}/api/v1/devbox" \
  -H "Authorization: Bearer ${DEVBOX_SERVER_TOKEN}"
```

List by upstreamID:

```bash
curl -sS -X GET "${DEVBOX_SERVER_HOST}/api/v1/devbox?upstreamID=session-123" \
  -H "Authorization: Bearer ${DEVBOX_SERVER_TOKEN}"
```

Info:

```bash
curl -sS -X GET "${DEVBOX_SERVER_HOST}/api/v1/devbox/${DEVBOX_NAME}" \
  -H "Authorization: Bearer ${DEVBOX_SERVER_TOKEN}"
```

Refresh pauseAt:

```bash
curl -sS -X POST "${DEVBOX_SERVER_HOST}/api/v1/devbox/${DEVBOX_NAME}/pause/refresh" \
  -H "Authorization: Bearer ${DEVBOX_SERVER_TOKEN}" \
  -H "Content-Type: application/json" \
  --data '{"pauseAt":"2026-03-03T12:00:00Z"}'
```

Pause:

```bash
curl -sS -X POST "${DEVBOX_SERVER_HOST}/api/v1/devbox/${DEVBOX_NAME}/pause" \
  -H "Authorization: Bearer ${DEVBOX_SERVER_TOKEN}"
```

Resume:

```bash
curl -sS -X POST "${DEVBOX_SERVER_HOST}/api/v1/devbox/${DEVBOX_NAME}/resume" \
  -H "Authorization: Bearer ${DEVBOX_SERVER_TOKEN}"
```

Destroy:

```bash
curl -sS -X DELETE "${DEVBOX_SERVER_HOST}/api/v1/devbox/${DEVBOX_NAME}" \
  -H "Authorization: Bearer ${DEVBOX_SERVER_TOKEN}"
```

Exec:

```bash
curl -sS -X POST "${DEVBOX_SERVER_HOST}/api/v1/devbox/${DEVBOX_NAME}/exec" \
  -H "Authorization: Bearer ${DEVBOX_SERVER_TOKEN}" \
  -H "Content-Type: application/json" \
  --data '{"command":["sh","-lc","whoami"],"timeoutSeconds":30}'
```

Upload:

```bash
curl -sS -X POST "${DEVBOX_SERVER_HOST}/api/v1/devbox/${DEVBOX_NAME}/files/upload?path=/home/devbox/workspace/a.txt&mode=0644" \
  -H "Authorization: Bearer ${DEVBOX_SERVER_TOKEN}" \
  -H "Content-Type: application/octet-stream" \
  --data-binary @/tmp/a.txt
```

Download:

```bash
curl -sS -X GET "${DEVBOX_SERVER_HOST}/api/v1/devbox/${DEVBOX_NAME}/files/download?path=/home/devbox/workspace/a.txt" \
  -H "Authorization: Bearer ${DEVBOX_SERVER_TOKEN}" \
  -o /tmp/a.download.txt
```

## SSH From Info API

```bash
INFO_JSON="$(curl -sS -X GET "${DEVBOX_SERVER_HOST}/api/v1/devbox/${DEVBOX_NAME}" -H "Authorization: Bearer ${DEVBOX_SERVER_TOKEN}")"
printf '%s' "$(echo "$INFO_JSON" | jq -r '.data.ssh.privateKeyBase64')" \
  | openssl base64 -d -A > /tmp/devbox.key
chmod 600 /tmp/devbox.key
SSH_USER="$(echo "$INFO_JSON" | jq -r '.data.ssh.user')"
SSH_HOST="$(echo "$INFO_JSON" | jq -r '.data.ssh.host')"
SSH_PORT="$(echo "$INFO_JSON" | jq -r '.data.ssh.port')"
ssh -i /tmp/devbox.key "${SSH_USER}@${SSH_HOST}" -p "${SSH_PORT}"
```

## Common Error Mapping

- `400`: invalid input
- `401`: invalid/expired token
- `404`: devbox/pod/file not found
- `409`: pod not running or state conflict
- `500`: server or k8s API error
- `504`: exec/transfer timeout

## Curl Compatibility Flags

- Use `--http1.1` if HTTP2 framing issues appear
- Use `-k` only in non-production testing
