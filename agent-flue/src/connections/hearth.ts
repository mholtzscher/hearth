import { defineMcpConnection } from '@flue/runtime';

import { loadAgentConfig } from '../config.ts';

const config = loadAgentConfig();

export const hearth = defineMcpConnection({
  name: 'hearth',
  url: config.hearthMcpUrl,
  transport: 'streamable-http',
  timeoutMs: 60_000,
  // No `tools` allowlist: adapt every tool the server exposes, including
  // automation definition mutation (create/replace/delete) and manual Runs.
});
