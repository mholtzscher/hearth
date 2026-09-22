import { sqlite } from '@flue/runtime/node';

export default sqlite(process.env.FLUE_DB_PATH?.trim() || '../.data/flue-agent.db');
