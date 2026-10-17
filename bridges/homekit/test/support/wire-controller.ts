import { createRequire } from 'node:module';
import type * as ProtocolModule from 'hap-controller/lib/protocol/pairing-protocol.js';
import type * as ConnectionModule from 'hap-controller/lib/transport/ip/http-connection.js';
import type { PairingData } from 'hap-controller/lib/protocol/pairing-protocol.js';

const require = createRequire(import.meta.url);
// Import only IP/protocol entry points; the optional native BLE stack is unused.
const Protocol: typeof ProtocolModule.default.default = require('hap-controller/lib/protocol/pairing-protocol.js').default;
const Connection: typeof ConnectionModule.default.default = require('hap-controller/lib/transport/ip/http-connection.js').default;

/** Independent SRP/Ed25519/encrypted transport; exposes real HTTP bytes, never controller echoes. */
export class WireController {
  private readonly protocol: InstanceType<typeof Protocol>;
  private readonly connection: InstanceType<typeof Connection>;

  constructor(port: number, saved?: PairingData) {
    this.protocol = new Protocol(saved);
    this.connection = new Connection('127.0.0.1', port);
  }

  async pair(code: string): Promise<PairingData> {
    const m2 = await this.connection.post('/pair-setup', await this.protocol.buildPairSetupM1(), 'application/pairing+tlv8');
    const pairing = await this.protocol.parsePairSetupM2(m2.body);
    const m4 = await this.connection.post('/pair-setup', await this.protocol.buildPairSetupM3(pairing, code), 'application/pairing+tlv8');
    await this.protocol.parsePairSetupM4(m4.body);
    const m6 = await this.connection.post('/pair-setup', await this.protocol.buildPairSetupM5(), 'application/pairing+tlv8');
    await this.protocol.parsePairSetupM6(m6.body);
    const data = this.protocol.getLongTermData();
    if (!data) throw new Error('controller pairing missing');
    return data;
  }

  async verify(): Promise<void> {
    const m2 = await this.connection.post('/pair-verify', await this.protocol.buildPairVerifyM1(), 'application/pairing+tlv8');
    await this.protocol.parsePairVerifyM2(m2.body);
    const m4 = await this.connection.post('/pair-verify', await this.protocol.buildPairVerifyM3(), 'application/pairing+tlv8');
    await this.protocol.parsePairVerifyM4(m4.body);
    this.connection.setSessionKeys(await this.protocol.getSessionKeys());
  }

  get(path: string) { return this.connection.get(path); }
  put(path: string, body: unknown, readEvents = false) { return this.connection.put(path, JSON.stringify(body), 'application/hap+json', readEvents); }
  onEvent(listener: (body: Buffer) => void) { this.connection.on('event', listener); return () => this.connection.off('event', listener); }
  close(): void { this.connection.close(); }
}
