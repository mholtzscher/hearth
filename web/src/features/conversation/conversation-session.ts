import { createConversation, loadHistory, streamTurn } from "../../api/agent";
import type {
  ConversationHistory,
  SelectedContext,
  TurnState,
} from "../../api/agent";
import { ApiError } from "../../api/client";
import { emptyTurn, projectHistory, reduceTurn } from "./runtime";
import type { ConversationItem, LiveTurn, PresentedArtifact } from "./runtime";

interface SessionState {
  id?: string;
  items: ConversationItem[];
  busy: boolean;
  loading: boolean;
  error: string;
  failedPrompt: string;
  /** The start time Core reported for the failed turn behind failedPrompt, if it came from history. */
  failedTurn: string;
  needsRecovery: boolean;
  /** Core is still running a turn this page is not streaming; history is re-read until it ends. */
  following: boolean;
  live: LiveTurn | null;
  optimistic: ConversationItem | null;
  queued: ConversationItem | null;
}
interface SendOptions {
  context: SelectedContext | undefined;
  draftVersion: number;
  onCreated: (id: string) => void;
}
const turnBase = (context: SelectedContext | undefined) => {
  if (
    context &&
    "source_dashboard_id" in context &&
    context.source_dashboard_id &&
    context.source_revision
  ) {
    return {
      dashboard_id: context.source_dashboard_id,
      revision: context.source_revision,
    };
  }
  return null;
};

/** How often a page re-reads history while Core runs a turn it is not streaming. */
export const FOLLOW_INTERVAL_MS = 2000;

const lastUser = (items: ConversationItem[]) =>
  items.findLast((item) => item.role === "user");

/** A turn this page sent, followed or reconciled after its stream ended. */
interface SentTurn {
  prompt: string;
  /** The newest stored user row before the send, so a repeated prompt is not mistaken for this one. */
  recordedBefore: string | undefined;
  context: SelectedContext | undefined;
  draftVersion: number;
}

/** Core recorded the sent prompt as a new user row. */
const recordedPrompt = (items: ConversationItem[], sent: SentTurn) => {
  const last = lastUser(items);
  return (
    last !== undefined &&
    last.id !== sent.recordedBefore &&
    last.text === sent.prompt
  );
};

/**
 * One browser-owned conversation. Routes subscribe to its snapshot; leaving a
 * route does not cancel work. Only the owning provider (unmount/server switch)
 * disposes requests. Core owns each turn: it keeps running when the stream
 * drops or the tab closes, so the session follows it through history until it
 * ends.
 */
export class ConversationSession {
  readonly key: string;
  private readonly created: (
    session: ConversationSession,
    prompt: string
  ) => ConversationSession;
  private readonly settled: () => void;
  private readonly unused: (session: ConversationSession) => void;
  private state: SessionState;
  private listeners = new Set<() => void>();
  private artifactListeners = new Set<
    (artifact: PresentedArtifact, draftVersion: number) => void
  >();
  private request: AbortController | null = null;
  private task: Promise<void> | null = null;
  private lifecycle = 0;
  private queuedOptions: SendOptions | null = null;
  /** Ends the current wait between follow reads early. */
  private wake: (() => void) | null = null;
  /** The failed turn whose prompt the person discarded, so later reads do not offer it again. */
  private dismissedFailure = "";

  constructor(
    key: string,
    id: string | undefined,
    created: (
      session: ConversationSession,
      prompt: string
    ) => ConversationSession,
    settled: () => void,
    unused: (session: ConversationSession) => void
  ) {
    this.key = key;
    this.created = created;
    this.settled = settled;
    this.unused = unused;
    this.state = {
      id,
      items: [],
      busy: false,
      loading: !!id,
      error: "",
      failedPrompt: "",
      failedTurn: "",
      needsRecovery: false,
      following: false,
      live: null,
      optimistic: null,
      queued: null,
    };
  }

