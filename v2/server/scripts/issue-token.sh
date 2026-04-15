#!/usr/bin/env bash
set -euo pipefail

# Usage:
#   ./v2/server/scripts/issue-token.sh <namespace> [expires_in_seconds]
# Example:
#   JWT_SIGNING_KEY='replace-with-your-hs256-secret' ./v2/server/scripts/issue-token.sh default 86400
# Output:
#   <jwt-token>

namespace="${1:-}"
expires_in="${2:-86400}"
signing_key="${JWT_SIGNING_KEY:-}"

if [[ -z "$namespace" ]]; then
  echo "namespace is required" >&2
  exit 1
fi
if ! [[ "$namespace" =~ ^[a-z0-9]([-a-z0-9]*[a-z0-9])?$ ]]; then
  echo "namespace must match k8s DNS1123 label" >&2
  exit 1
fi
if ! [[ "$expires_in" =~ ^[1-9][0-9]*$ ]]; then
  echo "expires_in_seconds must be a positive integer" >&2
  exit 1
fi
if [[ -z "$signing_key" ]]; then
  echo "JWT_SIGNING_KEY env is required" >&2
  exit 1
fi

if ! command -v openssl >/dev/null 2>&1; then
  echo "openssl is required" >&2
  exit 1
fi

base64url() {
  openssl base64 -A | tr '+/' '-_' | tr -d '='
}

now="$(date +%s)"
exp="$((now + expires_in))"

header='{"alg":"HS256","typ":"JWT"}'
payload='{"namespace":"'"${namespace}"'","iat":'"${now}"',"exp":'"${exp}"'}'

header_b64="$(printf '%s' "$header" | base64url)"
payload_b64="$(printf '%s' "$payload" | base64url)"
unsigned_token="${header_b64}.${payload_b64}"
signature_b64="$(
  printf '%s' "$unsigned_token" \
    | openssl dgst -sha256 -hmac "$signing_key" -binary \
    | base64url
)"

printf '%s.%s\n' "$unsigned_token" "$signature_b64"
