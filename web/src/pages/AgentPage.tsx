import { useCallback, useEffect, useRef, useState } from "react";
import { cn } from "cn";
import { apiFetch, ApiError, getBaseUrl } from "../api/client.ts";
import { ErrorBox, RawJson } from "../components/common.tsx";
import { Button } from "../components/ui/button.tsx";
import { Card, CardContent } from "../components/ui/card.tsx";
import { Textarea } from "../components/ui/textarea.tsx";

// The browser talks to hearthd's /v1/agent endpoints. The model key and durable
// SQLite history stay server-side; only the conversation ID lives in localStorage.

const CONV_KEY = "hearth.agentConversationId";

type AgentMessage = {
  message_id: number;
  role: string;
  content: string;
  tool_calls: string[] | null;
  created_at: string;
};

type ConversationSummary = {
  id: string;
  created_at: string;
  message_count: number;
  last_message_at?: string | null;
  preview: string;
};

function agentUrl(path: string): string {
  const base = getBaseUrl();
  return `${base}/v1/agent${path}`;
}

async function createConversation(): Promise<string> {
  const body = await apiFetch<{ id: string }>(agentUrl("/conversations"), { method: "POST" });
  return body.id;
}

async function listConversations(): Promise<ConversationSummary[]> {
  const body = await apiFetch<{ conversations: ConversationSummary[] }>(
    agentUrl("/conversations"),
  );
  return body.conversations;
}

async function loadHistory(conversationId: string): Promise<AgentMessage[]> {
  const body = await apiFetch<{ messages: AgentMessage[] }>(
    agentUrl(`/conversations/${conversationId}/messages`),
  );
  return body.messages;
}

async function sendTurn(
  conversationId: string,
  text: string,
  onEvent: (event: TurnEvent) => void,
  signal: AbortSignal,
): Promise<void> {
  const res = await fetch(agentUrl(`/conversations/${conversationId}/messages/stream`), {
    method: "POST",
    headers: { "content-type": "application/json" },
    body: JSON.stringify({ text }),
    signal,
  });
  if (!res.ok || !res.body) {
    throw new ApiError(res.status, `${res.status} ${res.statusText}`);
  }
  // Minimal SSE parse: frames are `event: <type>\ndata: <json>\n\n`. The turn
  // emits tool events plus one final reply, never text deltas, so a
  // split-buffer parse is plenty.
  const reader = res.body.getReader();
  const decoder = new TextDecoder();
  let buffer = "";
  for (;;) {
    const { done, value } = await reader.read();
    if (done) break;
    buffer += decoder.decode(value, { stream: true });
    let cut: number;
    while ((cut = buffer.indexOf("\n\n")) >= 0) {
      const frame = buffer.slice(0, cut);
      buffer = buffer.slice(cut + 2);
      const dataLine = frame
        .split("\n")
        .find((line) => line.startsWith("data:"));
      if (!dataLine) continue;
      onEvent(JSON.parse(dataLine.slice(5).trim()) as TurnEvent);
    }
  }
}

type TurnEvent = {
  type: string;
  name?: string;
  arguments?: string;
  result?: string;
  reply?: string;
  tool_calls?: string[] | null;
  error?: string;
};

function isUnavailable(error: unknown): boolean {
  return error instanceof ApiError && error.status === 404;
}

/** Short human timestamp for the sidebar; falls back to the raw value. */
function shortTime(iso: string | null | undefined): string {
  if (!iso) return "no messages yet";
  const parsed = new Date(iso);
  if (Number.isNaN(parsed.getTime())) return iso;
  const date = parsed.toLocaleDateString(undefined, { month: "numeric", day: "numeric" });
  const time = parsed.toLocaleTimeString(undefined, { hour: "numeric", minute: "2-digit" });
  return `${date} ${time}`;
}

