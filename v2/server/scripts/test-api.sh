#!/usr/bin/env bash
set -euo pipefail

# End-to-end API smoke test script using curl.
# This script does NOT destroy created devbox.
#
# Required env:
#   TOKEN (JWT, with namespace claim)
#
# Optional env:
#   HOST (default: http://127.0.0.1:8090)
#   DEVBOX_NAME (default: devbox-api-test-<timestamp>)
#   WAIT_RETRY (default: 90)
#   WAIT_INTERVAL (default: 2)
#   REMOTE_FILE_PATH (default: /home/devbox/workspace/devbox-api-upload.txt)
#   UPLOAD_LOCAL_FILE (default: /tmp/<devbox>-upload.txt)
#   DOWNLOAD_LOCAL_FILE (default: /tmp/<devbox>-download.txt)
#   STEP_MODE (default: 0; set 1 to pause before each step)
#   CURL_HTTP1_ONLY (default: 1; force curl to HTTP/1.1)
#   CURL_INSECURE (default: 0; set 1 to pass -k for self-signed/invalid TLS)
#
# Usage:
#   TOKEN="$(JWT_SIGNING_KEY='replace-with-your-hs256-secret' ./v2/server/scripts/issue-token.sh default 86400)" ./v2/server/scripts/test-api.sh

: "${TOKEN:?TOKEN is required}"
: "${HOST:=https://fastgpt-sandbox-server.hzh.sealos.run}"
: "${DEVBOX_NAME:=devbox-api-test-$(date +%s)}"
: "${WAIT_RETRY:=90}"
: "${WAIT_INTERVAL:=2}"
: "${REMOTE_FILE_PATH:=/home/devbox/workspace/devbox-api-upload.txt}"
: "${UPLOAD_LOCAL_FILE:=/tmp/${DEVBOX_NAME}-upload.txt}"
: "${DOWNLOAD_LOCAL_FILE:=/tmp/${DEVBOX_NAME}-download.txt}"
: "${STEP_MODE:=0}"
: "${CURL_HTTP1_ONLY:=1}"
: "${CURL_INSECURE:=0}"

HAS_JQ=0
if command -v jq >/dev/null 2>&1; then
  HAS_JQ=1
fi
API_LAST_STATUS=0
API_LAST_CURL_ERROR=""

CURL_COMMON_ARGS=()
if [[ "$CURL_HTTP1_ONLY" == "1" ]]; then
  CURL_COMMON_ARGS+=(--http1.1)
fi
if [[ "$CURL_INSECURE" == "1" ]]; then
  CURL_COMMON_ARGS+=(-k)
fi

log() {
  echo "[test-api] $*"
}

fail() {
  echo "[test-api] ERROR: $*" >&2
  exit 1
}

wait_for_continue() {
  if [[ "$STEP_MODE" != "1" ]]; then
    return
  fi

  if [[ ! -t 0 ]]; then
    fail "STEP_MODE=1 requires interactive terminal input"
  fi

  while true; do
    read -r -p "[test-api] 输入 继续 (或 continue) 以执行下一步: " answer
    answer="${answer// /}"
    if [[ "$answer" == "继续" || "$answer" == "continue" || "$answer" == "Continue" ]]; then
      break
    fi
    log "输入无效，请输入: 继续 或 continue"
  done
}

step() {
  log "$1"
  wait_for_continue
}

api_json() {
  local method="$1"
  local url="$2"
  local payload="${3:-}"
  local tmp_file err_file status body curl_rc
  tmp_file="$(mktemp)"
  err_file="$(mktemp)"
  status="0"
  curl_rc=0

  set +e
  if [[ -n "$payload" ]]; then
    status="$(
      curl -sS -o "$tmp_file" -w "%{http_code}" \
        "${CURL_COMMON_ARGS[@]}" \
        -X "$method" "$url" \
        -H "Authorization: Bearer ${TOKEN}" \
        -H "Content-Type: application/json" \
        --data "$payload" \
        2>"$err_file"
    )"
    curl_rc=$?
  else
    status="$(
      curl -sS -o "$tmp_file" -w "%{http_code}" \
        "${CURL_COMMON_ARGS[@]}" \
        -X "$method" "$url" \
        -H "Authorization: Bearer ${TOKEN}" \
        2>"$err_file"
    )"
    curl_rc=$?
  fi
  set -e

  body="$(cat "$tmp_file" 2>/dev/null || true)"
  API_LAST_CURL_ERROR="$(cat "$err_file" 2>/dev/null || true)"
  rm -f "$tmp_file"
  rm -f "$err_file"
  API_LAST_STATUS="$status"

  echo "$body"

  if [[ "$curl_rc" -ne 0 ]]; then
    return 1
  fi

  if [[ "$status" -ge 400 ]]; then
    return 1
  fi

  if [[ "$HAS_JQ" -eq 1 ]]; then
    local code
    code="$(echo "$body" | jq -r '.code // 0' 2>/dev/null || echo 0)"
    if [[ "$code" -ge 400 ]]; then
      return 1
    fi
  fi
}

