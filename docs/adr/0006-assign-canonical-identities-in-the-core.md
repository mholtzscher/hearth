# Assign canonical identities in the core

Hearthd will assign immutable canonical IDs to devices and entities and store adapter-specific external identifiers as mappings. Names and adapter ownership may change without changing canonical identity, allowing a device to move from Home Assistant to a native adapter without breaking API clients or future automations; this requires explicit reconciliation rather than adopting whichever identifier an adapter supplies.
