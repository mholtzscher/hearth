import { createRequire } from 'node:module';
const require = createRequire(import.meta.url);
const { Bridge, Accessory, Service, Characteristic, HAPStorage, Categories, MDNSAdvertiser, uuid } = require('@homebridge/hap-nodejs');

process.umask(0o077);
HAPStorage.setCustomStoragePath(process.env.HOMEKIT_TEST_STORE);
const bridge = new Bridge('Contract bridge', uuid.generate('hearth/homekit/dependency-contract'));
const accessory = new Accessory('Contract light', uuid.generate('hearth/homekit/dependency-light'));
const service = accessory.addService(Service.Lightbulb, 'Contract light', 'primary');
const observations = new Map();
const upstreamCompletions = [];
for (const type of [Characteristic.On, Characteristic.Brightness]) {
  const characteristic = service.getCharacteristic(type);
  const key = type === Characteristic.On ? 'on' : 'brightness';
  let authoritative = key === 'on' ? false : 0;
  characteristic.on('get', callback => callback(null, authoritative));
  // Conventional async setter: wait for upstream completion and return no
  // write-response value. The LIBRARY, not this fixture, publishes the request.
  characteristic.onSet(async () => {
    process.send({ event: 'set-started', characteristic: key });
    await new Promise(resolve => upstreamCompletions.push(resolve));
  });
  observations.set(key, value => {
    if (typeof value !== (key === 'on' ? 'boolean' : 'number')) throw new Error('invalid observation');
    authoritative = value;
    characteristic.updateValue(value);
  });
}
bridge.addBridgedAccessory(accessory);
bridge.on('listening', port => process.send({ port }));
process.on('message', async message => {
  if (message === 'stop') { await bridge.unpublish(); process.exit(0); }
  else if (message?.action === 'observe') {
    const observe = observations.get(message.characteristic);
    if (!observe) throw new Error('unknown observation slot');
    observe(message.value);
    process.send({ event: 'ack', id: message.id });
  } else if (message?.action === 'release') {
    for (const complete of upstreamCompletions.splice(0)) complete();
    process.send({ event: 'ack', id: message.id });
  }
});
process.on('disconnect', () => process.exit(1));
await bridge.publish({ username: '02:00:00:00:01:01', setupID: 'ABCD', pincode: '482-65-193', category: Categories.BRIDGE, port: 0, bind: '127.0.0.1', advertiser: MDNSAdvertiser.CIAO });
