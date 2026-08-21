# Use expiring request/reply for immediate commands

The core will send immediate entity commands to the owning adapter with Core NATS request/reply and an explicit deadline. A missing responder or elapsed deadline is a failure rather than a reason to queue work for later, because executing an old interactive command after an adapter reconnects would no longer represent the caller's immediate intent; durable scheduled behavior, if added later, must be modeled separately.
