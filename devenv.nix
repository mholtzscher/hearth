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
    go test ./...
    go vet ./...
  '';

  enterTest = ''
    go test ./...
    go vet ./...
  '';
}
