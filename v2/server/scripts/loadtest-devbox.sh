#!/usr/bin/env bash
set -euo pipefail

# Devbox create->ready trend test helper.
#
# Test model:
# - create 1 devbox every second (default)
# - each devbox carries pauseAt=+5m and archiveAfterPauseTime=10m (default)
# - measure create->ready latency and ready IOPS trend while total grows
#
# Required env:
#   TOKEN
#
# Optional env:
#   HOST (default: https://devbox-server.staging-usw-1.sealos.io)
#   RUN_ID (default: UTC timestamp)
#   TOTAL (default: 5000)
#   CREATE_INTERVAL (default: 1s)
#   PAUSE_AFTER (default: 5m)
#   ARCHIVE_AFTER_PAUSE (default: 10m)
#   READY_TIMEOUT (default: 15m)
#   READY_POLL_INTERVAL (default: 3s)
#   REQUEST_TIMEOUT (default: 15s)
#   BUCKET_SIZE (default: 500)
#   OBSERVE_RUNNING_INTERVAL (default: 15s)
#   OVERALL_TIMEOUT (default: 3h)
#   REPORT_FILE (default: /tmp/devbox-create-ready-trend-<run-id>.json)
#   INSECURE_TLS (default: 0)
#   HTTP1_ONLY (default: 0)
#   INCLUDE_SAMPLES (default: 0)

: "${TOKEN:?TOKEN is required}"
: "${HOST:=https://devbox-server.staging-usw-1.sealos.io}"
: "${RUN_ID:=$(date -u +%Y%m%d-%H%M%S)}"
: "${TOTAL:=300}"
: "${CREATE_INTERVAL:=1s}"
: "${PAUSE_AFTER:=2m}"
: "${ARCHIVE_AFTER_PAUSE:=10m}"
: "${READY_TIMEOUT:=15m}"
: "${READY_POLL_INTERVAL:=3s}"
: "${REQUEST_TIMEOUT:=15s}"
: "${BUCKET_SIZE:=500}"
: "${OBSERVE_RUNNING_INTERVAL:=15s}"
: "${OVERALL_TIMEOUT:=3h}"
: "${REPORT_FILE:=/tmp/devbox-create-ready-trend-${RUN_ID}.json}"
: "${INSECURE_TLS:=0}"
: "${HTTP1_ONLY:=0}"
: "${INCLUDE_SAMPLES:=0}"

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "${SCRIPT_DIR}/../.." && pwd)"

ARGS=(
  -host "${HOST}"
  -token "${TOKEN}"
  -run-id "${RUN_ID}"
  -total "${TOTAL}"
  -create-interval "${CREATE_INTERVAL}"
  -pause-after "${PAUSE_AFTER}"
  -archive-after-pause "${ARCHIVE_AFTER_PAUSE}"
  -ready-timeout "${READY_TIMEOUT}"
  -ready-poll-interval "${READY_POLL_INTERVAL}"
  -request-timeout "${REQUEST_TIMEOUT}"
  -bucket-size "${BUCKET_SIZE}"
  -observe-running-interval "${OBSERVE_RUNNING_INTERVAL}"
  -overall-timeout "${OVERALL_TIMEOUT}"
  -report-file "${REPORT_FILE}"
)

if [[ "${INSECURE_TLS}" == "1" ]]; then
  ARGS+=(-insecure-tls)
fi
if [[ "${HTTP1_ONLY}" == "1" ]]; then
  ARGS+=(-http1-only)
fi
if [[ "${INCLUDE_SAMPLES}" == "1" ]]; then
  ARGS+=(-include-samples)
fi

echo "[loadtest-devbox] repo=${REPO_ROOT}"
echo "[loadtest-devbox] host=${HOST} run_id=${RUN_ID} total=${TOTAL} create_interval=${CREATE_INTERVAL}"

cd "${REPO_ROOT}"
go run ./v2/server/cmd/devbox-loadtest "${ARGS[@]}"
