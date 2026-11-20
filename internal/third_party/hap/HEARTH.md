# Hearth compatibility copy

This is `github.com/brutella/hap` v0.0.35, with the upstream Apache-2.0
license in `LICENSE`. Upstream CI, generators, command and image assets are omitted.
The managed remote setter, explicit notification, server readiness and lifecycle,
timed-write nil handling, and selected persistence error paths are Hearth changes.
Encrypted event and response writes share a serialized, deadline-bounded socket
write so frame counters remain in wire order. Closed connections are removed
from the registry used to send events.

`internal/dnssd` contains runtime source from `github.com/brutella/dnssd`
v1.2.14, with its license alongside it. Its import paths are local to this module;
the responder reports completion of service registration and closes its socket on
registration errors. This copy is necessary because upstream `Add` merely queues
the service and `Respond` otherwise has no readiness signal.