  getSnapshot = () => this.state;
  subscribe = (listener: () => void) => {
    this.listeners.add(listener);
    return () => {
      this.listeners.delete(listener);
      this.releaseWhenUnused();
    };
  };
  subscribeArtifacts(
    listener: (artifact: PresentedArtifact, draftVersion: number) => void
  ) {
    this.artifactListeners.add(listener);
    return () => {
      this.artifactListeners.delete(listener);
    };
  }
  private present(artifact: PresentedArtifact, draftVersion: number) {
    for (const listener of this.artifactListeners) {
      listener(artifact, draftVersion);
    }
  }
  private releaseWhenUnused() {
    // React can unsubscribe and resubscribe synchronously, including in StrictMode.
    queueMicrotask(() => {
      if (
        !this.listeners.size &&
        !this.request &&
        !this.state.busy &&
        !this.state.queued &&
        !this.state.needsRecovery &&
        !this.state.failedPrompt
      ) {
        this.unused(this);
      }
    });
  }
  private update(next: Partial<SessionState>) {
    this.state = { ...this.state, ...next };
    for (const listener of this.listeners) {
      listener();
    }
  }
  dispose() {
    this.lifecycle += 1;
    this.request?.abort();
    this.request = null;
    this.wake?.();
  }

  private track(task: Promise<void>) {
    const tracked = task.finally(() => {
      if (this.task === tracked) {
        this.task = null;
      }
    });
    this.task = tracked;
    return tracked;
  }

  /** A live turn already owns its state; reopening it must not load a partial history over it. */
  load(): Promise<void> {
    if (
      !this.state.id ||
      this.state.busy ||
      (this.request && !this.request.signal.aborted)
    ) {
      return Promise.resolve();
    }
    return this.track(this.readHistory(this.state.id));
  }

  private async readHistory(id: string) {
    const controller = new AbortController();
    this.request = controller;
    this.update({ loading: true });
    let turn: TurnState | null = null;
    try {
      const history = await loadHistory(id, controller.signal);
      if (!controller.signal.aborted) {
        ({ turn } = history);
        this.update({
          items: projectHistory(history.messages),
          live: null,
          optimistic: null,
          needsRecovery: false,
          error: "",
        });
        this.reportFailedTurn(history);
      }
    } catch (error) {
      if (!controller.signal.aborted) {
        this.update({
          needsRecovery: true,
          error:
            error instanceof Error
              ? error.message
              : "Could not load conversation.",
        });
      }
    } finally {
      if (!controller.signal.aborted) {
        this.update({ loading: false });
      }
      if (this.request === controller) {
        this.request = null;
      }
      this.releaseWhenUnused();
    }
    if (turn?.state === "running" && !controller.signal.aborted) {
      await this.follow(id);
    }
  }

  /**
   * A turn that failed while nobody was streaming it offers its own prompt
   * again, once. Core reports that prompt because the turn may have failed
   * before saving it, when history ends with an earlier turn's prompt.
   */
  private reportFailedTurn({ turn }: ConversationHistory) {
    if (turn?.state !== "failed" || turn.started_at === this.dismissedFailure) {
      return;
    }
    this.update({
      error: turn.error,
      failedPrompt: turn.prompt ?? "",
      failedTurn: turn.started_at,
    });
  }

  /**
   * Reports how a turn this page did not see end went. When the page sent it,
   * a prompt history never recorded as a new user row did not start, even if
   * an older turn's failure is still reported, so that prompt is offered again.
   */
  private reportTurnEnd(
    history: ConversationHistory,
    items: ConversationItem[],
    sent?: SentTurn
  ) {
    if (sent && !recordedPrompt(items, sent)) {
      this.update({
        error: "The connection ended before the turn started.",
        failedPrompt: sent.prompt,
        failedTurn: "",
      });
      return;
    }
    this.reportFailedTurn(history);
  }

