# Project retained observations into core-owned SQLite

Adapters will publish observations to JetStream so the core can consume durably, recover after disconnection, and tolerate redelivery. The core will project those observations into SQLite, which transactionally owns canonical state, external-ID mappings, and processed observation IDs so exact redelivery is a no-op. NATS KV is deferred because no other first-slice component needs shared write access to current state, and duplicating ownership would add reconciliation without product value.
