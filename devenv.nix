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
    sqlc generate
    git diff --exit-code -- internal/platform/db/sqlc
    go test ./...
    go vet ./...
  '';

  enterTest = ''
    sqlc generate
    git diff --exit-code -- internal/platform/db/sqlc
    go test ./...
    go vet ./...
  '';
}