  /**
   * Re-reads history every FOLLOW_INTERVAL_MS, and as soon as the page becomes
   * visible again, until Core reports the turn is no longer running. Reads that
   * fail, such as on a phone that is offline, are retried, and streamed
   * activity stays on screen until one succeeds. When the session sent the
   * turn itself, drafts it completes reach the open editor, and a prompt
   * history never recorded is offered again because the turn did not start.
   */
  private async follow(id: string, sent?: SentTurn) {
    const { lifecycle } = this;
    const controller = new AbortController();
    this.request = controller;
    this.update({ busy: true, following: true });
    try {
      while (lifecycle === this.lifecycle) {
        // oxlint-disable-next-line no-await-in-loop -- each read waits for the previous interval.
        await this.waitToFollow();
        if (lifecycle !== this.lifecycle) {
          return;
        }
        try {
          // oxlint-disable-next-line no-await-in-loop -- reads are sequential by design.
          const history = await loadHistory(id, controller.signal);
          if (lifecycle !== this.lifecycle) {
            return;
          }
          const items = projectHistory(history.messages);
          if (sent) {
            this.presentNewArtifact(items, sent.context, sent.draftVersion);
          }
          this.update({ items, live: null, optimistic: null, error: "" });
          if (history.turn?.state !== "running") {
            this.reportTurnEnd(history, items, sent);
            break;
          }
        } catch (error) {
          // A client error such as a pruned conversation will not recover by
          // waiting; anything else keeps following, since the turn's outcome
          // is still in Core.
          if (
            error instanceof ApiError &&
            error.status >= 400 &&
            error.status < 500
          ) {
            this.update({ needsRecovery: true, error: error.message });
            break;
          }
        }
      }
      if (lifecycle !== this.lifecycle) {
        return;
      }
      this.update({ busy: false, following: false });
      this.settled();
    } finally {
      if (this.request === controller) {
        this.request = null;
      }
      this.releaseWhenUnused();
    }
  }

  private waitToFollow() {
    const { promise, resolve } = Promise.withResolvers<undefined>();
    const visible = () => {
      if (document.visibilityState === "visible") {
        done();
      }
    };
    const done = () => {
      clearTimeout(timer);
      document.removeEventListener("visibilitychange", visible);
      this.wake = null;
      resolve(undefined);
    };
    const timer = setTimeout(done, FOLLOW_INTERVAL_MS);
    document.addEventListener("visibilitychange", visible);
    this.wake = done;
    return promise;
  }

  send(text: string, options: SendOptions): Promise<void> {
    const prompt = text.trim();
    if (
      !prompt ||
      this.state.busy ||
      this.state.loading ||
      this.state.needsRecovery
    ) {
      return Promise.resolve();
    }
    return this.track(this.run(prompt, options));
  }

  /** A delayed create response can discover an ID whose canonical session is already open. */
  private async acceptCreatedPrompt(prompt: string, options: SendOptions) {
    const { lifecycle } = this;
    // The page may have obtained this owner but not started its initial load yet.
    const preceding = this.task ?? this.load();
    const queued: ConversationItem = {
      id: `queued-${crypto.randomUUID()}`,
      role: "user",
      text: prompt,
      activities: [],
      artifacts: [],
      context: options.context,
    };
    this.queuedOptions = options;
    this.update({ queued });
    await preceding;
    if (lifecycle !== this.lifecycle || this.state.queued !== queued) {
      return;
    }
    // An earlier failed prompt remains in the composer. Keep this one queued for
    // explicit send/cancel instead of replacing that prompt or retrying anything.
    if (!this.state.needsRecovery && !this.state.failedPrompt) {
      await this.sendQueued();
    }
  }

  cancelQueued() {
    this.queuedOptions = null;
    this.update({ queued: null });
    this.releaseWhenUnused();
  }
  discardFailedPrompt() {
    this.dismissedFailure = this.state.failedTurn;
    this.update({ failedPrompt: "", failedTurn: "" });
    this.releaseWhenUnused();
  }
  async sendQueued() {
    if (
      this.state.busy ||
      this.state.loading ||
      this.state.needsRecovery ||
      this.state.failedPrompt
    ) {
      return;
    }
    const { queued } = this.state;
    const options = this.queuedOptions;
    if (!queued || !options) {
      return;
    }
    this.cancelQueued();
    await this.send(queued.text, options);
  }