api_binary_download() {
  local url="$1"
  local output="$2"
  curl -sS -o "$output" -w "%{http_code}" \
    "${CURL_COMMON_ARGS[@]}" \
    -X GET "$url" \
    -H "Authorization: Bearer ${TOKEN}"
}

step "checking health endpoint"
health_status="$(curl -sS -o /dev/null -w "%{http_code}" "${CURL_COMMON_ARGS[@]}" "${HOST}/healthz")"
[[ "$health_status" == "200" ]] || fail "health check failed with status ${health_status}"

create_payload="$(
  cat <<JSON
{
  "name": "${DEVBOX_NAME}"
}
JSON
)"

step "creating devbox: ${DEVBOX_NAME}"
create_url="${HOST}/api/v1/devbox"
create_resp="$(api_json POST "$create_url" "$create_payload" || true)"
echo "$create_resp"
if [[ "$create_resp" != *'"code":201'* ]]; then
  fail "create devbox failed"
fi

step "fetching devbox info"
info_url="${HOST}/api/v1/devbox/${DEVBOX_NAME}"
info_resp="$(api_json GET "$info_url" || true)"
echo "$info_resp"
if [[ "$info_resp" != *'"code":200'* || "$info_resp" != *'"privateKeyBase64"'* ]]; then
  fail "devbox info API failed"
fi

step "waiting for devbox pod to become ready for exec"
exec_url="${HOST}/api/v1/devbox/${DEVBOX_NAME}/exec"
ready=0
for ((i = 1; i <= WAIT_RETRY; i++)); do
  ready_resp="$(api_json POST "$exec_url" '{"command":["sh","-lc","echo ready"]}' || true)"
  if [[ "$ready_resp" == *'"code":200'* && "$ready_resp" == *'"exitCode":0'* ]]; then
    ready=1
    break
  fi
  sleep "$WAIT_INTERVAL"
done
[[ "$ready" -eq 1 ]] || fail "devbox is not ready for exec in time"

run_exec_case() {
  local case_name="$1"
  local payload="$2"
  local expected_fragment="${3:-}"
  local resp stdout_text attempt success

  log "exec case: ${case_name}"
  success=0
  for attempt in 1 2; do
    resp="$(api_json POST "$exec_url" "$payload" || true)"
    echo "$resp"
    if [[ "$resp" == *'"code":200'* && "$resp" == *'"exitCode":0'* ]]; then
      success=1
      break
    fi
    if [[ "$attempt" -lt 2 ]]; then
      log "exec case retry once: ${case_name} (http_status=${API_LAST_STATUS})"
      sleep 1
    fi
  done

  if [[ "$success" -ne 1 ]]; then
    fail "exec case failed: ${case_name}, http_status=${API_LAST_STATUS}, curl_error=${API_LAST_CURL_ERROR}, response=${resp}"
  fi
  if [[ -n "$expected_fragment" && "$resp" != *"$expected_fragment"* ]]; then
    if [[ "$HAS_JQ" -eq 1 ]]; then
      stdout_text="$(echo "$resp" | jq -r '.data.stdout // ""' 2>/dev/null || true)"
      if [[ "$stdout_text" == *"$expected_fragment"* ]]; then
        return
      fi
      fail "exec case stdout check failed: ${case_name}, expected to contain: ${expected_fragment}, got stdout: ${stdout_text}"
    fi
    fail "exec case stdout check failed: ${case_name}, expected to contain: ${expected_fragment}"
  fi
}

step "testing exec API with multiple command examples"
run_exec_case \
  "simple echo" \
  '{"command":["sh","-lc","echo hello-from-devbox"]}' \
  'hello-from-devbox'

run_exec_case \
  "cwd and pwd" \
  '{"command":["sh","-lc","pwd"],"cwd":"/home/devbox/workspace"}' \
  '/home/devbox/workspace'

