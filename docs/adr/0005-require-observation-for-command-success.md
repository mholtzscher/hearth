# Require a fresh observation to satisfy a command outcome

Hearth will distinguish dispatch, adapter acceptance, and satisfaction of a command outcome. After acceptance, the adapter actively refreshes upstream state and publishes a fresh observation, even if the requested value was already current and no change event occurred. A matching post-dispatch observation satisfies the requested outcome but does not prove that the command caused it; dispatch or upstream acceptance alone is insufficient.
