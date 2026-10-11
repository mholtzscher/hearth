import { useRef, useState } from "react";
import { Link, useParams } from "react-router-dom";
import { useBaseUrlVersion } from "../api/client.ts";
import { useApi } from "../api/hooks.ts";
import { notificationsApi, type NotificationAttempt } from "../api/notifications.ts";
import { ErrorBox, StatusChip, linkClass } from "../components/common.tsx";
import { Button } from "../components/ui/button.tsx";
import { Input } from "../components/ui/input.tsx";
import { Label } from "../components/ui/label.tsx";

function DestinationDetail({ id }: { id: string }) {
  const { data, error, loading, refresh } = useApi(`notification-destination-${id}`, () => notificationsApi.destination(id));
  const [title, setTitle] = useState("");
  const [message, setMessage] = useState("");
  const [sending, setSending] = useState(false);
  const inFlight = useRef(false);
  const [attempt, setAttempt] = useState<NotificationAttempt | null>(null);
  const [sendError, setSendError] = useState<Error | null>(null);
  const valid = message.trim().length > 0 && Array.from(message).length <= 2000 && Array.from(title).length <= 256;

  async function send() {
    if (inFlight.current || !valid) return;
    inFlight.current = true;
    setSending(true);
    setSendError(null);
    setAttempt(null);
    try { setAttempt(await notificationsApi.send(id, title, message)); }
    catch (e) { setSendError(e instanceof Error ? e : new Error(String(e))); }
    finally { inFlight.current = false; setSending(false); }
  }

  return <div className="max-w-3xl">
    <Link className={linkClass} to="/notification-destinations">All destinations</Link>
    <h1 className="mt-3 text-lg font-semibold">{data?.name ?? "Notification destination"}</h1>
    <p className="font-mono text-xs">{id}</p>
    <Button className="mt-2" variant="outline" size="sm" onClick={() => void refresh()}>Refresh destination</Button>
    {loading && <p>Loading…</p>}
    {error && <ErrorBox error={error} />}
    {data && <>
      <StatusChip status={data.available ? "available" : "unavailable"} />
      {!data.available && <p className="mt-2 text-sm">Currently unavailable. A test send will still record a not-sent attempt if no route is available.</p>}
      <form className="mt-4 grid gap-2" onSubmit={(e) => { e.preventDefault(); void send(); }}>
        <Label htmlFor="notification-title">Title (optional, up to 256 characters)</Label>
        <Input id="notification-title" value={title} disabled={sending} onChange={(e) => setTitle(e.target.value)} />
        <Label htmlFor="notification-message">Message (required, up to 2000 characters)</Label>
        <textarea id="notification-message" className="min-h-28 rounded-md border bg-background p-2 text-sm" value={message} disabled={sending} onChange={(e) => setMessage(e.target.value)} />
        <p className="text-sm text-muted-foreground">Each click sends a new attempt. No automatic retries. An unknown outcome does not prove non-delivery.</p>
        <Button className="justify-self-start" type="submit" disabled={sending || !valid}>{sending ? "Sending…" : "Send test message"}</Button>
      </form>
    </>}
    {sendError && <><ErrorBox error={sendError} /><p role="status">The result could not be confirmed. No retry was made. Check notification history before sending again; another send may duplicate the message.</p></>}
    {attempt && <div className="mt-4" role="status"><StatusChip status={attempt.status} tone={attempt.status === "accepted" ? "success" : attempt.status === "not_sent" ? "error" : undefined} /> {attempt.reason_code}
      <p><Link className={linkClass} to={`/notifications/${attempt.id}`}>Inspect attempt {attempt.id}</Link></p>
    </div>}
    <p className="mt-4"><Link className={linkClass} to={`/notifications?destination_id=${encodeURIComponent(id)}`}>History for this destination</Link></p>
  </div>;
}

export default function NotificationDestinationDetailPage() {
  const { destinationId = "" } = useParams();
  const version = useBaseUrlVersion();
  return <DestinationDetail key={`${version}-${destinationId}`} id={destinationId} />;
}
