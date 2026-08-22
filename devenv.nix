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
    go run ./internal/cmd/entitytypegen -root . -check
    sqlc generate
    git diff --exit-code -- internal/platform/db/sqlc
    test -z "$(git ls-files --others --exclude-standard -- internal/platform/db/sqlc)"
    go test ./...
    go vet ./...
  '';

  enterTest = ''
    go run ./internal/cmd/entitytypegen -root . -check
    sqlc generate
    git diff --exit-code -- internal/platform/db/sqlc
    test -z "$(git ls-files --others --exclude-standard -- internal/platform/db/sqlc)"
    go test ./...
    go vet ./...
  '';
}
