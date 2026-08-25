#!/usr/bin/env bash
set -euo pipefail

root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$root"

files=()
while IFS= read -r file; do
  files+=("$file")
done < <(
  find internal/modules/devices -path 'internal/modules/devices/nats' -prune -o -name '*.go' -type f -print
  find internal/app/hearthd -name '*.go' -type f -print
)

for file in \
  internal/modules/devices/repository.go \
  internal/modules/devices/sqlite_repository.go \
  internal/modules/devices/sqlite_observations.go; do
  if [[ -e "$file" ]]; then
    echo "obsolete Device / Entity module file remains: $file" >&2
    exit 1
  fi
done

former_symbols='\b(NewService|Repository|RegistrationRepository|CommandLedger|SQLiteRepository|NewSQLiteRepository|CommandSender|CommandRequest|RegisterBindingParams|ProjectObservationParams|ProjectObservation|ProjectionResult|CommandRecord|CommandCompletion|CommandStatus|CommandFailureCode|ObservationReceiptRetention|TypeCatalog|NewTypeCatalog|NewBuiltinTypeCatalog|EntityTypeDefinition|OperationDefinition|DefineEntityType|DefineOperation|ResolvedCommand)\b'
if rg -n "$former_symbols" "${files[@]}"; then
  echo "obsolete Device / Entity module symbol remains" >&2
  exit 1
fi

root_device_files=()
while IFS= read -r file; do
  root_device_files+=("$file")
done < <(find internal/modules/devices -maxdepth 1 -name '*.go' -type f -print)
if rg -n '^type Dependencies struct' "${root_device_files[@]}"; then
  echo "root devices.Dependencies remains" >&2
  exit 1
fi

former_test_methods='^func .*\b(RegisterBinding|GetEntityView|ProjectObservation|DeleteExpiredObservationReceipts|CreateCommand|MarkCommandAccepted|CompleteCommand|InterruptActiveCommands)\b'
if rg -n "$former_test_methods" internal/modules/devices internal/app/hearthd --glob '*_test.go' --glob '!nats/**'; then
  echo "test persistence adapter remains" >&2
  exit 1
fi
