---
id: known-dependency-error-translation
description: Translate established dependency failures without blaming arbitrary input for operational faults
include: ['internal/modules/**/*.go']
exclude: ['**/*_test.go', '**/*_gen.go', '**/dbsqlc/**', '**/testdata/**']
scope: file
severity: warning
threshold: 0.90
message: Review whether a broad dependency-error translation misclassifies operational failures as invalid input or another unsupported domain class.
---
# Translate only known dependency failures

Source: CODING_STANDARDS.md, "Translate only known dependency failures" and
"Preserve useful error identity without wrapping noise".
This is a focused restatement, not a new error taxonomy.

Translate a dependency failure into a domain class only when its identity and
meaning establish that class. Unexpected operational failures must remain internal
failures rather than blaming valid caller input. Preserve error identities and
private causes needed for downstream decisions or diagnostics.

## Violations visible in one file

- Every error from a dependency lookup becomes invalid input, a missing resource,
  or an unsupported reference without distinguishing the known dependency class.
- A visible multi-source execution path labels every graph, persistence, or tool
  fault as a model-provider failure, even though the failure source is not established.
- A translation collapses an operational failure into a caller-repairable class
  merely because the operation was processing caller-supplied input.

## Exceptions and uncertainty

- Translating an established not-found or constraint identity is valid. Unknown
  failures may propagate unchanged or acquire useful operation context.
- Protocol boundaries may intentionally return fixed safe client text while
  retaining the private cause for their diagnostic owner.
- Documented conversion into a durable domain outcome may intentionally consume
  the original transport cause. Do not demand raw causes in public responses.
- Do not require wrapping every pass-through error or invent speculative classes.
- Choose uncertain when dependency contracts, helpers, or an approved outcome
  policy outside this file are needed to decide whether the translation is valid.
- Files without applicable dependency-error translations are compliant.
