# Agent Instructions

## Workflow

- When calling the `Agent` tool, always specify `agent` explicitly (including `"general-purpose"`) so its configured model and thinking settings are honored.

- For focused formatting, generation, module tidying, linting, testing, and vetting, always use the mise tasks below instead of invoking the underlying tools directly.
- When adding or changing tests, reviewing test boundaries, or choosing validation scope, read [docs/testing.md](docs/testing.md).
- During iteration, use `GO_PACKAGES='...' mise run check` for focused non-race tests, lint, and vet. Add focused race-enabled tests for concurrency changes. After integrating the final changes, unset `GO_PACKAGES`, run `mise run validate`, and review any generated, formatting, or module changes before committing.
- Review the resulting diff and include intended generated or formatting changes.
- When reviewing a change, read `CODING_STANDARDS.md` before reporting findings.
- For simulator development, start worktree-local NATS with `mise run simulator-start`; stop it with `mise run simulator-stop`.
- Real-Mosquitto integration tests always require a reachable Docker daemon and fail without one.

## Commands

| Activity                       | Command                               |
| ------------------------------ | ------------------------------------- |
| Format Go files                | `mise run --skip-deps format`         |
| Regenerate checked-in code     | `mise run --skip-deps generate`       |
| Lint                           | `mise run --skip-deps lint`           |
| Tidy module metadata           | `mise run --skip-deps tidy`           |
| Focused non-race tests         | `GO_PACKAGES='...' mise run test-fast` |
| Focused tests, lint, and vet    | `GO_PACKAGES='...' mise run check`     |
| Test with the race detector    | `mise run --skip-deps test`           |
| Vet                            | `mise run --skip-deps vet`            |
| Run completion validation      | `mise run validate`                   |
| Start simulator stack          | `mise run simulator-start`           |
| Stop simulator stack           | `mise run simulator-stop`            |

Set `GO_PACKAGES` to space-separated Go package paths for focused checks, for example `GO_PACKAGES='./internal/adapters/zwavejs ./internal/app/zwavejs' mise run check`. The `lint`, `test`, and `vet` tasks also support this selection with `--skip-deps`. Focused tasks include any integration tests in those packages. Unset `GO_PACKAGES` for full validation.

## External References

| Need                           | File                   |
| ------------------------------ | ---------------------- |
| Setup and development workflow | `README.md`            |
| Canonical domain language      | `GLOSSARY.md`          |
| Architectural constraints      | `docs/architecture.md` |
| Product scope                  | `docs/product.md`      |
