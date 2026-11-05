import { createConversation, loadHistory, streamTurn, type ViewContext } from "../../api/agent";
import { emptyTurn, projectHistory, reduceTurn, type ConversationItem, type LiveTurn, type PresentedArtifact } from "./runtime";

type SessionState = {
  id?: string;
  items: ConversationItem[];
  busy: boolean;
  loading: boolean;
  error: string;
  failedPrompt: string;
  needsRecovery: boolean;
  live: LiveTurn | null;
  optimistic: ConversationItem | null;
  queued: ConversationItem | null;
};
type SendOptions = {
  context: ViewContext | undefined;
  draftVersion: number;
  onCreated: (id: string) => void;
};

/**
 * One browser-owned conversation. Routes subscribe to its snapshot; leaving a
 * route does not cancel work. Only the owning provider (unmount/server switch)
 * disposes requests. Core still owns durable history and cancellation on tab close.
 */
export class ConversationSession {
  private state: SessionState;
  private listeners = new Set<() => void>();
  private artifactListeners = new Set<(artifact: PresentedArtifact, draftVersion: number) => void>();
  private request: AbortController | null = null;
  private task: Promise<void> | null = null;
  private lifecycle = 0;
  private queuedOptions: SendOptions | null = null;

  constructor(
    readonly key: string,
    id: string | undefined,
    private readonly created: (session: ConversationSession, prompt: string) => ConversationSession,
    private readonly settled: () => void,
    private readonly unused: (session: ConversationSession) => void,
  ) {
    this.state = { id, items: [], busy: false, loading: !!id, error: "", failedPrompt: "", needsRecovery: false, live: null, optimistic: null, queued: null };
  }

  getSnapshot = () => this.state;
  subscribe = (listener: () => void) => {
    this.listeners.add(listener);
    return () => { this.listeners.delete(listener); this.releaseWhenUnused(); };
  };
  subscribeArtifacts(listener: (artifact: PresentedArtifact, draftVersion: number) => void) {
    this.artifactListeners.add(listener);
    return () => { this.artifactListeners.delete(listener); };
  }
  private present(artifact: PresentedArtifact, draftVersion: number) {
    this.artifactListeners.forEach(listener => listener(artifact, draftVersion));
  }
  private releaseWhenUnused() {
    // React can unsubscribe and resubscribe synchronously, including in StrictMode.
    queueMicrotask(() => {
      if (!this.listeners.size && !this.request && !this.state.busy && !this.state.queued && !this.state.needsRecovery && !this.state.failedPrompt) this.unused(this);
    });
  }
  private update(next: Partial<SessionState>) {
    this.state = { ...this.state, ...next };
    this.listeners.forEach(listener => listener());
  }
  dispose() { this.lifecycle++; this.request?.abort(); this.request = null; }

  private track(task: Promise<void>) {
    const tracked = task.finally(() => { if (this.task === tracked) this.task = null; });
    this.task = tracked;
    return tracked;
  }

  /** A live turn already owns its state; reopening it must not load a partial history over it. */
  load(): Promise<void> {
    if (!this.state.id || this.state.busy || (this.request && !this.request.signal.aborted)) return Promise.resolve();
    return this.track(this.readHistory(this.state.id));
  }

  private async readHistory(id: string) {
    const controller = new AbortController(); this.request = controller;
    this.update({ loading: true });
    try {
      const items = projectHistory(await loadHistory(id, controller.signal));
      if (!controller.signal.aborted) this.update({ items, live: null, optimistic: null, needsRecovery: false, error: "" });
    } catch (err) {
      if (!controller.signal.aborted) this.update({ needsRecovery: true, error: err instanceof Error ? err.message : "Could not load conversation." });
    } finally {
      if (!controller.signal.aborted) this.update({ loading: false });
      if (this.request === controller) this.request = null;
      this.releaseWhenUnused();
    }
  }

  send(text: string, options: SendOptions): Promise<void> {
    const prompt = text.trim();
    if (!prompt || this.state.busy || this.state.loading || this.state.needsRecovery) return Promise.resolve();
    return this.track(this.run(prompt, options));
  }

