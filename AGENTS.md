# Agent Instructions

## Workflow

- For focused formatting, generation, module tidying, linting, testing, and vetting, always use the mise tasks below instead of invoking the underlying tools directly.
- After making any change, prefer `mise run validate`; it regenerates code, formats it, and tidies module metadata before running all checks.
- Review the resulting diff and include intended generated or formatting changes.
- To run NATS locally (JetStream enabled) while developing: `mise run nats`.

## Commands

| Activity | Command |
|---|---|
| Format Go files | `mise run format` |
| Regenerate checked-in code | `mise run generate` |
| Check formatting | `mise run format-check` |
| Check generated code | `mise run generate-check` |
| Lint | `mise run lint` |
| Tidy module metadata | `mise run tidy` |
| Check module tidiness | `mise run tidy-check` |
| Test with the race detector | `mise run test` |
| Vet | `mise run vet` |
| Run all validation (preferred) | `mise run validate` |
| Run NATS server locally | `mise run nats` |

## External References

| Need | File |
|---|---|
| Setup and development workflow | `README.md` |
| Canonical domain language | `CONTEXT.md` |
| Architectural constraints | `docs/architecture.md` |
| Product scope | `docs/product.md` |
