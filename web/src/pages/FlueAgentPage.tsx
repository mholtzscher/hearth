import { useFlueAgent } from "@flue/react";
import type {
  AgentStatus,
  FlueConversationMessage,
  FlueConversationPart,
} from "@flue/react";
import { useState } from "react";
import {
  FLUE_INSTANCE_KEY,
  flueConversationUrl,
  loadOrCreateFlueInstanceId,
  newFlueInstanceId,
} from "../api/flue.ts";
import { ErrorBox, StatusChip } from "../components/common.tsx";
import { Button } from "../components/ui/button.tsx";
import { Card, CardContent } from "../components/ui/card.tsx";
import { Textarea } from "../components/ui/textarea.tsx";

// Flue drives the conversation over the dashboard's same-origin /flue proxy:
// `useFlueAgent` loads one instance's durable history and follows its live
// updates. The local page owns only the chosen instance ID (localStorage) and
// transient input, so the built-in /agent page and hearthd stay untouched.

const STATUS_TONE: Record<AgentStatus, "default" | "info" | "error"> = {
  idle: "default",
  connecting: "info",
  submitted: "info",
  streaming: "info",
  error: "error",
};

/** Plain text or pretty JSON for tool input/output payloads. */
function formatValue(value: unknown): string {
  if (typeof value === "string") return value;
  try {
    return JSON.stringify(value, null, 2) ?? String(value);
  } catch {
    return String(value);
  }
}

type ToolPart = Extract<FlueConversationPart, { type: "dynamic-tool" }>;

/** One tool call, showing its full lifecycle: pending, result, or failure. */
function ToolCall({ part }: { part: ToolPart }) {
  const tone =
    part.state === "output-available"
      ? "success"
      : part.state === "output-error"
        ? "error"
        : "info";
  const stateLabel =
    part.state === "output-available"
      ? "done"
      : part.state === "output-error"
        ? "failed"
        : "running";
  // Uncontrolled-by-lifecycle: `running` seeds the initial state only, so a
  // finished call does not snap shut under the user when the part updates.
  const running = part.state !== "output-available" && part.state !== "output-error";
  const [open, setOpen] = useState(running);
  return (
    <details
      open={open}
      onToggle={(event) => setOpen(event.currentTarget.open)}
      className="rounded-md border px-2 py-1.5 text-xs"
    >
      {/* Keep <summary> at default display: flex on it drops the disclosure
          triangle in WebKit; the inner row does the layout instead. */}
      <summary className="cursor-pointer">
        {/* inline-flex (not flex): the row must stay inline-level so the
            disclosure marker shares its line instead of sitting alone above. */}
        <span className="inline-flex flex-wrap items-center gap-2 align-middle">
          <span className="font-mono font-medium">{part.toolName}</span>
          <StatusChip label="tool" status={stateLabel} tone={tone} />
          {typeof part.durationMs === "number" && (
            <span className="text-muted-foreground">{part.durationMs} ms</span>
          )}
        </span>
      </summary>
      <pre className="mt-1 overflow-auto font-mono text-xs whitespace-pre-wrap text-muted-foreground">
        {formatValue(part.input)}
      </pre>
      {part.state === "output-available" && (
        <pre className="mt-1 overflow-auto font-mono text-xs whitespace-pre-wrap">
          {formatValue(part.output)}
        </pre>
      )}
      {part.state === "output-error" && (
        <pre className="mt-1 overflow-auto font-mono text-xs whitespace-pre-wrap text-destructive">
          {part.errorText}
        </pre>
      )}
    </details>
  );
}

function PartView({ part }: { part: FlueConversationPart }) {
  if (part.type === "text") {
    if (part.text === "") return null;
    return <p className="text-sm whitespace-pre-wrap">{part.text}</p>;
  }
  if (part.type === "reasoning") {
    return (
      <details className="rounded-md border px-2 py-1 text-xs text-muted-foreground">
        <summary className="cursor-pointer">reasoning</summary>
        <p className="mt-1 whitespace-pre-wrap">{part.text}</p>
      </details>
    );
  }
  if (part.type === "dynamic-tool") {
    return <ToolCall part={part} />;
  }
  if (part.type === "file") {
    if (!part.url) return null;
    return part.mediaType.startsWith("image/") ? (
      <img
        src={part.url}
        alt={part.filename ?? "attachment"}
        className="max-h-64 rounded-md border"
      />
    ) : (
      <a
        href={part.url}
        target="_blank"
        rel="noreferrer"
        className="text-link text-xs underline-offset-4 hover:underline"
      >
        {part.filename ?? "attachment"}
      </a>
    );
  }
  return null;
}