  /** A delayed create response can discover an ID whose canonical session is already open. */
  private async acceptCreatedPrompt(prompt: string, options: SendOptions) {
    const lifecycle = this.lifecycle;
    // The page may have obtained this owner but not started its initial load yet.
    const preceding = this.task ?? this.load();
    const queued: ConversationItem = { id: `queued-${crypto.randomUUID()}`, role: "user", text: prompt, activities: [], artifacts: [], context: options.context };
    this.queuedOptions = options;
    this.update({ queued });
    await preceding;
    if (lifecycle !== this.lifecycle || this.state.queued !== queued) return;
    // An earlier failed prompt remains in the composer. Keep this one queued for
    // explicit send/cancel instead of replacing that prompt or retrying anything.
    if (!this.state.needsRecovery && !this.state.failedPrompt) await this.sendQueued();
  }

  cancelQueued() { this.queuedOptions = null; this.update({ queued: null }); this.releaseWhenUnused(); }
  discardFailedPrompt() { this.update({ failedPrompt: "" }); this.releaseWhenUnused(); }
  async sendQueued() {
    if (this.state.busy || this.state.loading || this.state.needsRecovery || this.state.failedPrompt) return;
    const queued = this.state.queued;
    const options = this.queuedOptions;
    if (!queued || !options) return;
    this.cancelQueued();
    await this.send(queued.text, options);
  }

  private async run(prompt: string, options: SendOptions) {
    const { context, draftVersion, onCreated } = options;
    const controller = new AbortController(); this.request = controller;
    const base = context?.source_dashboard_id && context.source_revision ? { dashboard_id: context.source_dashboard_id, revision: context.source_revision } : null;
    let live = emptyTurn();
    this.update({ busy: true, error: "", failedPrompt: "", live,
      optimistic: { id: `pending-user-${Date.now()}`, role: "user", text: prompt, activities: [], artifacts: [], context },
    });
    let handedOff = false;
    let id = this.state.id;
    try {
      if (!id) {
        id = await createConversation(controller.signal);
        if (controller.signal.aborted) return;
        this.update({ id });
        const owner = this.created(this, prompt);
        onCreated(id);
        if (owner !== this) {
          handedOff = true;
          await owner.acceptCreatedPrompt(prompt, options);
          return;
        }
      }
      const scope = `${id}:${crypto.randomUUID()}`;
      await streamTurn(id, prompt, context, event => {
        if (controller.signal.aborted) return;
        live = reduceTurn(live, event, scope, base); this.update({ live });
        if (event.type === "view.presented" && event.artifact && context) this.present({ artifact: event.artifact, base }, draftVersion);
      }, controller.signal);
      if (controller.signal.aborted) return;
      if (!live.terminal) throw new Error("The connection ended before the turn completed.");
      if (live.failed) throw new Error(live.failed);
    } catch (err) {
      if (!controller.signal.aborted) this.update({ error: err instanceof Error ? err.message : "The request failed.", failedPrompt: prompt });
    } finally {
      if (!handedOff && !controller.signal.aborted && this.state.id) {
        try {
          const items = projectHistory(await loadHistory(this.state.id, controller.signal));
          if (!controller.signal.aborted) {
            const previous = new Set(this.state.items.flatMap(item => item.artifacts.map(value => value.artifact.artifact_id)));
            const discovered = items.flatMap(item => item.artifacts).filter(value => !previous.has(value.artifact.artifact_id));
            const latest = discovered[discovered.length - 1];
            if (latest && context) this.present(latest, draftVersion);
            this.update({ items, live: null, optimistic: null });
          }
        } catch {
          if (!controller.signal.aborted) this.update({ needsRecovery: true, error: `${this.state.error ? `${this.state.error} ` : ""}Could not reconcile history; streamed activity is retained here.` });
        }
      }
      if (!controller.signal.aborted) {
        this.update({ busy: false });
        if (!handedOff) this.settled();
      }
      if (this.request === controller) this.request = null;
      this.releaseWhenUnused();
    }
  }
}
