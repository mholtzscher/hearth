// Fault injection uses Node's public TCP server lifecycle, not HAP internals.
import { Server } from 'node:net';
if (process.argv[1]?.endsWith('/locked.js')) {
  const listen = Server.prototype.listen;
  let listener;
  Server.prototype.listen = function (...args) {
    this.once('listening', () => {
      const address = this.address();
      if (address && typeof address !== 'string' && String(address.port) === process.env.HOMEKIT_FAULT_PORT) {
        listener = this;
      }
    });
    return Reflect.apply(listen, this, args);
  };
  // The test sends this only after observing production readiness. Closing on
  // an elapsed startup timer would test the initial probe, not runtime health.
  process.once('SIGUSR2', () => listener?.close());
}
