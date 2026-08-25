#!/usr/bin/env bash
set -euo pipefail

files=$(find internal/modules/devices internal/app/hearthd -name '*.go' \
  ! -path 'internal/modules/devices/nats/*' -print)
former='\b(NewService|Repository|RegistrationRepository|CommandLedger|SQLiteRepository|NewSQLiteRepository|CommandSender|CommandRequest|RegisterBindingParams|ProjectObservationParams|ProjectObservation|ProjectionResult|CommandRecord|CommandCompletion|CommandStatus|CommandFailureCode|ObservationReceiptRetention|TypeCatalog|NewTypeCatalog|NewBuiltinTypeCatalog|EntityTypeDefinition|OperationDefinition|DefineEntityType|DefineOperation|ResolvedCommand|devices\.Dependencies)\b'

if grep -En "$former" $files; then
  echo 'former Device / Entity module seam remains' >&2
  exit 1
fi

if grep -En '^type Dependencies\b' internal/modules/devices/*.go; then
  echo 'root devices.Dependencies remains' >&2
  exit 1
fi

for path in \
  internal/modules/devices/repository.go \
  internal/modules/devices/sqlite_repository.go \
  internal/modules/devices/sqlite_observations.go
do
  if test -e "$path"; then
    echo "obsolete file remains: $path" >&2
    exit 1
  fi
done
