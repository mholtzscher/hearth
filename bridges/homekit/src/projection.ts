import { BoundaryError } from './boundary.js';
import { HearthClient, HearthError } from './hearth.js';
import type { EntityDescriptor, EntityRead, ReadPriority } from './hearth.js';

export type Projection =
  | { kind: 'usable'; revision: number; refresh: number; descriptor: EntityDescriptor; observationId: string; value: boolean | number }
  | { kind: 'unusable'; revision: number; refresh: number; reason: 'unknown_state' | 'disabled' | 'unavailable' | 'read_failed' | 'entity_missing' | 'descriptor_changed' };

type Waiter = { target: number; resolve(): void; reject(error: unknown): void; removeAbort(): void };

/** One authoritative refresh at a time; cancellation of a waiter never owns shared work. */
export class EntityProjection {
  readonly descriptor: EntityDescriptor;
  private value: Projection;
  private generation = 0;
  private active: { controller: AbortController; task: Promise<void> } | undefined;
  private dirty = false;
  private followupPriority: ReadPriority = 'background';
  private closed = false;
  private incompatible: 'entity_missing' | 'descriptor_changed' | undefined;
  private readonly waiters = new Set<Waiter>();
  private readonly listeners = new Set<(projection: Projection) => void>();

  constructor(private readonly client: HearthClient, initial: EntityRead) {
    this.descriptor = structuredClone(initial.descriptor);
    this.value = this.fromRead(initial, 0);
  }

  current(): Projection { return this.value; }
  onChange(listener: (projection: Projection) => void): () => void {
    if (this.closed) throw new BoundaryError('projection_closed');
    this.listeners.add(listener);
    return () => { this.listeners.delete(listener); };
  }

  hint(): void {
    if (this.closed) return;
    if (this.active) this.dirty = true;
    else this.start();
  }

  refreshAfterNow(signal: AbortSignal, priority: ReadPriority = 'foreground'): Promise<void> {
    if (this.closed) return Promise.reject(new BoundaryError('projection_closed'));
    if (signal.aborted) return Promise.reject(new BoundaryError('refresh_cancelled'));
    return new Promise<void>((resolve, reject) => {
      const abort = () => { this.waiters.delete(waiter); reject(new BoundaryError('refresh_cancelled')); };
      const waiter: Waiter = { target: this.generation + 1, resolve, reject, removeAbort: () => signal.removeEventListener('abort', abort) };
      this.waiters.add(waiter);
      signal.addEventListener('abort', abort, { once: true });
      if (this.active) {
        this.dirty = true;
        if (priority === 'foreground') this.followupPriority = 'foreground';
      } else this.start(priority);
    });
  }

  private start(priority: ReadPriority = 'background'): void {
    const generation = ++this.generation;
    const controller = new AbortController();
    const deadline = performance.now() + 2000;
    let failure: unknown;
    // Race the WHOLE request, not just its headers. Once this race settles, an
    // abandoned body parse has no capability to commit a late success/failure.
    let timer: NodeJS.Timeout;
    const timeout = new Promise<never>((_resolve, reject) => {
      timer = setTimeout(() => { controller.abort(); reject(new HearthError('core_timeout', 504)); }, 2000);
    });
    const failed = (input: unknown) => {
      if (this.closed) return;
      const error = performance.now() >= deadline ? new HearthError('core_timeout', 504) : input;
      failure = error;
      if (error instanceof HearthError && error.status === 404) this.incompatible = 'entity_missing';
      if (error instanceof HearthError && ['unsupported_entity_type', 'invalid_support'].includes(error.code)) this.incompatible = 'descriptor_changed';
      if (error instanceof HearthError && error.descriptor && JSON.stringify(error.descriptor) !== JSON.stringify(this.descriptor)) this.incompatible = 'descriptor_changed';
      this.commit({ kind: 'unusable', revision: 0, refresh: generation, reason: this.incompatible ?? 'read_failed' });
    };
    const task = Promise.race([this.client.readEntity(this.descriptor.id, controller.signal, priority), timeout])
      .then(read => {
        if (performance.now() >= deadline) { controller.abort(); failed(new HearthError('core_timeout', 504)); }
        else if (!this.closed) this.commit(this.fromRead(read, generation));
      }, failed).finally(() => {
        clearTimeout(timer);
        this.active = undefined;
        for (const waiter of this.waiters) {
          if (waiter.target > generation) continue;
          this.waiters.delete(waiter); waiter.removeAbort();
          if (failure !== undefined) waiter.reject(failure); else waiter.resolve();
        }
        if (!this.closed && this.dirty) {
          this.dirty = false;
          const followup = this.followupPriority; this.followupPriority = 'background';
          this.start(followup);
        }
      });
    this.active = { controller, task };
  }

  private fromRead(read: EntityRead, refresh: number): Projection {
    if (JSON.stringify(read.descriptor) !== JSON.stringify(this.descriptor)) this.incompatible = 'descriptor_changed';
    if (this.incompatible) return { kind: 'unusable', revision: 0, refresh, reason: this.incompatible };
    const reason = !read.enabled ? 'disabled' : read.availability !== 'available' ? 'unavailable' : !read.state ? 'unknown_state' : undefined;
    if (reason || !read.state) return { kind: 'unusable', revision: 0, refresh, reason: reason ?? 'unknown_state' };
    return { kind: 'usable', revision: 0, refresh, descriptor: this.descriptor, observationId: read.state.observationId, value: read.state.value };
  }

  private commit(next: Projection): void {
    const previous = this.value;
    const changed = previous.kind !== next.kind || (previous.kind === 'unusable' && next.kind === 'unusable' && previous.reason !== next.reason)
      || (previous.kind === 'usable' && next.kind === 'usable' && (previous.value !== next.value || previous.observationId !== next.observationId));
    this.value = { ...next, revision: previous.revision + Number(changed) };
    if (changed) for (const listener of this.listeners) listener(this.value);
  }

  async close(): Promise<void> {
    this.closed = true; this.dirty = false; this.listeners.clear();
    for (const waiter of this.waiters) { waiter.removeAbort(); waiter.reject(new BoundaryError('projection_closed')); }
    this.waiters.clear();
    this.active?.controller.abort();
    await this.active?.task;
  }
}
