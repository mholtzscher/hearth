import { experimental_createMCPClient as createMCPClient } from "@ai-sdk/mcp";
import { createOpenAI } from "@ai-sdk/openai";
import { generateText, stepCountIs, type ModelMessage } from "ai";
import { useRef, useState } from "react";
import { getBaseUrl } from "../api/client.ts";
import { ErrorBox, RawJson } from "../components/common.tsx";
import { Button } from "../components/ui/button.tsx";
import { Card, CardContent } from "../components/ui/card.tsx";
import { Textarea } from "../components/ui/textarea.tsx";

// Rough prototype: the browser talks OpenAI + hearthd /mcp directly. The API
// key ships in the bundle (VITE_OPENAI_API_KEY), so loopback/dev only.

const MODEL: string =
  (import.meta.env.VITE_OPENAI_MODEL as string | undefined) || "gpt-4o-mini";

const SYSTEM = [
  "You are a household assistant operating Hearth through its MCP tools.",
  "You can list and inspect entities, devices, adapters, and automations,",
  "read state/event/command histories, and execute entity commands.",
  "Prefer a read tool before acting. When executing a command, state the",
  "entity_id and operation first, then summarize the outcome plainly with IDs.",
].join(" ");

type ToolTrace = { name: string; args: unknown; result: unknown };

type ChatMsg = {
  id: number;
  role: "user" | "assistant";
  text: string;
  tools?: ToolTrace[];
  tokens?: number;
};

function mcpUrl(): string {
  return `${getBaseUrl() || window.location.origin}/mcp`;
}

// The AI SDK invokes its fetch unbound, which Chrome rejects with
// "Illegal invocation": keep a bound copy for the MCP transport.
const boundFetch: typeof globalThis.fetch = (...args) => globalThis.fetch(...args);

function toError(err: unknown): Error {
  return err instanceof Error ? err : new Error(String(err));
}

