# Review standards

Use these criteria when reviewing changes. Repository checks enforce mechanical
rules; this document covers judgments about readability and responsibility.
Read the affected module's ownership documentation before recommending a change
to its organization.

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
