import assert from 'node:assert/strict';
import test from 'node:test';

import { loadAgentConfig } from './config.ts';

test('uses local Hearth and the current default model', () => {
  assert.deepEqual(loadAgentConfig({}), {
    hearthMcpUrl: 'http://127.0.0.1:8080/mcp',
    model: 'openai/gpt-5.6-luna',
  });
});

test('accepts explicit MCP and model configuration', () => {
  assert.deepEqual(
    loadAgentConfig({
      HEARTH_MCP_URL: 'https://hearth.example/mcp',
      HEARTH_AGENT_MODEL: 'anthropic/claude-sonnet-4-6',
    }),
    {
      hearthMcpUrl: 'https://hearth.example/mcp',
      model: 'anthropic/claude-sonnet-4-6',
    },
  );
});

test('rejects a non-HTTP MCP endpoint', () => {
  assert.throws(
    () => loadAgentConfig({ HEARTH_MCP_URL: 'file:///tmp/hearth' }),
    /must use http or https/,
  );
});

test('rejects a model without a provider', () => {
  assert.throws(
    () => loadAgentConfig({ HEARTH_AGENT_MODEL: 'gpt-5.6-luna' }),
    /provider\/model/,
  );
});
