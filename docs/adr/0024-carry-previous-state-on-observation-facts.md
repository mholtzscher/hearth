# Carry previous State on Observation Facts

Observation Facts carry the optional normalized State value that immediately preceded their Observation's acceptance, allowing Observation Triggers to match deterministic transitions. Capturing the predecessor in the Devices projection transaction avoids admission-time State races and cross-module history lookups; delayed delivery, redelivery, and restart therefore preserve the same transition evidence, while absence remains distinct from a real JSON `null` value.

The field is an additive change to the version 1 Observation Fact contract. Consumers treat its absence as no previous State, so queued older Facts remain valid and previous-value comparisons simply do not match them.
