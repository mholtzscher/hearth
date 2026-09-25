# Agent Instructions

## Workflow

- When calling the `Agent` tool, always specify `agent` explicitly (including `"general-purpose"`) so its configured model and thinking settings are honored.

- For focused formatting, generation, module tidying, linting, testing, and vetting, always use the mise tasks below instead of invoking the underlying tools directly.
- After making any change, prefer `mise run validate`; it regenerates code, formats it, and tidies module metadata before running all checks.
- Review the resulting diff and include intended generated or formatting changes.
- For simulator development, start worktree-local NATS with `mise run simulator-start`; stop it with `mise run simulator-stop`.
- Real-device validation uses operator-managed NATS and MQTT brokers; never start a local MQTT broker as a substitute.
- Real-Mosquitto integration tests always require a reachable Docker daemon and fail without one.

## Commands

| Activity                       | Command                               |
| ------------------------------ | ------------------------------------- |
| Format Go files                | `mise run --skip-deps format`         |
| Regenerate checked-in code     | `mise run --skip-deps generate`       |
| Check formatting               | `mise run --skip-deps format-check`   |
| Check generated code           | `mise run --skip-deps generate-check` |
| Lint                           | `mise run --skip-deps lint`           |
| Tidy module metadata           | `mise run --skip-deps tidy`           |
| Check module tidiness          | `mise run --skip-deps tidy-check`     |
| Test with the race detector    | `mise run --skip-deps test`           |
| Vet                            | `mise run --skip-deps vet`            |
| Run all validation (preferred) | `mise run validate`                   |
| Start simulator stack          | `mise run simulator-start`           |
| Stop simulator stack           | `mise run simulator-stop`            |

## External References

| Need                           | File                   |
| ------------------------------ | ---------------------- |
| Setup and development workflow | `README.md`            |
| Canonical domain language      | `CONTEXT.md`           |
| Architectural constraints      | `docs/architecture.md` |
| Product scope                  | `docs/product.md`      |