export default function ChatPage() {
  const [messages, setMessages] = useState<ChatMsg[]>([]);
  const [input, setInput] = useState("");
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<Error | null>(null);
  const [toolCount, setToolCount] = useState<number | null>(null);
  const idRef = useRef(1);

  const hasKey = Boolean(import.meta.env.VITE_OPENAI_API_KEY as string | undefined);

  function nextId(): number {
    return idRef.current++;
  }

  /** Lightweight connectivity check: count the tools /mcp exposes. */
  async function checkMcp() {
    setError(null);
    let client;
    try {
      client = await createMCPClient({
        transport: { type: "http", url: mcpUrl(), fetch: boundFetch },
      });
      const tools = await client.tools();
      setToolCount(Object.keys(tools).length);
    } catch (err) {
      setError(toError(err));
    } finally {
      await client?.close().catch(() => {});
    }
  }

  async function send() {
    const text = input.trim();
    if (!text || busy) return;
    const apiKey = import.meta.env.VITE_OPENAI_API_KEY as string | undefined;
    if (!apiKey) {
      setError(
        new Error("Missing VITE_OPENAI_API_KEY: copy web/.env.example to web/.env and restart vite."),
      );
      return;
    }
    const history: ChatMsg[] = [...messages, { id: nextId(), role: "user", text }];
    setMessages(history);
    setInput("");
    setBusy(true);
    setError(null);
    let client;
    try {
      client = await createMCPClient({
        transport: { type: "http", url: mcpUrl(), fetch: boundFetch },
      });
      const tools = await client.tools();
      setToolCount(Object.keys(tools).length);
      const openai = createOpenAI({ apiKey });
      const past: ModelMessage[] = history.map((m) => ({
        role: m.role,
        content: m.text,
      }));
      const result = await generateText({
        model: openai(MODEL),
        system: SYSTEM,
        messages: past,
        tools,
        stopWhen: stepCountIs(10),
      });
      const traces: ToolTrace[] = [];
      for (const step of result.steps) {
        for (const call of step.toolCalls) {
          const match = step.toolResults.find((r) => r.toolCallId === call.toolCallId);
          traces.push({ name: call.toolName, args: call.input, result: match?.output });
        }
      }
      setMessages([
        ...history,
        {
          id: nextId(),
          role: "assistant",
          text: result.text || "(no text — see tool calls)",
          tools: traces,
          tokens: result.totalUsage?.totalTokens,
        },
      ]);
    } catch (err) {
      setError(toError(err));
    } finally {
      setBusy(false);
      await client?.close().catch(() => {});
    }
  }

  return (
    <div>
      <div className="flex flex-wrap items-center gap-2">
        <h1 className="mr-2 text-lg font-semibold">Chat</h1>
        <Button size="sm" variant="outline" onClick={() => void checkMcp()} disabled={busy}>
          Check MCP
        </Button>
        <Button
          size="sm"
          variant="outline"
          onClick={() => {
            setMessages([]);
            setError(null);
          }}
          disabled={busy || messages.length === 0}
        >
          Clear
        </Button>
        {busy && <span className="text-sm text-muted-foreground">Thinking + calling tools…</span>}
      </div>
      <p className="mt-1 text-sm text-muted-foreground">
        Rough prototype: browser-direct OpenAI ({MODEL}) with hearthd MCP tools. Key{" "}
        {hasKey ? "present" : "missing (see web/.env.example)"} · MCP {mcpUrl()}
        {toolCount !== null && ` · ${toolCount} tools`}
      </p>

      {error && <ErrorBox error={error} />}

      <div className="mt-3 grid gap-2">
        {messages.length === 0 && (
          <p className="text-sm text-muted-foreground">
            Try “what devices do you see?” or “turn off entity …”. Reads run first; commands
            execute for real.
          </p>
        )}
        {messages.map((m) => (
          <Card key={m.id} size="sm" className={m.role === "user" ? "bg-muted/50" : undefined}>
            <CardContent>
              <div className="text-xs font-semibold tracking-wide text-muted-foreground uppercase">
                {m.role}
                {m.tokens !== undefined && ` · ${m.tokens} tokens`}
                {m.tools && m.tools.length > 0 && ` · ${m.tools.length} tool call${m.tools.length === 1 ? "" : "s"}`}
              </div>
              <p className="mt-1 text-sm whitespace-pre-wrap">{m.text}</p>
              {m.tools?.map((t, i) => (
                <details key={i} className="mt-1.5 rounded-md border px-2 py-1 text-xs">
                  <summary className="cursor-pointer font-mono">{t.name}</summary>
                  <div className="mt-1 grid gap-1">
                    <div>
                      <span className="text-muted-foreground">args: </span>
                      <code className="font-mono break-all">{JSON.stringify(t.args)}</code>
                    </div>
                    <div>
                      <span className="text-muted-foreground">result: </span>
                      <code className="font-mono break-all">
                        {JSON.stringify(t.result)?.slice(0, 2000)}
                      </code>
                    </div>
                  </div>
                </details>
              ))}
            </CardContent>
          </Card>
        ))}
      </div>

      <div className="mt-3 flex flex-wrap items-end gap-2">
        <Textarea
          aria-label="Chat message"
          placeholder="Ask about your household… (Enter to send, Shift+Enter for newline)"
          className="min-w-64 flex-1"
          rows={3}
          value={input}
          disabled={busy}
          onChange={(e) => setInput(e.target.value)}
          onKeyDown={(e) => {
            if (e.key === "Enter" && !e.shiftKey) {
              e.preventDefault();
              void send();
            }
          }}
        />
        <Button onClick={() => void send()} disabled={busy || !input.trim()}>
          Send
        </Button>
      </div>

      <RawJson
        value={{ model: MODEL, mcp: mcpUrl(), keyPresent: hasKey, toolCount }}
        title="Chat config JSON"
      />
    </div>
  );
}
