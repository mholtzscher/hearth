# Measure agent prompt caching

Hearth captures provider-reported token usage without changing prompts or cache
settings. OpenAI Responses reads `usage.input_tokens_details`; OpenCode Go reads
the final Chat Completions usage frame's `usage.prompt_tokens_details`. Both map
`cached_tokens` to `cached_input_tokens` and `cache_write_tokens` to
`cache_write_input_tokens`.

An explicit zero is a reported zero. An absent or null counter means **unknown**.
In particular, a missing write count is not evidence that no cache was written.
The Go gateway currently reports standard cached-token counts and may return null
for cache writes. Hearth does not infer counts from billing or allowance fields.

## Measure with logs

Start Core with `--log-level debug --log-format json` (or the corresponding
`HEARTHD_LOG_LEVEL` and `HEARTHD_LOG_FORMAT` environment variables). Capture stderr
outside the repository. Each completed model call produces an `agent.model_usage`
record with provider/model identity, conversation ID, and reported counts. A turn
that uses tools usually produces several records. History replay emits none.

1. Create a conversation and ask a read-only question that uses Hearth tools.
2. Ask two related follow-up questions in that conversation. Keep the provider,
   model, reasoning setting, instructions, and tool definitions unchanged.
3. Select that conversation's records. Compute the token-weighted hit rate using
   only records with both input and cached-input counts:

```sh
jq -s --arg id 'aconv_...' '
  [.[] | select(.event == "agent.model_usage" and .conversation_id == $id)]
  | group_by([.provider, .model])
  | map(. as $calls
      | [.[] | select(.input_tokens != null and .input_tokens > 0
                      and .cached_input_tokens != null)] as $measured
      | ($measured | map(.input_tokens) | add // 0) as $input
      | ($measured | map(.cached_input_tokens) | add // 0) as $cached
      | {provider: $calls[0].provider, model: $calls[0].model,
         calls: ($calls | length), measured_calls: ($measured | length),
         input_tokens: $input, cached_input_tokens: $cached,
         cache_hit_rate: (if $input > 0 then $cached / $input else null end)})
' /path/to/core.jsonl
```

For example, 1,536 cached tokens out of 4,096 measured input tokens is a 37.5%
hit rate. Do not average per-call percentages or count unreported usage as misses.
Compare `measured_calls` with `calls` to see whether the sample is complete.
Short prompts, changed prefixes, cache expiry, or provider routing can yield zero
hits even when caching is enabled. Cache-hit rate alone does not establish dollar
savings, latency improvement, or a future hit rate.

## Retained evidence

Each persisted assistant message also stores the normalized record under
`extra.hearth_model_usage` in `agent_messages.message_json`. This remains private:
public conversation history and SSE payloads do not expose it, and it is not sent
back to the model. Retention follows the conversation's existing retention policy.
Older messages have no record and cannot be retroactively measured.

For a local read-only inspection, select only this metadata rather than dumping
conversation text or private reasoning:

```sh
sqlite3 -readonly /path/to/hearthd.db '
  SELECT id, conversation_id,
         json_extract(message_json, "$.extra.hearth_model_usage") AS usage
  FROM agent_messages
  WHERE role = "assistant"
    AND json_type(message_json, "$.extra.hearth_model_usage") = "object"
  ORDER BY id DESC LIMIT 20;
'
```

These counters describe completed responses accepted by the provider adapter,
not a billing ledger. Failed or interrupted streams may consume tokens without
returning usage. Logs may survive a later persistence failure, while SQLite only
contains committed messages. Use provider billing reports for total charges.
No prompt, tool payload, reasoning, API key, or endpoint URL is logged.
