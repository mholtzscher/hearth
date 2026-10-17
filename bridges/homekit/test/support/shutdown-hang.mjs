import { Bridge } from '@homebridge/hap-nodejs';
if (process.argv[1]?.endsWith('/locked.js')) {
  // Simulate a dependency cleanup that never joins through its public API.
  Bridge.prototype.unpublish = async function () { await new Promise(() => {}); };
}
