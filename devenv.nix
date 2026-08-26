{ pkgs, ... }:

{
  env.GOMODCACHE = "${builtins.getEnv "HOME"}/go/pkg/mod";

  packages = [
    pkgs.git
    pkgs.golangci-lint
    pkgs.goose
    pkgs.nats-server
    pkgs.ko
    pkgs.sqlc
  ];

  languages.go.enable = true;

  services.nats = {
    enable = true;
    jetstream.enable = true;
  };

  tasks."hearth:format" = {
    after = [ "hearth:generate" ];
    exec = ''
      git ls-files -z --cached --others --exclude-standard -- '*.go' | xargs -0 -r gofmt -w
    '';
  };

  tasks."hearth:format-check" = {
    after = [ "hearth:tidy" ];
    exec = ''
      unformatted="$(git ls-files -z --cached --others --exclude-standard -- '*.go' | xargs -0 -r gofmt -l)"
      if test -n "$unformatted"; then
        printf 'The following Go files need formatting:\n%s\n' "$unformatted"
        exit 1
      fi
    '';
  };

  tasks."hearth:generate".exec = ''
    go generate ./entitytypes
    sqlc generate
  '';

  tasks."hearth:generate-check" = {
    after = [ "hearth:tidy" ];
    exec = ''
      go run ./internal/cmd/entitytypegen -root . -check

      tmp="$(mktemp -d)"
      trap 'rm -rf "$tmp"' EXIT
      mkdir -p "$tmp/internal/platform/db"
      cp sqlc.yaml "$tmp/"
      cp -R internal/platform/db/migrations internal/platform/db/queries internal/platform/db/sqlc "$tmp/internal/platform/db/"
      (cd "$tmp" && sqlc generate)
      diff -ru internal/platform/db/sqlc "$tmp/internal/platform/db/sqlc"
    '';
  };

  tasks."hearth:ko-build" = {
    after = [ "hearth:tidy" ];
    exec = ''
      KO_DOCKER_REPO=example.invalid/hearth ko build --base-import-paths --tags validation --push=false \
        ./cmd/hearthd ./cmd/hearth-adapter-homeassistant ./cmd/hearth-simulator
    '';
  };

  tasks."hearth:lint" = {
    after = [ "hearth:tidy" ];
    exec = ''
      golangci-lint run ./...
    '';
  };

  tasks."hearth:tidy" = {
    after = [ "hearth:format" ];
    exec = ''
      go mod tidy
    '';
  };

  tasks."hearth:tidy-check" = {
    after = [ "hearth:tidy" ];
    exec = ''
      go mod tidy -diff
    '';
  };

  tasks."hearth:test" = {
    after = [ "hearth:tidy" ];
    exec = ''
      go test -race ./...
    '';
  };

  tasks."hearth:vet" = {
    after = [ "hearth:tidy" ];
    exec = ''
      go vet ./...
    '';
  };

  tasks."hearth:validate".after = [
    "hearth:format-check"
    "hearth:generate-check"
    # "hearth:lint"
    "hearth:tidy-check"
    "hearth:test"
    "hearth:vet"
  ];

  enterTest = ''
    devenv tasks run --no-reload hearth:validate
  '';
}
