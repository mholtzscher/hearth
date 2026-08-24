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
    test -z "$(gofmt -l $(git ls-files --cached --others --exclude-standard -- '*.go'))"
    go run ./internal/cmd/entitytypegen -root . -check
    sqlc generate
    git diff --exit-code -- internal/platform/db/sqlc
    test -z "$(git ls-files --others --exclude-standard -- internal/platform/db/sqlc)"
    go test -race ./...
    go vet ./...
  '';

  enterTest = ''
    test -z "$(gofmt -l $(git ls-files --cached --others --exclude-standard -- '*.go'))"
    go run ./internal/cmd/entitytypegen -root . -check
    sqlc generate
    git diff --exit-code -- internal/platform/db/sqlc
    test -z "$(git ls-files --others --exclude-standard -- internal/platform/db/sqlc)"
    go test -race ./...
    go vet ./...
  '';
}
