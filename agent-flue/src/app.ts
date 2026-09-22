import { createAgentRouter } from '@flue/runtime/routing';
import { Hono } from 'hono';

import { HouseholdAssistant } from './agents/household-assistant.ts';

const app = new Hono();

app.get('/healthz', (context) => context.json({ status: 'ok' }));
app.route('/agents/household', createAgentRouter(HouseholdAssistant));

export default app;
