# Generate Entity-type behavior from typed manifests

Built-in Entity types use authoritative JSON Schemas plus a versioned manifest and language-neutral conformance examples as their complete authoring interface. A small schema-checked relation DSL in the manifest defines cross-document validation, deadlines, and outcome matching; build-time generation compiles it to direct typed Go for the Entity-type package, SDK, and core catalog, so adding a type requires no handwritten per-type Go.

We chose a deliberately limited DSL over CEL because Hearth's built-ins are compiled at build time and currently need only equality, numeric ordering, and divisibility. CEL would add a second runtime type system and evaluator to both core and SDK. Operators will be added only for concrete product needs; if conditionals, collections, custom functions, or rapid operator growth make the DSL resemble a general-purpose language, CEL should be reconsidered.
