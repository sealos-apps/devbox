---
name: devbox-api-helper
description: Help external clients call, validate, and troubleshoot the Devbox REST API over HTTP with JWT authentication. Use when users need ready-to-run curl examples for devbox lifecycle, info, exec, file upload/download, SSH info retrieval, or API error diagnosis.
---

# Devbox Api Helper

## Overview

Use this skill to provide client-facing API usage guidance and executable curl commands for Devbox operations.

## Load Reference

1. Read `references/endpoint-cheatsheet.md` first.
2. Treat this skill as client-side usage guidance.
3. If the user provides a newer API contract, follow the user-provided contract.

## Session Bootstrap

1. When this skill is activated, first ask the user to provide `DEVBOX_SERVER_HOST` and `DEVBOX_SERVER_TOKEN`.
2. If either value is missing, ask for it before generating API curl commands.
3. After the user provides values, show examples using those values (or clearly marked placeholders if redaction is needed).

## Apply Core Constraints

1. Use API prefix `/api/v1/devbox`.
2. Do not add `namespace` query/body fields for business APIs.
3. Treat namespace as JWT claim only (`namespace` in token payload).
4. Keep `Authorization: Bearer <JWT>` on all business endpoints.
5. Treat `GET /api/v1/devbox/{name}/files/download` as binary response, not JSON.

## Execute Common Workflows

1. Initialize shell vars (`DEVBOX_SERVER_HOST`, `DEVBOX_SERVER_TOKEN`, `DEVBOX_NAME`) before giving curl examples.
2. For lifecycle: use create, info, pause, resume, destroy endpoints.
3. For command execution: call `POST /exec` with non-empty `command` array and valid timeout.
4. For file transfer: call upload/download with required `path`.
5. For SSH access: call info endpoint, read `data.ssh.*`, decode `privateKeyBase64`, then run ssh command.

## Troubleshoot Predictably

1. Map failures by HTTP code:
`400` invalid params, `401` token invalid, `404` resource missing, `409` state conflict, `500` server error, `504` timeout.
2. If gateway/TLS/HTTP2 issues appear with curl, retry using `--http1.1` and optionally `-k` in non-production tests.
3. For `exec`/file failures, verify Devbox pod is running before retrying.

## Output Style

1. Give executable curl commands with concrete placeholders.
2. Keep examples aligned with current API doc fields.
3. When returning info examples, include:
`creationTimestamp`, `deletionTimestamp`, `state`, and `ssh` fields.
4. Do not expose local machine paths in output.
