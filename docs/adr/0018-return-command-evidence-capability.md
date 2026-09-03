# Return a command-evidence capability from adapter acceptance

A successful adapter `Responder.Accept` will return a `CommandEvidence` capability bound to that accepted Command. Only the capability may publish Observations linked to the Command, so an adapter can return from its handler and later publish asynchronous outcome evidence without retaining the handler context or exposing a raw Command ID on public Observation types.

The capability supplies the accepted Command's Entity, runtime, correlation, causation, trace context, and absolute deadline. Ordinary `Session.PublishObservation` remains unlinked. This preserves the existing wire `refresh_for_command_id` field and Core projection semantics while making publication authority explicit and testable.
