#!/usr/bin/env bash
set -euo pipefail

if test "$#" -ne 1; then
  echo "usage: $0 <base-sha>" >&2
  exit 2
fi

changed=$(git diff --name-only "$1"...HEAD -- \
  configs \
  contracts/v1 \
  sdk/adapter \
  internal/platform/db/migrations \
  internal/platform/db/queries \
  internal/platform/nats/codec.go \
  internal/platform/nats/wire.go \
  internal/platform/nats/subjects.go \
  internal/platform/nats/command_client.go \
  internal/platform/nats/observation_consumer.go \
  internal/platform/nats/registration_server.go \
  internal/platform/nats/jetstream.go \
  internal/platform/nats/trace.go)

if test -n "$changed"; then
  echo 'preserved Device / Entity compatibility surface changed:' >&2
  echo "$changed" >&2
  exit 1
fi
