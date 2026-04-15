#!/usr/bin/env bash
set -euo pipefail

# Lifecycle API smoke test for pauseAt/archiveAfterPauseTime/refresh.
#
# Required env:
#   TOKEN (JWT, with namespace claim)
#
# Optional env:
#   HOST (default: https://devbox-server.staging-usw-1.sealos.io)
#   DEVBOX_NAME (default: devbox-lifecycle-test-<timestamp>)
#   PAUSE_AFTER_SECONDS (default: 90)
#   ARCHIVE_AFTER_DURATION (default: 2m)
#   REFRESH_PAUSE_AFTER_SECONDS (default: 180)
#   WAIT_TIMEOUT_SECONDS (default: 480)
#   WAIT_INTERVAL_SECONDS (default: 5)
#   CURL_HTTP1_ONLY (default: 1)
#   CURL_INSECURE (default: 0)
#
# Usage:
#   TOKEN="$(JWT_SIGNING_KEY='replace-with-your-hs256-secret' ./v2/server/scripts/issue-token.sh default 86400)" \
#   ./v2/server/scripts/test-lifecycle-api.sh

: "${TOKEN:?TOKEN is required}"
: "${HOST:=https://devbox-server.staging-usw-1.sealos.io}"
: "${DEVBOX_NAME:=devbox-lifecycle-test-$(date +%s)}"
: "${PAUSE_AFTER_SECONDS:=90}"
: "${ARCHIVE_AFTER_DURATION:=2m}"
: "${REFRESH_PAUSE_AFTER_SECONDS:=180}"
: "${WAIT_TIMEOUT_SECONDS:=480}"
: "${WAIT_INTERVAL_SECONDS:=5}"
: "${CURL_HTTP1_ONLY:=1}"
: "${CURL_INSECURE:=0}"

CURL_COMMON_ARGS=()
if [[ "$CURL_HTTP1_ONLY" == "1" ]]; then
  CURL_COMMON_ARGS+=(--http1.1)
fi
if [[ "$CURL_INSECURE" == "1" ]]; then
  CURL_COMMON_ARGS+=(-k)
fi

log() {
  echo "[test-lifecycle-api] $*"
}

fail() {
  echo "[test-lifecycle-api] ERROR: $*" >&2
  exit 1
}

api_json() {
  local method="$1"
  local url="$2"
  local payload="${3:-}"
  local tmp_file status body
  tmp_file="$(mktemp)"

  if [[ -n "$payload" ]]; then
    status="$(
      curl -sS -o "$tmp_file" -w "%{http_code}" \
        "${CURL_COMMON_ARGS[@]}" \
        -X "$method" "$url" \
        -H "Authorization: Bearer ${TOKEN}" \
        -H "Content-Type: application/json" \
        --data "$payload"
    )"
  else
    status="$(
      curl -sS -o "$tmp_file" -w "%{http_code}" \
        "${CURL_COMMON_ARGS[@]}" \
        -X "$method" "$url" \
        -H "Authorization: Bearer ${TOKEN}"
    )"
  fi

  body="$(cat "$tmp_file")"
  rm -f "$tmp_file"

  echo "$body"
  if [[ "$status" -ge 400 ]]; then
    return 1
  fi
}

extract_state_spec() {
  local body="$1"
  if command -v jq >/dev/null 2>&1; then
    echo "$body" | jq -r '.data.state.spec // ""'
    return
  fi
  echo "$body" | sed -n 's/.*"spec":"\([^"]*\)".*/\1/p' | head -n1
}

rfc3339_after_seconds() {
  local sec="$1"
  date -u -d "+${sec} seconds" +"%Y-%m-%dT%H:%M:%SZ" 2>/dev/null || \
    python3 - "$sec" <<'PY'
import datetime, sys
sec = int(sys.argv[1])
print((datetime.datetime.now(datetime.timezone.utc) + datetime.timedelta(seconds=sec)).strftime("%Y-%m-%dT%H:%M:%SZ"))
PY
}

wait_for_state() {
  local expected="$1"
  local timeout="$2"
  local start now elapsed info state

  start="$(date +%s)"
  while true; do
    info="$(api_json GET "${HOST}/api/v1/devbox/${DEVBOX_NAME}" || true)"
    state="$(extract_state_spec "$info")"
    log "current state=${state}, expected=${expected}"
    if [[ "$state" == "$expected" ]]; then
      return 0
    fi

    now="$(date +%s)"
    elapsed=$((now - start))
    if (( elapsed >= timeout )); then
      echo "$info"
      return 1
    fi
    sleep "$WAIT_INTERVAL_SECONDS"
  done
}

log "checking health endpoint"
health_status="$(curl -sS -o /dev/null -w "%{http_code}" "${CURL_COMMON_ARGS[@]}" "${HOST}/healthz")"
[[ "$health_status" == "200" ]] || fail "health check failed with status ${health_status}"

pause_at="$(rfc3339_after_seconds "$PAUSE_AFTER_SECONDS")"
create_payload="$(
  cat <<JSON
{
  "name": "${DEVBOX_NAME}",
  "pauseAt": "${pause_at}",
  "archiveAfterPauseTime": "${ARCHIVE_AFTER_DURATION}"
}
JSON
)"

log "creating devbox with lifecycle schedule: pauseAt=${pause_at}, archiveAfterPauseTime=${ARCHIVE_AFTER_DURATION}"
create_resp="$(api_json POST "${HOST}/api/v1/devbox" "$create_payload" || true)"
echo "$create_resp"
[[ "$create_resp" == *'"code":201'* ]] || fail "create devbox failed"

refresh_pause_at="$(rfc3339_after_seconds "$REFRESH_PAUSE_AFTER_SECONDS")"
refresh_payload="$(
  cat <<JSON
{
  "pauseAt": "${refresh_pause_at}"
}
JSON
)"
log "refreshing pauseAt to ${refresh_pause_at}"
refresh_resp="$(api_json POST "${HOST}/api/v1/devbox/${DEVBOX_NAME}/pause/refresh" "$refresh_payload" || true)"
echo "$refresh_resp"
[[ "$refresh_resp" == *'"code":200'* ]] || fail "refresh pauseAt failed"

log "waiting devbox to become Paused"
wait_for_state "Paused" "$WAIT_TIMEOUT_SECONDS" || fail "devbox did not become Paused in time"

log "waiting devbox to become Shutdown"
wait_for_state "Shutdown" "$WAIT_TIMEOUT_SECONDS" || fail "devbox did not become Shutdown in time"

log "lifecycle API checks passed"
