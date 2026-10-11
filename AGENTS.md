# Agent Instructions

## Workflow

- When calling the `Agent` tool, always specify `agent` explicitly (including `"general-purpose"`) so its configured model and thinking settings are honored.

- For focused formatting, generation, module tidying, linting, testing, and vetting, always use the mise tasks below instead of invoking the underlying tools directly.
- During iteration, use the relevant focused mise tasks for feedback. After integrating the final changes, run `mise run validate` and review any generated, formatting, or module changes before committing.
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
| Test with the race detector    | `mise run --skip-deps test`           |
| Vet                            | `mise run --skip-deps vet`            |
| Run all validation (preferred) | `mise run validate`                   |
| Start simulator stack          | `mise run simulator-start`           |
| Stop simulator stack           | `mise run simulator-stop`            |

For package-scoped iteration, set `GO_PACKAGES` to space-separated Go package paths when running `lint`, `test`, or `vet` with `--skip-deps`, for example `GO_PACKAGES='./internal/adapters/zwavejs ./internal/app/zwavejs' mise run --skip-deps test`. Unset it for full validation.

## External References

| Need                           | File                   |
| ------------------------------ | ---------------------- |
| Setup and development workflow | `README.md`            |
| Canonical domain language      | `GLOSSARY.md`          |
| Architectural constraints      | `docs/architecture.md` |
| Product scope                  | `docs/product.md`      |
