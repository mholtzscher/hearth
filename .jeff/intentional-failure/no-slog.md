---
id: no-slog
description: Experimental prohibition of log/slog for intentional-failure checks
include: ['**/*.go']
exclude: ['**/*_test.go', '**/*_gen.go', '**/dbsqlc/**', '**/testdata/**', '**/vendor/**']
scope: file
severity: error
threshold: 0.90
message: Experimental no-slog rule violated; this file imports or uses the standard-library log/slog package.
---
# Do not use log/slog

This is an isolated intentional-failure policy, not a Hearth coding standard.
Hearth's actual logging conventions require slog; this rule deliberately opposes
that convention to exercise blocking findings on existing production code.

Production Go files must not import or use the standard-library `log/slog` package.

## Violations

- Importing `log/slog`, including aliased, dot, or blank imports.
- Using types, functions, constants, or methods established by this file as coming
  from `log/slog`, such as `*slog.Logger`, `slog.String`, or logger.ErrorContext.

## Non-violations and uncertainty

- Mentions in comments or ordinary string literals are not package imports or usage.
- An unrelated identifier named slog is not evidence of using `log/slog`.
- A method name such as ErrorContext alone does not establish its receiver's package.
- Choose compliant when the file contains no log/slog import or established usage.
- Choose uncertain when establishing a receiver's origin requires missing context.
