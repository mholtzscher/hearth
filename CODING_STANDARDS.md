# Review standards

Use these criteria when reviewing changes. Repository checks enforce mechanical
rules; this document covers judgments about readability and responsibility.
Read the affected module's ownership documentation before recommending a change
to its organization.

## Validation boundaries

Validate external or freely constructed input at an explicit entry point. Inside
a workflow, reuse that validated result. Recheck changing facts at the operation
that depends on them.

- Name each entry point's accepted input and validation responsibility. A
  repository that accepts arbitrary domain values is an independent boundary,
  even when a service usually calls it with validated values.
- Keep structural checks separate from checks against current external state.
  Perform transactional eligibility checks against the transaction's current data.
- Share preparation results within a boundary, including normalized values,
  encoded bytes, and collected references. Add a prepared type only when its
  consumer and ownership contract justify it.
- Give every production validator a production enforcement point. Test the
  constructor, decoder, operation, or persistence constraint that actually owns
  the invariant; put assertion-only helpers in test files.
- Before removing a check, identify its input contract and the remaining
  enforcement point. Preserve independent entry-point safety and error semantics.

## Structural review

Apply this section when a change creates, renames, moves, or splits files or
packages, or adds responsibilities to an existing file.

### Establish the review scope

1. Identify the comparison base and inspect the diff, staged changes, and new
   untracked files that belong to the change. Record that scope in the review.
2. Inspect the affected directory's file inventory and the complete contents of
   changed files. Read neighboring callers, helpers, types, and tests needed to
   understand their ownership.
3. Identify generated files and their sources. Review their source ownership
   rather than recommending hand edits to generated output.

### Assess each changed handwritten file

- Describe what the file does in one sentence, based on its contents. Compare
  that description with the filename. A reader should understand the name
  without the implementation conversation; vocabulary from the domain model is
  appropriate when it describes the actual responsibility.
- Check that the declarations belong together. Identify symbols whose callers
  or rules give them a clearer owner elsewhere. Give a reason for each move,
  such as a general decoder hidden among comparisons or service configuration
  hidden among persistence contracts.
- Trace service workflows through their preparation, decision, and execution
  steps. Keep source-specific preparation discoverable with its entry point;
  shared helpers should have an identifiable responsibility of their own.
- Check that tests name the behavior they exercise and fixture-only files are
  recognizable as support code. Preserve cohesive cross-cutting tests, such as
  transport parity and shutdown ordering, even when they span production files.
- For package changes, explain the dependency boundary they enforce and check
  visibility, dependency direction, and transaction or lifecycle ownership.
  Shared filename prefixes and file length alone do not justify a package split.
- Check affected ownership documentation and navigation links against the new
  layout and behavior.

### Report and completion

Return a compact inventory of changed handwritten files with their responsibility
and a verdict: keep, rename, move declarations, or split. Group files only when
each filename is listed and the same explanation applies. Account for generated
artifacts separately through their sources.

For each recommended change, cite the file and symbols, explain the navigation
or ownership problem, and name the proposed destination or responsibility.
Distinguish required corrections from optional naming preferences.

The structural review is complete when every in-scope file is accounted for,
related workflow and test placement has been checked, and unresolved questions
are stated. Passing checks or a declaration-preservation comparison supports
correctness review; neither establishes that the resulting organization is clear.