/** One visible transcript entry. Tool calls are parts of the assistant reply,
 *  so a single response renders its text and every call in stream order. */
function MessageView({ message }: { message: FlueConversationMessage }) {
  const user = message.role === "user";
  return (
    <Card size="sm" className={user ? "bg-muted/50" : undefined}>
      <CardContent>
        <div className="text-xs font-semibold tracking-wide text-muted-foreground uppercase">
          {message.role}
          {message.settlement ? ` · ${message.settlement.outcome}` : ""}
        </div>
        <div className="mt-1 grid gap-2">
          {message.parts.map((part, index) => (
            <PartView key={`${message.id}-${index}`} part={part} />
          ))}
        </div>
      </CardContent>
    </Card>
  );
}

export default function FlueAgentPage() {
  const [instanceId, setInstanceId] = useState(loadOrCreateFlueInstanceId);
  const [input, setInput] = useState("");
  const agent = useFlueAgent({ url: flueConversationUrl(instanceId) });
  const busy = agent.status === "submitted" || agent.status === "streaming";
  const visible = agent.messages.filter((message) => message.display === "visible");

  function startNewConversation() {
    // A fresh caller-chosen ID is a new conversation: Flue creates the
    // instance on the first admitted send. Persist before rendering so a
    // reload resumes the same instance instead of orphaning it.
    const next = newFlueInstanceId();
    localStorage.setItem(FLUE_INSTANCE_KEY, next);
    setInput("");
    setInstanceId(next);
  }

  async function send() {
    const text = input.trim();
    if (!text || busy) return;
    setInput("");
    try {
      await agent.sendMessage(text);
    } catch {
      // The optimistic row stays in the transcript and `failedSends` renders
      // the error, so the user can see what did not reach Flue.
    }
  }

  return (
    <div className="mx-auto max-w-3xl">
      <div className="flex flex-wrap items-center gap-2">
        <h1 className="mr-2 text-lg font-semibold">Flue agent</h1>
        <StatusChip label="status" status={agent.status} tone={STATUS_TONE[agent.status]} />
        <Button size="sm" variant="outline" onClick={agent.refresh} disabled={busy}>
          Refresh
        </Button>
        <Button size="sm" variant="outline" onClick={startNewConversation} disabled={busy}>
          New conversation
        </Button>
        {busy && <span className="text-sm text-muted-foreground">Working…</span>}
      </div>
      <p className="mt-1 text-xs text-muted-foreground">
        Instance <span className="font-mono">{instanceId}</span> · a separate Flue runtime
        behind the dashboard&apos;s <code>/flue</code> proxy (configure <code>FLUE_URL</code>,
        see web/README.md).
      </p>

      {agent.error && agent.failedSends.length === 0 && <ErrorBox error={agent.error} />}
      {agent.failedSends.length > 0 && (
        <div
          role="alert"
          className="my-2 grid gap-1 rounded-lg border border-destructive/40 bg-destructive/10 px-3 py-2 text-sm text-destructive"
        >
          {agent.failedSends.map((failed) => (
            <div key={failed.id}>
              <span className="font-medium">Send failed:</span> {failed.message} —{" "}
              {failed.error.message}
            </div>
          ))}
        </div>
      )}

      <section aria-label="Flue conversation" aria-live="polite" className="mt-3 grid gap-2">
        {visible.length === 0 && !agent.error && (
          <p className="text-sm text-muted-foreground">
            {agent.historyReady
              ? "No messages yet — ask the Flue household agent about your devices."
              : "Connecting to the Flue agent…"}
          </p>
        )}
        {visible.map((message) => (
          <MessageView key={message.id} message={message} />
        ))}
      </section>

      <div className="mt-3 flex flex-wrap items-end gap-2">
        <Textarea
          aria-label="Flue message"
          placeholder="Ask the Flue household agent… (Enter to send, Shift+Enter for newline)"
          className="min-w-64 flex-1"
          rows={3}
          value={input}
          disabled={busy}
          onChange={(event) => setInput(event.target.value)}
          onKeyDown={(event) => {
            if (event.key === "Enter" && !event.shiftKey) {
              event.preventDefault();
              void send();
            }
          }}
        />
        <Button onClick={() => void send()} disabled={busy || !input.trim()}>
          Send
        </Button>
      </div>
    </div>
  );
}
