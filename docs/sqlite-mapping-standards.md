# SQLite mapping standards

These rules supplement [the coding standards](../CODING_STANDARDS.md) for
handwritten conversions between SQLite rows or columns and domain values.
They apply whether the conversions live in a module's `sqlite` subpackage or its
root package. Generated sqlc code is outside their scope.

Apply the existing rules for
[dependency-error translation](../CODING_STANDARDS.md#translate-only-known-dependency-failures),
[error identity](../CODING_STANDARDS.md#preserve-useful-error-identity-without-wrapping-noise),
and [protocol-local error mapping](../CODING_STANDARDS.md#map-errors-at-the-owning-protocol-boundary).
Choose verification boundaries from [the testing guide](testing.md).

## Make the stored-row validation contract explicit

Identify the invariants SQL guarantees and validate the remaining domain invariants
required to construct the returned result.

**Why:** A SQL-valid row can still contain unusable domain evidence.
**Applies to:** New and changed handwritten SQLite persistence mappings. Existing
differences are scoped migration candidates, not permission for unrelated cleanup.
**Source:** stated

Reuse domain parsers and validators where appropriate. Keep summaries limited to
their selected evidence; do not fetch detail-only data for symmetry. Establish the
SQL guarantees from the selected projection and its constraints, including nullable
columns introduced by joins, rather than assuming write validation proves retained
data is usable.

Bad:

```text
Cast a retained Notification reason code into the domain without establishing
that the outcome permits it.
```

Good:

```text
SQL establishes lifecycle column shape. The decoder validates the retained
outcome/reason combination using the domain's evidence rules.
```

## Give persistence conversions resource-specific files

Place all handwritten persistence conversions in `<resource>_mapping.go`, including
operation-only mappings, while keeping primitive time and null helpers in
`time_mapping.go` and `null_mapping.go`.

**Why:** Predictable file roles make conversions discoverable across modules.
**Applies to:** New and changed handwritten SQLite persistence mappings. Existing
differences are scoped migration candidates, not permission for unrelated cleanup.
**Source:** stated

Keep related domain reconstruction together. Repository operation files retain SQL
execution, transaction orchestration, and checks against current transactional
state. The file convention creates no new package or interface boundary. Add no
empty files or per-operation packages for symmetry.

Bad:

```text
repository.go contains SQL operations, transaction orchestration, and a buried
Notification attempt row decoder.
```

Good:

```text
repository.go             # SQL operations and transactions
attempt_mapping.go        # Notification attempt row decoding and column encoding
time_mapping.go           # Primitive timestamp conversions
null_mapping.go           # Primitive SQL NULL conversions
```

## Name the row conversion consistently

Name row-to-domain functions `<resource>FromRow`, whether shared or operation-only,
using descriptive alternatives for projections and multi-input reconstruction.

**Why:** Readers can identify input and result without learning a different verb
in every module.
**Applies to:** New and changed handwritten SQLite row-to-domain conversions.
Existing differences are scoped migration candidates, not permission for unrelated
cleanup. Column encoders and primitive helpers retain names describing their
actual inputs and results rather than acquiring a `FromRow` suffix.
**Source:** stated

Bad:

```text
mapAttempt, automationRecord, and commandFromRow all decode one stored resource,
but each uses a different naming convention.
```

Good:

```text
attemptFromRow
automationRecordFromRow
commandFromRow
entityWithStateFromRow       # A specific projection
adapterInstanceFromView      # Reconstruction from a view, not one resource row
```

## Make timestamp compatibility explicit

Preserve each column's stored timestamp encoding and accepted legacy
representations, using canonical fixed-width UTC for new or changed timestamp
contracts that rely on lexical TEXT ordering.

**Why:** TEXT ordering, cursors, and historical reads depend on stored
representations.
**Applies to:** New and changed handwritten SQLite timestamp mappings. Existing
differences are scoped migration candidates, not permission for unrelated cleanup.
An edit to an existing helper does not itself authorize changing its storage
contract.
**Source:** stated

Treat storage-byte or decoder-compatibility changes as explicit migration
decisions. Review affected columns, ordering and cursor behavior, retained rows,
and migration compatibility before changing the contract. A shared helper or
consistent filename does not require a single encoding across existing columns.

Bad:

```text
Replace a timestamp helper and silently change stored bytes or stop accepting
legacy rows.
```

Good:

```text
Preserve the existing column encoding and legacy reads during mapper cleanup.
For a separately approved ordering contract change, inventory affected columns
and verify migration, chronological ordering, and cursor compatibility.
```
