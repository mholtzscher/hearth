---
name: show-complexity
description: Visualize and evaluate essential versus accidental complexity in a project, module, workflow, or change. Use when the user asks where complexity comes from, whether a design is overengineered, or what a simplification would actually remove.
---

# Show complexity

Make complexity visible, explain what requires it, and identify what can be simplified. Borrow show-me's approach: use the smallest visual that explains the point, place evidence beside it, and keep prose brief.

## Establish the scope

Use the conversation to identify the target and the decision the user needs to make. For a change, establish the comparison base and include relevant staged, unstaged, and new files. For a proposal, distinguish the proposed design from existing code.

Start with applicable project guidance, package documentation, a file inventory, and entry-point and caller searches. Before judging an abstraction, distinguish production-used APIs, test-only APIs, and apparently unused code. Check registrations and supported external consumers before declaring an entry point unused. For apparent duplicates, establish which implementation production callers use.

Select consequential workflows from that inventory. Trace their callers, state, dependencies, and failure paths, reading the requirements that govern them. Expand discovery only to resolve a named question that could change a classification or recommendation. Follow each suspected burden far enough to identify who owns it and who pays for it.

For a whole project, use a shallow responsibility map to select the areas to inspect. State which areas were sampled and which remain unexamined. Ask a focused question only when an unknown requirement or scope choice would change the assessment.

Proceed when you can name the required behavior, the relevant constraints, and the implementation mechanisms being assessed. Cite file paths and symbols, with line numbers when useful. Treat missing rationale as uncertainty rather than evidence that a mechanism is unnecessary.

## Classify the burden

Classify individual rules, relationships, or mechanisms rather than labeling an entire module essential or accidental.

| Mark | Meaning | Evidence needed |
| --- | --- | --- |
| E | Essential complexity survives a simpler design that meets the same requirements and constraints. | Name the invariant or constraint, its source, and what breaks if it is removed. |
| A | Accidental complexity comes from the chosen implementation and can be reduced while preserving those requirements. | Show a plausible simpler design and explain how it preserves the behavior. |
| ? | The classification depends on facts not yet established. | Name the missing fact and how to resolve it. |

Essential is relative to the agreed problem. Availability, security, compatibility, and operational constraints count when they are real requirements. Separate a required capability from its particular implementation. Reliable delivery may be essential while several overlapping retry loops add accidental complexity.

Accidental does not mean worth removing now. Record the benefit of a chosen mechanism and the cost of replacing it. A compatibility adapter may impose avoidable complexity in the long term while remaining the best current tradeoff. Mark classifications conditional when they depend on an assumption.

Inspect the burdens that matter to this target:

- Rules and state combinations a reader must understand, including invalid states.
- Dependencies and places that must change together for one behavior change.
- Indirection, translation, or duplicated knowledge across boundaries.
- Ordering, concurrency, retries, and partial failures.
- Configuration and operational work required to run or diagnose the system.

Use size, branch counts, and dependency counts as leads. They do not establish necessity. Prefer concrete observations such as "changing this rule requires editing four mappings" over an invented complexity score or percentage.

## Choose the visual

Lead with the main finding and one compact view. Use real names from the inspected material and include the E/A/? legend. Add another view only if it answers a different question.

| Question | Useful view |
| --- | --- |
| Where is complexity concentrated across a project? | Shallow responsibility tree with annotated hotspots and inspection coverage. |
| Why is a module difficult to understand or change? | Call tree or dependency diagram showing ownership, indirection, and change coupling. |
| Why is a workflow difficult? | State or sequence diagram showing decisions, failure paths, and coordination. |
| What complexity does a change introduce or remove? | Before/after trees or a structural diff, plus a short account of added, removed, and relocated burdens. |
| Would a simpler design help? | Current/proposed views with the same boundaries and level of detail. |

Use text trees, pseudocode, or Mermaid when they suffice. Label what arrows mean, such as calls, data flow, or dependencies. Keep proposal labels distinct from observed behavior. Collapse repeated detail explicitly so the diagram cannot imply that omitted work disappeared. Use text labels as well as color.

For example, with repository evidence supporting each annotation:

```text
Save order
├── [E] Enforce quantity rules       required order invariant
├── [A] Translate through 3 models   identical fields; no separate contracts
├── [E] Commit atomically           partial orders must not persist
└── [?] Invalidate secondary cache  is it still read by a supported client?

Proposed
Save order
├── [E] Enforce quantity rules
├──     Map once at storage boundary
├── [E] Commit atomically
└── [?] Invalidate secondary cache  retained pending consumer check
```

This example illustrates presentation, not a rule that mappings or caches are unnecessary. Attach source references to the actual findings.

When the subject needs a richer visual or the user requests one, create a focused, self-contained HTML artifact and show it through an available preview tool. Keep the same evidence and classification discipline. Use an available HTML skill for artifact-specific guidance.

## Evaluate simplifications

For each material simplification, answer:

- What rule, state, dependency, translation, or coordination step disappears, and which concrete code or configuration can be removed?
- Where does its responsibility go, and who must understand or operate it afterward?
- Which behavior and constraints stay intact? What evidence supports that claim?
- What tradeoff, migration work, or new failure mode does the alternative introduce?
- What marks completion? Name collateral code or tests to preserve and the existing verification boundary, or identify a verification gap. Keep this proportional to the change; a small deletion needs only a short handoff.

Stop investigating a simplification candidate once its requirement, implementation burden, simpler alternative, tradeoff, and completion condition have source evidence. If a deciding fact remains unavailable, mark it `?` and name the lookup or clarification needed.

Compare total burden across the relevant boundary. Moving logic into a helper, framework, dependency, configuration file, or manual runbook may relocate complexity without removing it. Fewer files or lines can increase coupling or obscure ownership. Removing a requirement is a separate product tradeoff, not an equivalent implementation simplification.

For a change review, assess the net effect against its base. Show complexity the change retires or contains as well as what it adds. Distinguish temporary migration machinery from ongoing cost.

## Finish with a decision

After the visual, give a short evidence-backed assessment of the important E, A, and ? items. Recommend what to preserve, what to simplify first, and what needs clarification. Prioritize by the burden removed, the reach of the benefit, and the cost and risk of changing it. Keeping the current design is a valid conclusion.

End when the main findings have source evidence, proposed simplifications account for retained responsibilities, and unresolved requirements are explicit. Keep the response proportional to the scope. An assessment authorizes analysis and requested visual artifacts; implement refactors only when the user asks for them.