export default function AgentPage() {
  const [conversationId, setConversationId] = useState<string | null>(null);
  const [conversations, setConversations] = useState<ConversationSummary[]>([]);
  const [messages, setMessages] = useState<AgentMessage[]>([]);
  const [input, setInput] = useState("");
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<Error | null>(null);
  const [unavailable, setUnavailable] = useState(false);
  const idRef = useRef(-1);
  const abortRef = useRef<AbortController | null>(null);

  const refresh = useCallback(async (id: string) => {
    setMessages(await loadHistory(id));
  }, []);

  const refreshList = useCallback(async () => {
    setConversations(await listConversations());
  }, []);

  useEffect(() => {
    async function init() {
      try {
        let list: ConversationSummary[] = [];
        try {
          list = await listConversations();
          setConversations(list);
        } catch (err) {
          if (isUnavailable(err)) {
            setUnavailable(true);
            return;
          }
          throw err;
        }
        let id = localStorage.getItem(CONV_KEY);
        if (id) {
          try {
            await loadHistory(id);
          } catch (err) {
            if (!isUnavailable(err)) throw err;
            id = null;
          }
          // A stored ID from another browser/database is unusable: fall
          // back to the newest listed conversation or a fresh one.
          if (id && !list.some((c) => c.id === id)) {
            id = list[0]?.id ?? null;
          }
        } else {
          id = list[0]?.id ?? null;
        }
        if (!id) {
          id = await createConversation();
          localStorage.setItem(CONV_KEY, id);
          await refreshList();
        } else {
          localStorage.setItem(CONV_KEY, id);
        }
        setConversationId(id);
        await refresh(id);
      } catch (err) {
        if (isUnavailable(err)) {
          setUnavailable(true);
        } else {
          setError(err instanceof Error ? err : new Error(String(err)));
        }
      }
    }
    void init();
    return () => abortRef.current?.abort();
  }, [refresh, refreshList]);

  async function selectConversation(id: string) {
    if (id === conversationId || busy) return;
    setError(null);
    setBusy(true);
    try {
      localStorage.setItem(CONV_KEY, id);
      setConversationId(id);
      await refresh(id);
    } catch (err) {
      setError(err instanceof Error ? err : new Error(String(err)));
    } finally {
      setBusy(false);
    }
  }

  async function startNew() {
    setError(null);
    setBusy(true);
    try {
      const id = await createConversation();
      localStorage.setItem(CONV_KEY, id);
      setConversationId(id);
      setMessages([]);
      await refreshList();
    } catch (err) {
      setError(err instanceof Error ? err : new Error(String(err)));
    } finally {
      setBusy(false);
    }
  }

  async function send() {
    const text = input.trim();
    if (!text || busy || !conversationId) return;
    const id = conversationId;
    setInput("");
    setBusy(true);
    setError(null);
    // Optimistic user row (negative id marks it local until refresh).
    const localId = idRef.current--;
    setMessages((prev) => [
      ...prev,
      { message_id: localId, role: "user", content: text, tool_calls: null, created_at: "" },
    ]);
    const controller = new AbortController();
    abortRef.current = controller;
    let failed: string | null = null;
    try {
      await sendTurn(
        id,
        text,
        (event) => {
          // Incremental tool activity from the turn; the final refresh()
          // reconciles these temp rows with persisted history.
          if (event.type === "tool.started" && event.name) {
            const chipId = idRef.current--;
            setMessages((prev) => [
              ...prev,
              {
                message_id: chipId,
                role: "assistant",
                content: "",
                tool_calls: [event.name as string],
                created_at: "",
              },
            ]);
          } else if (event.type === "tool.finished") {
            const resultId = idRef.current--;
            setMessages((prev) => [
              ...prev,
              {
                message_id: resultId,
                role: "tool",
                content: event.result ?? "",
                tool_calls: null,
                created_at: "",
              },
            ]);
          } else if (event.type === "turn.failed") {
            failed = event.error || "agent turn failed";
          }
        },
        controller.signal,
      );
      if (failed) setError(new Error(failed));
      await refresh(id);
      // The turn changed this conversation's preview/count/recency.
      await refreshList().catch(() => {});
    } catch (err) {
      if ((err as Error).name !== "AbortError") {
        setError(err instanceof Error ? err : new Error(String(err)));
        await refresh(id).catch(() => {});
      }
    } finally {
      abortRef.current = null;
      setBusy(false);
    }
  }

  return (
    <div>
      <div className="flex flex-wrap items-center gap-2">
        <h1 className="mr-2 text-lg font-semibold">Agent</h1>
        <Button
          size="sm"
          variant="outline"
          onClick={() => conversationId && void refresh(conversationId)}
          disabled={busy || !conversationId}
        >
          Refresh
        </Button>
        {busy && <span className="text-sm text-muted-foreground">Thinking + calling tools…</span>}
      </div>
      <p className="mt-1 text-sm text-muted-foreground">
        Household agent: Eino ReAct in hearthd with durable SQLite history and no model
        key in the browser.
        {conversationId ? (
          <>
            {" "}
            Conversation <span className="font-mono text-xs">{conversationId}</span>
          </>
        ) : (
          " Connecting…"
        )}
      </p>

      {unavailable && (
        <div role="alert" className="my-2 rounded-lg border px-3 py-2 text-sm">
          Agent routes are unavailable: this Core build does not serve /v1/agent. The agent
          is a required module, so update hearthd and reload (see web/README.md), or use the
          browser-direct Chat page.
        </div>
      )}
      {error && <ErrorBox error={error} />}

      {!unavailable && (
        <div className="mt-3 flex flex-col gap-4 md:flex-row">
          <aside
            aria-label="Conversations"
            className="w-full shrink-0 md:sticky md:top-16 md:w-72 md:self-start"
          >
            <div className="flex items-center justify-between gap-2">
              <h2 className="text-sm font-semibold">Conversations</h2>
              <Button size="sm" variant="outline" onClick={() => void startNew()} disabled={busy}>
                New
              </Button>
            </div>
            <ul className="mt-2 flex flex-col gap-1 md:max-h-[calc(100vh-10rem)] md:overflow-y-auto md:pr-1">
              {conversations.map((c) => {
                const active = c.id === conversationId;
                return (
                  <li key={c.id} className="min-w-0">
                    <button
                      type="button"
                      aria-current={active ? "true" : undefined}
                      onClick={() => void selectConversation(c.id)}
                      disabled={busy}
                      title={c.preview || c.id}
                      className={cn(
                        "min-w-0 w-full overflow-hidden rounded-lg border px-2.5 py-1.5 text-left text-sm transition-colors",
                        active
                          ? "border-transparent bg-secondary font-medium text-secondary-foreground ring-1 ring-inset ring-secondary-foreground/30"
                          : "text-muted-foreground hover:bg-muted hover:text-foreground",
                      )}
                    >
                      <span className="block truncate">
                        {c.preview || "Empty conversation"}
                      </span>
                      <span className="mt-0.5 block truncate text-xs opacity-80">
                        {shortTime(c.last_message_at ?? c.created_at)} · {c.message_count}{" "}
                        message{c.message_count === 1 ? "" : "s"}
                      </span>
                    </button>
                  </li>
                );
              })}
              {conversations.length === 0 && (
                <li className="text-sm text-muted-foreground">
                  No conversations yet — start a new one.
                </li>
              )}
            </ul>
          </aside>

          <section aria-label="Conversation" className="min-w-0 flex-1">
            <div className="grid gap-2">
              {messages.length === 0 && !unavailable && (
                <p className="text-sm text-muted-foreground">
                  Try “what devices do you see?” — reads run first; commands execute for real.
                </p>
              )}
              {messages.map((m) => {
                if (m.role === "user") {
                  return (
                    <Card key={m.message_id} size="sm" className="bg-muted/50">
                      <CardContent>
                        <div className="text-xs font-semibold tracking-wide text-muted-foreground uppercase">
                          user
                        </div>
                        <p className="mt-1 text-sm whitespace-pre-wrap">{m.content}</p>
                      </CardContent>
                    </Card>
                  );
                }
                if (m.role === "tool") {
                  return (
                    <details
                      key={m.message_id}
                      className="rounded-md border px-2 py-1 text-xs text-muted-foreground"
                    >
                      <summary className="cursor-pointer">tool result</summary>
                      <code className="mt-1 block font-mono break-all">
                        {m.content.slice(0, 2000)}
                      </code>
                    </details>
                  );
                }
                if (m.content === "") {
                  return (
                    <div
                      key={m.message_id}
                      className="font-mono text-xs text-muted-foreground"
                    >
                      🔧 {(m.tool_calls ?? []).join(", ")}
                    </div>
                  );
                }
                return (
                  <Card key={m.message_id} size="sm">
                    <CardContent>
                      <div className="text-xs font-semibold tracking-wide text-muted-foreground uppercase">
                        assistant
                        {(m.tool_calls?.length ?? 0) > 0 &&
                          ` · ${(m.tool_calls ?? []).join(", ")}`}
                      </div>
                      <p className="mt-1 text-sm whitespace-pre-wrap">{m.content}</p>
                    </CardContent>
                  </Card>
                );
              })}
            </div>

            <div className="mt-3 flex flex-wrap items-end gap-2">
              <Textarea
                aria-label="Agent message"
                placeholder="Ask about your household… (Enter to send, Shift+Enter for newline)"
                className="min-w-64 flex-1"
                rows={3}
                value={input}
                disabled={busy || unavailable || !conversationId}
                onChange={(e) => setInput(e.target.value)}
                onKeyDown={(e) => {
                  if (e.key === "Enter" && !e.shiftKey) {
                    e.preventDefault();
                    void send();
                  }
                }}
              />
              <Button onClick={() => void send()} disabled={busy || !input.trim() || !conversationId}>
                Send
              </Button>
            </div>
          </section>
        </div>
      )}

      <RawJson
        value={{ endpoint: agentUrl("/conversations"), conversationId }}
        title="Agent config JSON"
      />
    </div>
  );
}
