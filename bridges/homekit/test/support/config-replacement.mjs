import { renameSync } from 'node:fs';

// Node loads this fixture after flock execs the private child, before its config
// read. The parent has already resolved and locked the original data directory.
if (process.argv[1]?.endsWith('/locked.js')) {
  renameSync(process.env.HOMEKIT_REPLACEMENT_SOURCE, process.env.HOMEKIT_REPLACEMENT_TARGET);
}
