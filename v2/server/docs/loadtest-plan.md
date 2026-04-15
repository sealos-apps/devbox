# Devbox Create->Ready Trend Test Plan

Updated: 2026-03-04

## Goal

Measure how `create -> ready` behaves as Devbox count grows to 5000, using a fixed create rate.

- Create rate: `1 devbox / second`
- Lifecycle policy on create:
  - `pauseAt = now + 5m`
  - `archiveAfterPauseTime = 10m`
- Expected effect: running set stays around 300 (`5m / 1s = 300`)

## Metrics

1. `ready_latency`: `ready_time - create_request_start`
2. `ready_iops`: successful create->ready cycles per second
3. Bucket trend by create sequence (default bucket size: 500)

## Buckets

- `1-500`
- `501-1000`
- ...
- `4501-5000`

Each bucket reports:

- success/failure count
- success rate
- ready IOPS
- ready latency avg/p50/p95/p99

## Command

Run with defaults:

```bash
TOKEN="<jwt>" ./v2/server/scripts/loadtest-devbox.sh
```

Direct command:

```bash
go run ./v2/server/cmd/devbox-loadtest \
  -host "https://devbox-server.staging-usw-1.sealos.io" \
  -token "<jwt>" \
  -total 5000 \
  -create-interval 1s \
  -pause-after 5m \
  -archive-after-pause 10m \
  -bucket-size 500 \
  -report-file "/tmp/devbox-create-ready-trend.json"
```

## Output

- Console:
  - overall summary (`ready_iops`, latency percentiles)
  - bucket trend summaries
  - running observation (`last/max/over_limit_hits`)
- JSON report:
  - written to `/tmp/devbox-create-ready-trend-<run-id>.json` by default

## Notes

- `create status 201` is considered a valid start for create->ready cycle.
- If create returns non-201 or ready times out, cycle is failure.
- You can set `INCLUDE_SAMPLES=1` to include per-sample detail in report.
