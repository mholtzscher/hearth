import { CommandError } from "../../../api/commands";
import type { CommandFailure, CommandRequest } from "../../../api/commands";
import type { CommandResult } from "../../../api/types";

/** One controllable Operation on one Entity. */
export interface ControlTarget {
  entityId: string;
  /** "set" and "stop" for shared controls; the rest are thermostat Operations. */
  operation:
    | "set"
    | "stop"
    | "set_mode"
    | "set_target"
    | "set_range"
    | "set_fan_mode";
}
/**
 * queue: a newer value waits for the Command in flight, then only the latest is sent.
 * replace: a newer value is sent at once and the earlier Command's outcome is ignored.
 * Covers use replace because their Adapter writes each Command without waiting
 * for an earlier position target.
 */
export type RequestPolicy = "queue" | "replace";
/** Scalar setters use { value }; structured color setters use their named fields. */
export type ControlValue = string | boolean | number | Record<string, unknown>;

/**
 * requested: the value the user most recently asked for.
 * pending: a Command is in flight; queued holds a newer value to send when it finishes.
 * confirming: the Command was satisfied, and the State refresh that follows it hasn't landed yet.
 * dispatched: Stop was sent; this evidence does not wait for or claim a State change.
 * failed: the last Command failed; the control shows observed State and the reason until the next request.
 */
export type ControlState =
  | { phase: "idle" }
  | { phase: "pending"; requested: unknown; queued?: { value: ControlValue } }
  | { phase: "confirming"; requested: unknown }
  | { phase: "dispatched"; requested: unknown; at: number }
  | { phase: "failed"; requested: unknown; failure: CommandFailure };

/** current identifies the latest send; an outcome from any other send is ignored. */
interface Entry {
  state: ControlState;
  listeners: Set<() => void>;
  inFlight: boolean;
  current: number;
}

export interface CommandQueueDeps {
  execute: (
    entityId: string,
    request: CommandRequest
  ) => Promise<CommandResult>;
  refreshState: () => Promise<void>;
}

const idle: ControlState = { phase: "idle" };
const key = (target: ControlTarget) => `${target.entityId}|${target.operation}`;

/**
 * Sends the Commands a household member asks for through view controls. Under
 * the default queue policy, each Entity Operation has at most one Command in
 * flight and one queued value, and a newer value replaces the queued one. Under
 * the replace policy, a newer value is sent at once and only the latest
 * Command's outcome is shown. A connection owns one queue, so every card
 * showing the same Entity shares its state.
 */
export class CommandQueue {
  private entries = new Map<string, Entry>();
  /** Disposal starts a new generation; outcomes from an earlier one are dropped. */
  private generation = 0;
  private readonly deps: CommandQueueDeps;

  constructor(deps: CommandQueueDeps) {
    this.deps = deps;
  }

  snapshot(target: ControlTarget): ControlState {
    return this.entries.get(key(target))?.state ?? idle;
  }

  subscribe(target: ControlTarget, listener: () => void): () => void {
    const entry = this.entry(target);
    entry.listeners.add(listener);
    return () => {
      entry.listeners.delete(listener);
      this.release(target, entry);
    };
  }

  request(
    target: ControlTarget,
    value: ControlValue,
    policy: RequestPolicy = "queue"
  ): void {
    const entry = this.entry(target);
    if (entry.inFlight && policy === "queue") {
      CommandQueue.update(entry, {
        phase: "pending",
        requested: value,
        queued: { value },
      });
      return;
    }
    void this.send(target, entry, value);
  }

  /**
   * Stops showing an Operation's Command: it returns to idle, and the outcome of
   * any Command still in flight is ignored. The Command itself is not affected.
   */
  forget(target: ControlTarget): void {
    const entry = this.entries.get(key(target));
    if (!entry) {
      return;
    }
    entry.current += 1;
    entry.inFlight = false;
    CommandQueue.update(entry, idle);
    this.release(target, entry);
  }

  /**
   * Forgets every control state and ignores Commands still in flight. The queue
   * stays usable, and subscribed cards keep their entries, because React may
   * dispose and reuse it when it remounts the provider.
   */
  dispose(): void {
    this.generation += 1;
    for (const [entryKey, entry] of this.entries) {
      entry.inFlight = false;
      entry.state = idle;
      if (entry.listeners.size === 0) {
        this.entries.delete(entryKey);
      }
    }
  }

  private entry(target: ControlTarget): Entry {
    let entry = this.entries.get(key(target));
    if (!entry) {
      entry = {
        state: idle,
        listeners: new Set(),
        inFlight: false,
        current: 0,
      };
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
    if (entry.listeners.size === 0 && entry.state.phase === "idle") {
      this.entries.delete(key(target));
    }
  }

  private static update(entry: Entry, state: ControlState): void {
    entry.state = state;
    for (const listener of entry.listeners) {
      listener();
    }
  }

  private async send(
    target: ControlTarget,
    entry: Entry,
    value: ControlValue
  ): Promise<void> {
    const { generation } = this;
    entry.current += 1;
    const send = entry.current;
    entry.inFlight = true;
    CommandQueue.update(entry, { phase: "pending", requested: value });
    let failure: CommandFailure | undefined;
    let result: CommandResult | undefined;
    try {
      result = await this.deps.execute(target.entityId, {
        operation: target.operation,
        parameters: typeof value === "object" ? value : { value },
      });
    } catch (error) {
      failure =
        error instanceof CommandError ? error.failure : { kind: "internal" };
    }
    if (generation !== this.generation || send !== entry.current) {
      return;
    }
    entry.inFlight = false;
    // A newer request supersedes this outcome, including a failure.
    const queued =
      entry.state.phase === "pending" ? entry.state.queued : undefined;
    if (queued) {
      void this.send(target, entry, queued.value);
      return;
    }
    if (failure) {
      CommandQueue.update(entry, {
        phase: "failed",
        requested: value,
        failure,
      });
      void this.deps.refreshState();
      this.release(target, entry);
      return;
    }
    if (result?.status === "dispatched" && target.operation === "stop") {
      CommandQueue.update(entry, {
        phase: "dispatched",
        requested: value,
        at: Date.now(),
      });
      void this.deps.refreshState();
      return;
    }
    const confirming: ControlState = { phase: "confirming", requested: value };
    CommandQueue.update(entry, confirming);
    await this.deps.refreshState();
    if (
      generation !== this.generation ||
      send !== entry.current ||
      entry.state !== confirming
    ) {
      return;
    }
    CommandQueue.update(entry, idle);
    this.release(target, entry);
  }
}
