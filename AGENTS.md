# Agent Instructions

## Workflow

- For focused formatting, generation, module tidying, linting, testing, and vetting, always use the devenv tasks below instead of invoking the underlying tools directly.
- After making any change, prefer `devenv test`; it runs `hearth:validate`, which regenerates code, formats it, and tidies module metadata before running all checks.
- Review the resulting diff and include intended generated or formatting changes.
- Use `--mode single` for a focused task so devenv does not run its validation prerequisites.

## Commands

| Activity | Command |
|---|---|
| Format Go files | `devenv tasks run --mode single hearth:format` |
| Regenerate checked-in code | `devenv tasks run --mode single hearth:generate` |
| Check formatting | `devenv tasks run --mode single hearth:format-check` |
| Check generated code | `devenv tasks run --mode single hearth:generate-check` |
| Lint | `devenv tasks run --mode single hearth:lint` |
| Tidy module metadata | `devenv tasks run --mode single hearth:tidy` |
| Check module tidiness | `devenv tasks run --mode single hearth:tidy-check` |
| Test with the race detector | `devenv tasks run --mode single hearth:test` |
| Vet | `devenv tasks run --mode single hearth:vet` |
| Run all validation (preferred) | `devenv test` |
| Run the validation task directly | `devenv tasks run hearth:validate` |

## External References

| Need | File |
|---|---|
| Setup and development workflow | `README.md` |
| Canonical domain language | `CONTEXT.md` |
| Architectural constraints | `docs/architecture.md` |
| Product scope | `docs/product.md` |
