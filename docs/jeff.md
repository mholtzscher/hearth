# Advisory semantic review pilot

[Jeff](https://github.com/mholtzscher/jeff) supplements human review with
file-level semantic checks. It does not replace `mise run validate`, ownership
documentation, or the [coding standards](../CODING_STANDARDS.md). The standards
remain authoritative; these rules are deliberately narrow restatements.

## Pilot scope

| Rule | Scope | Standard |
| --- | --- | --- |
| `meaningful-test-assertions` | Go tests under internal, SDK, contracts, and Entity types | [Explicit outcomes and meaningful assertions](testing.md#fixtures-and-techniques) |
| `protocol-neutral-domain-errors` | Module-root production Go files | [Protocol-local error policy](../CODING_STANDARDS.md#map-errors-at-the-owning-protocol-boundary) |
| `known-dependency-error-translation` | Module production Go files | [Known dependency failures](../CODING_STANDARDS.md#translate-only-known-dependency-failures) |

All three are warnings at threshold 0.90; uncertainty is reported, not blocking.
Thresholds are initial settings, not calibrated accuracy claims. Generated sqlc,
conventional `*_gen.go`/`*_gen_test.go` files, and testdata are excluded. Inspect
the selected files for other generated code before evaluating them.

The CLI is pinned to 1.0.0 using task-local Mise tools; the model is pinned to
`jev-1.13.0` in `.jeff/config.yaml`. The private GitHub release requires
authenticated repository access for a fresh installation. The pilot is not wired
into ordinary installation, validation, CI, or automatic PR comments.

## Preview first: no provider calls

From the repository root:

```sh
mise run jeff -- version
mise run jeff-validate
mise run jeff -- rules list
mise run jeff-preview -- --staged
# Or preview the branch's changed tracked files:
mise run jeff-preview -- --base origin/main
# Explicit paths also select new untracked files:
mise run jeff-preview -- internal/modules/notifications/httpapi/errors_test.go
```

`jeff-preview` always adds `--dry-run`. Inspect its planned file/rule pairs,
skipped files, and unevaluated inputs before approving a live run with the same
selection. Dry runs need no API key and never call the classifier. Exit 0 means
the preview completed, not that files comply; dry runs never exit 1 for policy
violations. They can still exit 2 for local processing errors. A preview does not
prove model availability or quality, or that the provider will accept a request.
With no selection arguments, preview and `check` select all tracked files.

### Migration from the earlier pilot

The pilot now uses `.jeff/`, `docs/jeff.md`, and the `jeff`, `jeff-validate`, and
`jeff-preview` Mise tasks. Jeff does not discover the old `.rulelint/` directory.
The released CLI removed `--allow-upload`: use `--dry-run` to preview, or omit it
only after approving source transmission. It reads `TYPESAFE_API_KEY` natively;
the previous task's credential-name translation is no longer needed. Historical
reports under `.data/rulelint/` remain unchanged; new reports use `.data/jeff/`.

## Live review: explicit source transmission

**`check` uploads complete selected source files and matching rule bodies to
TypeSafe by default**, even with `--base` or `--staged`. Approve the selected
source and endpoint before omitting `--dry-run`; a preview is not upload approval.
The default endpoint is `https://api.typesafe.ai`; `JEV_BASE_URL` can override it.
Supply `TYPESAFE_API_KEY` through your secret manager/environment, never committed
configuration or CLI arguments. Jeff reads this variable directly.
Calls may incur provider charges.

After approval, with the key already set:

```sh
mkdir -p .data/jeff
mise run jeff -- check --format json internal/modules/notifications/httpapi/errors_test.go > .data/jeff/sample.json
# Staged review:
mise run jeff -- check --staged --format json > .data/jeff/staged.json
# Alternative: branch review, not combined with --staged:
mise run jeff -- check --base origin/main --format json > .data/jeff/branch.json
```

`--staged` evaluates index blobs. `--base` selects changes against the merge base
but evaluates current working-tree content, including uncommitted edits. Neither
selects untracked files; explicit paths with either flag intersect its changed
set rather than adding files. Rules/configuration come from the working tree.
Keep reports under ignored `.data/jeff/` and record the CLI/model, base and
head revisions, source state, and rule revision with each assessment.

Exit 0 can include warnings, uncertainty, and unevaluated files; it is not a clean
bill of health. Exit 1 means blocking policy findings (not expected with this
pilot configuration); exit 2 means processing/provider failure, not compliance.
Do not hide failures with `|| true`.

## Initial live smoke check

Before migration, the approved two-file check completed with the predecessor CLI
0.2.0 and returned model `jev-1.13.0` at `https://api.typesafe.ai`, using
`TYPESAFE_API_KEY` through the task.
All three file/rule pairs were evaluated as compliant; there were no warnings,
uncertain results, unevaluated pairs, or processing errors:

- `internal/modules/notifications/httpapi/errors_test.go`: meaningful assertions
  against HTTP status, diagnostic ownership, and private-cause leakage.
- `internal/modules/automations/definition_reference_validation.go`: known
  dependency identities are translated while unexpected failures retain identity.
  The domain-error rule also passed, but this file declares no domain error types;
  that is a non-applicable case, not evidence of detecting a domain-policy defect.

Human inspection agreed with these labels. The command took 0.72 seconds including
Mise overhead; provider usage/cost was not exposed in the report. Raw results and
metadata (HEAD and source/rule/config hashes) are kept locally under
`.data/rulelint/20261030T034825Z-{sample,metadata}.json`, not committed. This is an
execution smoke test only: there were no known violations in the approved sample,
so it establishes neither defect-detection accuracy nor false-negative rates.

## Assess the pilot before expanding it

### Intentional blocking failure

An isolated synthetic negative control lives in `.jeff/intentional-failure/`.
Its `intentional-failure` error-level rule requires `pilot_status: pass`; the
fixture deliberately says `pilot_status: fail`. The same isolated configuration
also loads `no-slog`, an error-level rule rejecting `log/slog` imports and usage
in handwritten production Go files. Both are excluded from the default rules
glob and do not change ordinary pilot results or repository validation.

`no-slog` deliberately contradicts [Hearth's logging conventions](logging.md#shared-conventions),
which require slog. It is a failure-reporting experiment, not authorization to
remove or replace production logging. Aliased imports count; comments, ordinary
strings, and unrelated identifiers do not.

Validate its configuration offline:

```sh
mise run jeff -- rules validate --config .jeff/intentional-failure/config.yaml
mise run jeff-preview -- --config .jeff/intentional-failure/config.yaml .jeff/intentional-failure/fixture.txt
```

After approving transmission of this synthetic fixture and its rule, evaluate it
with the existing `TYPESAFE_API_KEY`:

```sh
mise run jeff -- check --config .jeff/intentional-failure/config.yaml .jeff/intentional-failure/fixture.txt
```

Expected for the live check: one blocking violation and exit 1. The dry run only
plans the pair and exits 0; it does not evaluate the intentional failure.
A model pass or uncertainty is an unexpected negative-control result; exit 2 is a
processing failure. This checks
the blocking-result path, not the accuracy of the three real review rules.

Preview `no-slog` first:

```sh
mise run jeff-preview -- --config .jeff/intentional-failure/config.yaml internal/modules/devices/httpapi/errors.go
```

Then, after approving transmission of the selected source and matching rule:

```sh
mise run jeff -- check --config .jeff/intentional-failure/config.yaml internal/modules/devices/httpapi/errors.go
```

This file imports `log/slog`, so the expected result is one blocking `no-slog`
violation and exit 1. The synthetic status rule does not match this Go file.

### Representative PRs

Try five representative PRs, retaining the existing human review process:

1. Record applicable human findings independently before reading Jeff output.
2. Preview the exact selection, approve transmission, and save the live JSON.
3. Inspect every warning, uncertain result, and unevaluated pair. For each finding,
   record file, rule, relevant symbol, standards citation, and whether it concerns
   changed code or pre-existing code elsewhere in the selected file.
4. Label results as actionable true positives, false positives, uncertain, or
   pre-existing/out-of-scope. Compare with human findings to record missed defects
   (false negatives); passes alone cannot establish recall. Record runtime and
   provider usage/cost where available.
5. Summarize counts per rule and reviewer time saved or added. Narrow or remove
   noisy rules before adding more. Any blocking policy or automated upload/CI
   integration needs a separate decision supported by these observations.

Jeff receives one file at a time, not neighboring callers, linked documents,
the PR discussion, or diff context. Links in rules are provenance, not extra
context loaded by the classifier. Rules therefore include their operative criteria
and ask for uncertainty when context is missing. It returns file/rule labels and
probabilities, not verified explanations or line numbers. A reviewer must establish
the actual violation before requesting a change; do not invent source locations
or treat a probability as proof.

Cross-file ownership, transaction guarantees, shutdown ordering, API/schema
agreement, and the complete structural-review inventory remain human-review
responsibilities. Existing differences are not authorization for unrelated cleanup.
When standards change, update the affected rule in the same change and rerun
`mise run jeff-validate` and a matching selection preview.
