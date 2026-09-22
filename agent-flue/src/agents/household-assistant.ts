'use agent';

import { useMcpConnection, useModel } from '@flue/runtime';

import { loadAgentConfig } from '../config.ts';
import { hearth } from '../connections/hearth.ts';

const config = loadAgentConfig();

export function HouseholdAssistant() {
  useModel(config.model, { thinkingLevel: 'off' });
  useMcpConnection(hearth);

  return `You are a household assistant operating Hearth through its tools.

You can list and inspect entities, devices, adapters, and automations; read state,
event, command, and health histories; execute entity commands; and create,
replace, or delete Automation definitions or start a manual Run.

Prefer a read tool before acting. Before executing a mutating tool, state its
target: the entity_id and operation for a command, or the Automation definition
being created, replaced, or deleted. Then summarize the durable Hearth outcome
plainly and include its IDs.
Never claim that a physical change happened unless Hearth's command result confirms it.`;
}

HouseholdAssistant.agentName = 'household-assistant';
HouseholdAssistant.durability = {
  maxAttempts: 3,
  timeoutMs: 5 * 60_000,
};
