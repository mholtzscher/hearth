import { object, text } from '../../dist/boundary.js';

export type Stack = {
  directory: string; coreURL: string; natsURL: string; simulatorURL: string;
  startCore(): Promise<void>; stopCore(): Promise<void>; startNATS(): Promise<void>; stopNATS(): Promise<void>;
  startSimulator(): Promise<void>; stopSimulator(): Promise<void>; close(): Promise<void>;
};

/** Test-only executable-boundary fixture owned by repository scripts, not the bridge. */
export async function createStack(options: { unknownPower?: boolean } = {}): Promise<Stack> {
  const module: unknown = await import(new URL('../../../../scripts/homekit-stack.mjs', import.meta.url).href);
  const create = object(module).createHomeKitStack;
  if (typeof create !== 'function') throw new Error('missing stack factory');
  const result: unknown = await create(options);
  const stack = object(result);
  const action = (name: string) => {
    const method = stack[name];
    if (typeof method !== 'function') throw new Error(`missing stack action ${name}`);
    return async () => { await method.call(stack); };
  };
  return { directory: text(stack.directory), coreURL: text(stack.coreURL), natsURL: text(stack.natsURL), simulatorURL: text(stack.simulatorURL),
    startCore: action('startCore'), stopCore: action('stopCore'), startNATS: action('startNATS'), stopNATS: action('stopNATS'),
    startSimulator: action('startSimulator'), stopSimulator: action('stopSimulator'), close: action('close') };
}
