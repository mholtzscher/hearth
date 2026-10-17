/** Fixed-code diagnostics only; never serialize unknown exceptions or input. */
export function diagnostic(component: string, reason: string, event = 'failed', context: { accessoryKey?: string; entityId?: string; commandId?: string } = {}): void {
  try { process.stderr.write(`${JSON.stringify({ app: 'hearth-bridge-homekit', pid: process.pid, component, event, reason, ...context })}\n`); }
  catch { /* best effort, including during immediate persistence-failure stop */ }
}
