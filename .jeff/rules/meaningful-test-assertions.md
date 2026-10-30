---
id: meaningful-test-assertions
description: Tests distinguish correct behavior from plausible defects
include: ['internal/**/*_test.go', 'sdk/**/*_test.go', 'contracts/**/*_test.go', 'entitytypes/**/*_test.go']
exclude: ['**/testdata/**', '**/dbsqlc/**', '**/*_gen_test.go']
scope: file
severity: warning
threshold: 0.90
message: Review whether the test asserts an explicit expected behavior rather than only successful execution or a self-consistent round trip.
---
# Assert meaningful expected behavior

Source: docs/testing.md, "Test boundaries" and "Fixtures and techniques";
CODING_STANDARDS.md, "Enforce production invariants through production entry points".
This is a focused restatement, not an additional testing policy.

Tests should have explicit expected results that can expose a plausible defect.
Round-trip tests also need explicit correct-value and rejection cases. Tests of
production invariants should exercise the constructor, decoder, operation, or
persistence constraint that enforces them, not only an unused validator.

## Violations visible in one file

- A behavior test invokes an operation but never checks any meaningful result,
  state change, error contract, or side effect.
- Codec tests check only encode/decode round trips, with no explicit expected
  values or rejection cases visible in the supplied file.
- A claimed invariant test checks a validator while the supplied file explicitly
  establishes that the production entry point does not enforce that invariant.

## Exceptions and uncertainty

- A successful call or absence of an error can itself be the behavior under test,
  such as accepting a valid input. Do not require arbitrary extra assertions.
- Mock interactions are valid when the interaction is the observable contract.
- Helpers, shared assertions, cohesive cross-operation tests, examples, benchmarks,
  and fixture-only files need not mirror production files or repeat assertions.
- Do not infer that a validator is unused in production from this test file alone.
- Choose uncertain when helpers or tests in other files are needed to establish
  the assertion or coverage. Missing context is not evidence of a violation.
- If the file contains only fixture support or no applicable behavior, it is compliant.
