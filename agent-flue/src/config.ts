const DEFAULT_HEARTH_MCP_URL = 'http://127.0.0.1:8080/mcp';
const DEFAULT_MODEL = 'openai/gpt-5.6-luna';

export interface AgentConfig {
  hearthMcpUrl: string;
  model: string;
}

export function loadAgentConfig(env: NodeJS.ProcessEnv = process.env): AgentConfig {
  const hearthMcpUrl = env.HEARTH_MCP_URL?.trim() || DEFAULT_HEARTH_MCP_URL;
  const model = env.HEARTH_AGENT_MODEL?.trim() || DEFAULT_MODEL;

  const parsedMcpUrl = new URL(hearthMcpUrl);
  if (parsedMcpUrl.protocol !== 'http:' && parsedMcpUrl.protocol !== 'https:') {
    throw new Error('HEARTH_MCP_URL must use http or https');
  }
  if (!model.includes('/')) {
    throw new Error('HEARTH_AGENT_MODEL must be a provider/model specifier');
  }

  return { hearthMcpUrl: parsedMcpUrl.toString(), model };
}
