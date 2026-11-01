export type ReadRequest = {
  key: string;
  kind: "state" | "series";
  load: (signal: AbortSignal, to: Date) => Promise<unknown>;
};
export type ReadSnapshot = { data?: unknown; error?: Error; loading: boolean };
type Entry = {
  request: ReadRequest; snapshot: ReadSnapshot; listeners: Set<() => void>;
  controller?: AbortController;
  requestedTo?: number;
  pendingTo?: Date;
};
export const emptySnapshot: ReadSnapshot = { loading: true };

/** A connection owns one cache. Cards and formatting never own refresh work. */
export class ReadCache {
  private entries = new Map<string, Entry>();
  private stateTimer?: ReturnType<typeof setInterval>;
  private seriesTimer?: ReturnType<typeof setTimeout>;
  private listening = false;
  private seriesRefreshQueued = false;

  snapshot(key: string): ReadSnapshot { return this.entries.get(key)?.snapshot ?? emptySnapshot; }

  subscribe(request: ReadRequest, listener: () => void): () => void {
    let entry = this.entries.get(request.key);
    if (!entry) {
      entry = { request, snapshot: emptySnapshot, listeners: new Set() };
      this.entries.set(request.key, entry);
    }
    entry.listeners.add(listener);
    if (entry.listeners.size === 1) {
      if (request.kind === "series") this.queueSeriesRefresh();
      else void this.read(entry, this.now());
    }
    this.start();
    return () => {
      entry.listeners.delete(listener);
      if (entry.listeners.size === 0) {
        entry.controller?.abort();
        this.entries.delete(request.key);
      }
      if (this.entries.size === 0) this.stop();
    };
  }

  dispose(): void {
    this.stop();
    for (const entry of this.entries.values()) entry.controller?.abort();
    this.entries.clear();
  }

  private now(): Date { return new Date(Math.floor(Date.now() / 1000) * 1000); }
  private queueSeriesRefresh(): void {
    if (this.seriesRefreshQueued) return;
    this.seriesRefreshQueued = true;
    queueMicrotask(() => {
      this.seriesRefreshQueued = false;
      const to = this.now();
      for (const entry of this.entries.values()) if (entry.request.kind === "series") void this.read(entry, to);
    });
  }
  private visible(): boolean { return document.visibilityState !== "hidden"; }
  private refresh = (): void => {
    if (!this.visible()) return;
    const to = this.now();
    for (const entry of this.entries.values()) void this.read(entry, to);
  };
  private visibility = (): void => {
    if (this.visible()) { this.refresh(); this.schedule(); }
    else this.clearTimers();
  };
  private start(): void {
    if (this.listening) return;
    this.listening = true;
    window.addEventListener("focus", this.refresh);
    window.addEventListener("online", this.refresh);
    document.addEventListener("visibilitychange", this.visibility);
    this.schedule();
  }
  private schedule(): void {
    this.clearTimers();
    if (!this.visible()) return;
    this.stateTimer = setInterval(() => {
      const to = this.now();
      for (const entry of this.entries.values()) if (entry.request.kind === "state") void this.read(entry, to);
    }, 10_000);
    const series = () => {
      const to = new Date(Math.floor(Date.now() / 60_000) * 60_000);
      for (const entry of this.entries.values()) if (entry.request.kind === "series") void this.read(entry, to);
      this.seriesTimer = setTimeout(series, 60_000 - Date.now() % 60_000);
    };
    this.seriesTimer = setTimeout(series, 60_000 - Date.now() % 60_000);
  }
  private clearTimers(): void { clearInterval(this.stateTimer); clearTimeout(this.seriesTimer); }
  private stop(): void {
    this.clearTimers();
    window.removeEventListener("focus", this.refresh);
    window.removeEventListener("online", this.refresh);
    document.removeEventListener("visibilitychange", this.visibility);
    this.listening = false;
  }
  private async read(entry: Entry, to: Date): Promise<void> {
    if (!this.visible()) return;
    if (entry.controller) {
      if (entry.request.kind === "series" && entry.requestedTo !== to.getTime()) entry.pendingTo = to;
      return;
    }
    const controller = new AbortController();
    entry.controller = controller;
    entry.requestedTo = to.getTime();
    try {
      const data = await entry.request.load(controller.signal, to);
      if (!controller.signal.aborted) entry.snapshot = { data, loading: false };
    } catch (error) {
      if (!controller.signal.aborted) entry.snapshot = {
        data: entry.snapshot.data, loading: false,
        error: error instanceof Error ? error : new Error("Read failed"),
      };
    } finally {
      entry.controller = undefined;
      if (!controller.signal.aborted) entry.listeners.forEach(listener => listener());
      const pending = entry.pendingTo;
      entry.pendingTo = undefined;
      if (pending && !controller.signal.aborted) void this.read(entry, pending);
    }
  }
}
