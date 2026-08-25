{ pkgs, ... }:

{
  packages = [
    pkgs.git
    pkgs.goose
    pkgs.nats-server
    pkgs.sqlc
  ];

  languages.go.enable = true;

  services.nats = {
    enable = true;
    jetstream.enable = true;
  };

  tasks."hearth:test".exec = ''
    go_files="$(git ls-files --cached --others --exclude-standard -- '*.go' | while read -r file; do test -f "$file" && echo "$file"; done)"
    test -z "$(gofmt -l $go_files)"
    go run ./internal/cmd/entitytypegen -root . -check
    scripts/check-device-module-shape.sh
    sqlc generate
    git diff --exit-code -- internal/platform/db/sqlc
    test -z "$(git ls-files --others --exclude-standard -- internal/platform/db/sqlc)"
    go test -race ./...
    go vet ./...
  '';

  enterTest = ''
    go_files="$(git ls-files --cached --others --exclude-standard -- '*.go' | while read -r file; do test -f "$file" && echo "$file"; done)"
    test -z "$(gofmt -l $go_files)"
    go run ./internal/cmd/entitytypegen -root . -check
    scripts/check-device-module-shape.sh
    sqlc generate
    git diff --exit-code -- internal/platform/db/sqlc
    test -z "$(git ls-files --others --exclude-standard -- internal/platform/db/sqlc)"
    go test -race ./...
    go vet ./...
  '';
}
