# Evaluate Conditions from coherent State snapshots

**Status:** Accepted and implemented. [Automation Conditions](../../specs/automation-conditions.md) is the implementation contract.

Automations evaluate Conditions once at admission using a coherent batch of current State evidence read through the devices module before the final admission transaction, rather than reading devices tables directly, maintaining another State projection, or calling devices while holding an automation transaction. Final admission still uses transaction-loaded current definitions and atomically commits every matching Fact's Run-or-Skip outcomes and receipts; if the evidence lacks any newly required Entity, admission writes nothing and requests a bounded complete re-snapshot outside the transaction. This preserves module ownership and avoids nested reads on the shared single-connection SQLite pool, while explicitly accepting that coherent State evidence is not atomic with admission, subsequent Commands, or physical effects.

## Consequences

- Missing snapshot coverage is not missing State: requested Entities with no State are covered negative evidence, while unread Entities require another complete snapshot, never a merge of different reads.
- Conditions use three-valued logic and admit only on a true root. Every evaluated predicate's evidence is retained, and false/unknown automatic decisions receive durable matched-Fact receipts so redelivery cannot reconsider them against later State.
- An unavailable State read delays the entire Fact admission, including unconditional matching siblings, rather than weakening its existing all-or-nothing transaction. Each admission call has bounded retries; corrupt stored evidence is diagnosed and the Fact is negatively acknowledged rather than terminated. The usual 30-second stale classification still precedes Conditions on redelivery, so repair cannot cause old household actions to execute. Corruption can delay pending delivery and suppress useful fresh execution until repaired; no Command retry or crash replay is introduced.
- State may change after the read. Historical explanations report what was evaluated, not State at the triggering Fact's time and not a guarantee that Conditions still hold when Steps execute.