run_exec_case \
  "pipeline grep wc" \
  '{"command":["sh","-lc","{ echo alpha; echo beta; echo gamma; } | grep -E '\''a$'\'' | wc -l"]}' \
  '3'

run_exec_case \
  "redirect overwrite >" \
  '{"command":["sh","-lc","rm -f /home/devbox/workspace/.redir.txt; echo first-line > /home/devbox/workspace/.redir.txt; cat /home/devbox/workspace/.redir.txt; rm -f /home/devbox/workspace/.redir.txt"]}' \
  'first-line'

run_exec_case \
  "echo js and redirect to hello.js" \
  '{"command":["sh","-lc","rm -f /home/devbox/workspace/hello.js; echo \"console.log(\\\"Hello, World!\\\");\" > /home/devbox/workspace/hello.js; cat /home/devbox/workspace/hello.js; rm -f /home/devbox/workspace/hello.js"]}' \
  'console.log("Hello, World!");'

run_exec_case \
  "redirect append >>" \
  '{"command":["sh","-lc","rm -f /home/devbox/workspace/.redir.txt; echo line-1 > /home/devbox/workspace/.redir.txt; echo line-2 >> /home/devbox/workspace/.redir.txt; tail -n 1 /home/devbox/workspace/.redir.txt; rm -f /home/devbox/workspace/.redir.txt"]}' \
  'line-2'

run_exec_case \
  "stderr redirect 2>" \
  '{"command":["sh","-lc","rm -f /home/devbox/workspace/.redir.err; (echo std-out; echo std-err 1>&2) 2> /home/devbox/workspace/.redir.err; cat /home/devbox/workspace/.redir.err; rm -f /home/devbox/workspace/.redir.err"]}' \
  'std-err'

run_exec_case \
  "redirect to /dev/null" \
  '{"command":["sh","-lc","echo hidden >/dev/null 2>&1; echo visible-after-null-redir"]}' \
  'visible-after-null-redir'

run_exec_case \
  "redirect append and tail" \
  '{"command":["sh","-lc","tmp=/home/devbox/workspace/.api-smoke.txt; rm -f /home/devbox/workspace/.api-smoke.txt; printf '\''line-1\nline-2\nline-3\n'\'' > /home/devbox/workspace/.api-smoke.txt; tail -n 1 /home/devbox/workspace/.api-smoke.txt; rm -f /home/devbox/workspace/.api-smoke.txt"]}' \
  'line-3'

run_exec_case \
  "logical fallback" \
  '{"command":["sh","-lc","false || echo fallback-ok"]}' \
  'fallback-ok'

run_exec_case \
  "stdin passthrough" \
  '{"command":["cat"],"stdin":"line-a\nline-b\n"}' \
  'line-a'

step "preparing upload test file"
cat >"$UPLOAD_LOCAL_FILE" <<EOF
hello devbox api
ts=$(date -u +"%Y-%m-%dT%H:%M:%SZ")
EOF

upload_url="${HOST}/api/v1/devbox/${DEVBOX_NAME}/files/upload?path=${REMOTE_FILE_PATH}&mode=0644"
step "testing upload API: ${upload_url}"
upload_tmp="$(mktemp)"
upload_status="$(
  curl -sS -o "$upload_tmp" -w "%{http_code}" \
    "${CURL_COMMON_ARGS[@]}" \
    -X POST "$upload_url" \
    -H "Authorization: Bearer ${TOKEN}" \
    -H "Content-Type: application/octet-stream" \
    --data-binary @"$UPLOAD_LOCAL_FILE"
)"
upload_resp="$(cat "$upload_tmp")"
rm -f "$upload_tmp"
echo "$upload_resp"
if [[ "$upload_status" -ge 400 || "$upload_resp" != *'"code":200'* ]]; then
  fail "upload API failed"
fi

download_url="${HOST}/api/v1/devbox/${DEVBOX_NAME}/files/download?path=${REMOTE_FILE_PATH}"
step "testing download API: ${download_url}"
download_status="$(api_binary_download "$download_url" "$DOWNLOAD_LOCAL_FILE")"
if [[ "$download_status" != "200" ]]; then
  echo "download status=${download_status}"
  cat "$DOWNLOAD_LOCAL_FILE" || true
  fail "download API failed"
fi

if ! cmp -s "$UPLOAD_LOCAL_FILE" "$DOWNLOAD_LOCAL_FILE"; then
  fail "downloaded file content mismatch"
fi
log "upload/download file content matched"

log "all API checks passed"
