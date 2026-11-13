import { CommandError, type CommandFailure, type CommandRequest } from "../../../api/commands";
import type { CommandResult } from "../../../api/types";

/** One controllable Operation on one Entity. */
export type ControlTarget = { entityId: string; operation: "set" };
/** Scalar setters use { value }; structured color setters use their named fields. */
export type ControlValue = string | boolean | number | Record<string, unknown>;

/**
 * requested: the value the user most recently asked for.
 * pending: a Command is in flight; queued holds a newer value to send when it finishes.
 * confirming: the Command was satisfied, and the State refresh that follows it hasn't landed yet.
 * failed: the last Command failed; the control shows observed State and the reason until the next request.
 */
export type ControlState =
  | { phase: "idle" }
  | { phase: "pending"; requested: unknown; queued?: { value: ControlValue } }
  | { phase: "confirming"; requested: unknown }
  | { phase: "failed"; requested: unknown; failure: CommandFailure };

type Entry = { state: ControlState; listeners: Set<() => void>; inFlight: boolean };

export type CommandQueueDeps = {
  execute: (entityId: string, request: CommandRequest) => Promise<CommandResult>;
  refreshState: () => Promise<void>;
};

const idle: ControlState = { phase: "idle" };
const key = (target: ControlTarget) => `${target.entityId}|${target.operation}`;

/**
 * Sends the Commands a household member asks for through view controls. Each
 * Entity Operation has at most one Command in flight and one queued value, and
 * a newer value replaces the queued one. A connection owns one queue, so every
 * card showing the same Entity shares its state.
 */
export class CommandQueue {
  private entries = new Map<string, Entry>();
  /** Disposal starts a new generation; outcomes from an earlier one are dropped. */
  private generation = 0;
  private readonly deps: CommandQueueDeps;

  constructor(deps: CommandQueueDeps) { this.deps = deps; }

  snapshot(target: ControlTarget): ControlState { return this.entries.get(key(target))?.state ?? idle; }

  subscribe(target: ControlTarget, listener: () => void): () => void {
    const entry = this.entry(target);
    entry.listeners.add(listener);
    return () => {
      entry.listeners.delete(listener);
      this.release(target, entry);
    };
  }

  request(target: ControlTarget, value: ControlValue): void {
    const entry = this.entry(target);
    if (entry.inFlight) {
      this.update(entry, { phase: "pending", requested: value, queued: { value } });
      return;
    }
    void this.send(target, entry, value);
  }

  /**
   * Forgets every control state and ignores Commands still in flight. The queue
   * stays usable, and subscribed cards keep their entries, because React may
   * dispose and reuse it when it remounts the provider.
   */
  dispose(): void {
    this.generation++;
    for (const [entryKey, entry] of this.entries) {
      entry.inFlight = false;
      entry.state = idle;
      if (entry.listeners.size === 0) this.entries.delete(entryKey);
    }
  }

  private entry(target: ControlTarget): Entry {
    let entry = this.entries.get(key(target));
    if (!entry) {
      entry = { state: idle, listeners: new Set(), inFlight: false };
      this.entries.set(key(target), entry);
    }
    return entry;
  }

  /**
   * Only an idle entry without subscribers is dropped. A card that unmounts
   * mid-Command still sees its outcome when it returns, and a failure stays
   * until the next request.
   */
  private release(target: ControlTarget, entry: Entry): void {
    if (entry.listeners.size === 0 && entry.state.phase === "idle") this.entries.delete(key(target));
  }

  private update(entry: Entry, state: ControlState): void {
    entry.state = state;
    entry.listeners.forEach(listener => listener());
  }

  private async send(target: ControlTarget, entry: Entry, value: ControlValue): Promise<void> {
    const generation = this.generation;
    entry.inFlight = true;
    this.update(entry, { phase: "pending", requested: value });
    let failure: CommandFailure | undefined;
    try {
      await this.deps.execute(target.entityId, { operation: target.operation, parameters: typeof value === "object" ? value : { value } });
    } catch (error) {
      failure = error instanceof CommandError ? error.failure : { kind: "internal" };
    }
    if (generation !== this.generation) return;
    entry.inFlight = false;
    // A newer request supersedes this outcome, including a failure.
    const queued = entry.state.phase === "pending" ? entry.state.queued : undefined;
    if (queued) {
      void this.send(target, entry, queued.value);
      return;
    }
    if (failure) {
      this.update(entry, { phase: "failed", requested: value, failure });
      void this.deps.refreshState();
      this.release(target, entry);
      return;
    }
    const confirming: ControlState = { phase: "confirming", requested: value };
    this.update(entry, confirming);
    await this.deps.refreshState();
    if (generation !== this.generation || entry.state !== confirming) return;
    this.update(entry, idle);
    this.release(target, entry);
  }
}
