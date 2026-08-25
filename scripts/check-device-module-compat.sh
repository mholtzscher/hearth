#!/usr/bin/env bash
set -euo pipefail

if [[ $# -ne 1 || -z "$1" ]]; then
  echo "usage: $0 <base-sha>" >&2
  exit 2
fi

root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$root"
base="$1"

guarded_platform_nats='^(internal/platform/nats/(codec|wire|subjects|command_client|observation_consumer|registration_server|jetstream|trace)\.go)$'
failed=0
while IFS= read -r path; do
  case "$path" in
    configs/*|contracts/v1/*|sdk/adapter/*|internal/platform/db/migrations/*|internal/platform/db/queries/*)
      echo "preserved surface changed: $path" >&2
      failed=1
      ;;
    *)
      if [[ "$path" =~ $guarded_platform_nats ]]; then
        echo "preserved platform NATS surface changed: $path" >&2
        failed=1
      fi
      ;;
  esac
done < <(git diff --name-only "$base"...HEAD)

exit "$failed"