  private presentNewArtifact(
    items: ConversationItem[],
    context: SelectedContext | undefined,
    draftVersion: number
  ) {
    const previous = new Set(
      this.state.items.flatMap((item) =>
        item.artifacts.map((value) => value.artifact.artifact_id)
      )
    );
    const discovered = items
      .flatMap((item) => item.artifacts)
      .filter((value) => !previous.has(value.artifact.artifact_id));
    const latest = discovered.at(-1);
    if (latest && context) {
      this.present(latest, draftVersion);
    }
  }

  /**
   * Re-reads history after a turn's stream ends and reports whether Core is
   * still running a turn whose stream dropped, so the session should follow it.
   */
  private async reconcile(
    sent: SentTurn,
    dropped: boolean,
    controller: AbortController
  ): Promise<boolean> {
    const { id } = this.state;
    if (!id) {
      return false;
    }
    const { context, draftVersion } = sent;
    try {
      const history = await loadHistory(id, controller.signal);
      if (controller.signal.aborted) {
        return false;
      }
      const items = projectHistory(history.messages);
      this.presentNewArtifact(items, context, draftVersion);
      this.update({ items, live: null, optimistic: null });
      if (!dropped) {
        return false;
      }
      if (history.turn?.state === "running") {
        return true;
      }
      this.reportTurnEnd(history, items, sent);
      return false;
    } catch {
      if (controller.signal.aborted) {
        return false;
      }
      // A dropped stream whose history cannot be read yet is followed until
      // Core answers.
      if (!dropped) {
        this.update({
          needsRecovery: true,
          error: `${this.state.error ? `${this.state.error} ` : ""}Could not reconcile history; streamed activity is retained here.`,
        });
      }
      return dropped;
    }
  }

  private async run(prompt: string, options: SendOptions) {
    const { context, draftVersion, onCreated } = options;
    const controller = new AbortController();
    this.request = controller;
    const base = turnBase(context);
    const sent: SentTurn = {
      prompt,
      recordedBefore: lastUser(this.state.items)?.id,
      context,
      draftVersion,
    };
    let live = emptyTurn();
    this.update({
      busy: true,
      error: "",
      failedPrompt: "",
      failedTurn: "",
      live,
      optimistic: {
        id: `pending-user-${crypto.randomUUID()}`,
        role: "user",
        text: prompt,
        activities: [],
        artifacts: [],
        context,
      },
    });
    let handedOff = false;
    let streaming = false;
    let dropped = false;
    let { id } = this.state;
    try {
      if (!id) {
        id = await createConversation(controller.signal);
        if (controller.signal.aborted) {
          return;
        }
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
      streaming = true;
      await streamTurn(
        id,
        prompt,
        context,
        (event) => {
          if (controller.signal.aborted) {
            return;
          }
          live = reduceTurn(live, event, scope, base);
          this.update({ live });
          if (
            (event.type === "view.presented" ||
              event.type === "automation.presented") &&
            event.artifact &&
            context
          ) {
            this.present({ artifact: event.artifact, base }, draftVersion);
          }
        },
        controller.signal
      );
      if (controller.signal.aborted) {
        return;
      }
      // Core keeps running a turn whose stream ended early; history says how it went.
      dropped = !live.terminal;
      if (live.failed) {
        throw new Error(live.failed);
      }
    } catch (error) {
      // A connection lost after the request left can still have started a turn.
      dropped = streaming && !(error instanceof ApiError) && !live.failed;
      if (!(controller.signal.aborted || dropped)) {
        this.update({
          error: error instanceof Error ? error.message : "The request failed.",
          failedPrompt: prompt,
        });
      }
    } finally {
      const follow =
        !handedOff &&
        !controller.signal.aborted &&
        (await this.reconcile(sent, dropped, controller));
      if (this.request === controller) {
        this.request = null;
      }
      if (follow && this.state.id) {
        await this.follow(this.state.id, sent);
      } else if (!controller.signal.aborted) {
        this.update({ busy: false });
        if (!handedOff) {
          this.settled();
        }
      }
      this.releaseWhenUnused();
    }
  }
}
