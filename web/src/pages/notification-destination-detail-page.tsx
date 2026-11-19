import { useRef, useState } from "react";
import { Link, useParams } from "react-router-dom";

import { useBaseUrlVersion } from "../api/client.ts";
import { useApi } from "../api/hooks.ts";
import { notificationsApi } from "../api/notifications.ts";
import type { NotificationAttempt } from "../api/notifications.ts";
import { Button } from "../components/button.tsx";
import { ErrorBox } from "../components/error-box.tsx";
import { Input } from "../components/input.tsx";
import { Label } from "../components/label.tsx";
import { linkClass } from "../components/link-styles.ts";
import { PageHeader } from "../components/page-header.tsx";
import { StatusChip } from "../components/status-chip.tsx";
import { Textarea } from "../components/textarea.tsx";

const attemptTone: Partial<
  Record<NotificationAttempt["status"], "success" | "error">
> = {
  accepted: "success",
  not_sent: "error",
};

const toneForAttempt = (status: NotificationAttempt["status"]) =>
  attemptTone[status];

const DestinationDetail = ({ id }: { id: string }) => {
  const { data, error, loading, refresh } = useApi(
    `notification-destination-${id}`,
    () => notificationsApi.destination(id)
  );
  const [title, setTitle] = useState("");
  const [message, setMessage] = useState("");
  const [sending, setSending] = useState(false);
  const inFlight = useRef(false);
  const [attempt, setAttempt] = useState<NotificationAttempt | null>(null);
  const [sendError, setSendError] = useState<Error | null>(null);
  const valid =
    message.trim().length > 0 &&
    [...message].length <= 2000 &&
    [...title].length <= 256;

  const send = async () => {
    if (inFlight.current || !valid) {
      return;
    }
    inFlight.current = true;
    setSending(true);
    setSendError(null);
    setAttempt(null);
    try {
      setAttempt(await notificationsApi.send(id, title, message));
    } catch (error_) {
      setSendError(
        error_ instanceof Error ? error_ : new Error(String(error_))
      );
    }
    inFlight.current = false;
    setSending(false);
  };

  return (
    <div className="max-w-3xl">
      <Link className={linkClass} to="/notification-destinations">
        All destinations
      </Link>
      <PageHeader
        title={data?.name ?? "Notification destination"}
        description={
          <span className="text-label font-mono break-all">{id}</span>
        }
      >
        <Button
          variant="outline"
          onClick={async () => {
            await refresh();
          }}
        >
          Refresh destination
        </Button>
      </PageHeader>
      {loading && <p>Loading…</p>}
      {error && <ErrorBox error={error} />}
      {data && (
        <>
          <StatusChip status={data.available ? "available" : "unavailable"} />
          {!data.available && (
            <p className="text-label mt-2">
              Currently unavailable. A test send will still record a not-sent
              attempt if no route is available.
            </p>
          )}
          <form
            className="mt-4 grid gap-2"
            onSubmit={async (e) => {
              e.preventDefault();
              await send();
            }}
          >
            <Label htmlFor="notification-title">
              Title (optional, up to 256 characters)
            </Label>
            <Input
              id="notification-title"
              value={title}
              disabled={sending}
              onChange={(e) => setTitle(e.target.value)}
            />
            <Label htmlFor="notification-message">
              Message (required, up to 2000 characters)
            </Label>
            <Textarea
              id="notification-message"
              className="min-h-28"
              value={message}
              disabled={sending}
              onChange={(e) => setMessage(e.target.value)}
            />
            <p className="text-muted-foreground text-label">
              Each click sends a new attempt. No automatic retries. An unknown
              outcome does not prove non-delivery.
            </p>
            <Button
              className="justify-self-start"
              type="submit"
              disabled={sending || !valid}
            >
              {sending ? "Sending…" : "Send test message"}
            </Button>
          </form>
        </>
      )}
      {sendError && (
        <>
          <ErrorBox error={sendError} />
          <output className="block">
            The result could not be confirmed. No retry was made. Check
            notification history before sending again; another send may
            duplicate the message.
          </output>
        </>
      )}
      {attempt && (
        <output className="mt-4 block">
          <StatusChip
            status={attempt.status}
            tone={toneForAttempt(attempt.status)}
          />{" "}
          {attempt.reason_code}
          <p>
            <Link className={linkClass} to={`/notifications/${attempt.id}`}>
              Inspect attempt {attempt.id}
            </Link>
          </p>
        </output>
      )}
      <p className="mt-4">
        <Link
          className={linkClass}
          to={`/notifications?destination_id=${encodeURIComponent(id)}`}
        >
          History for this destination
        </Link>
      </p>
    </div>
  );
};

const NotificationDestinationDetailPage = () => {
  const { destinationId = "" } = useParams();
  const version = useBaseUrlVersion();
  return (
    <DestinationDetail key={`${version}-${destinationId}`} id={destinationId} />
  );
};

export default NotificationDestinationDetailPage;
