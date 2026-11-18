import { useLayoutEffect, useRef, useState } from "react";
import { Link, useParams, useSearchParams } from "react-router-dom";

import { ApiError, useBaseUrlVersion } from "../api/client.ts";
import { useApi } from "../api/hooks.ts";
import { notificationsApi } from "../api/notifications.ts";
import type { NotificationAttempt } from "../api/notifications.ts";
import { Button } from "../components/button.tsx";
import { Card } from "../components/card.tsx";
import { EmptyRow } from "../components/empty-row.tsx";
import { ErrorBox } from "../components/error-box.tsx";
import { Facts } from "../components/facts.tsx";
import { Input } from "../components/input.tsx";
import { Label } from "../components/label.tsx";
import { linkClass } from "../components/link-styles.ts";
import { PageHeader } from "../components/page-header.tsx";
import { StatusChip } from "../components/status-chip.tsx";
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from "../components/table.tsx";

const attemptTone = (status: NotificationAttempt["status"]) => {
  if (status === "accepted") {
    return "success";
  }
  if (status === "not_sent") {
    return "error";
  }
};

const attemptOutcome = (status: NotificationAttempt["status"]) => {
  switch (status) {
    case "unknown": {
      return "Submission may have occurred; acceptance is unproven. Do not assume it is safe to resend.";
    }
    case "running": {
      return "Still in flight; no terminal outcome recorded yet.";
    }
    case "accepted": {
      return "Provider confirmed acceptance, not delivery to a recipient.";
    }
    case "not_sent": {
      return "No external submission occurred.";
    }
    default: {
      return "Provider or adapter explicitly refused the request.";
    }
  }
};

const AttemptDetail = ({ id }: { id: string }) => {
  const { data, error, loading, refresh } = useApi(`notification-${id}`, () =>
    notificationsApi.attempt(id)
  );
  const missing = error instanceof ApiError && error.status === 404;
  return (
    <Card asChild size="compact" className="mt-4">
      <section>
        <h2 className="font-semibold">Attempt {id}</h2>
        <Button
          variant="outline"
          size="default"
          onClick={() => {
            void refresh();
          }}
        >
          Refresh attempt
        </Button>
        {loading && <p>Loading…</p>}
        {missing ? (
          <output className="block">
            This notification attempt was not found or has been pruned after its
            30-day retention. Any retained Automation outcome is unchanged.
          </output>
        ) : (
          error && <ErrorBox error={error} />
        )}
        {data && !error && (
          <>
            <StatusChip status={data.status} tone={attemptTone(data.status)} />
            <p className="text-label mt-2">{attemptOutcome(data.status)}</p>
            <Facts
              rows={[
                [
                  "Destination",
                  <Link
                    key="destination"
                    className={linkClass}
                    to={`/notification-destinations/${data.destination_id}`}
                  >
                    {data.destination_name} ({data.destination_id})
                  </Link>,
                ],
                ["Title", data.title || "—"],
                [
                  "Message",
                  <span key="message" className="whitespace-pre-wrap">
                    {data.message}
                  </span>,
                ],
                ["Started", data.started_at],
                ["Deadline", data.deadline],
                ["Completed", data.completed_at ?? "—"],
                ["Reason", data.reason_code ?? "—"],
                ["Provider receipt", data.receipt ?? "—"],
              ]}
            />
          </>
        )}
      </section>
    </Card>
  );
};

const NotificationsHistory = ({
  destination,
  setParams,
  refreshRef,
}: {
  destination: string;
  setParams: ReturnType<typeof useSearchParams>[1];
  refreshRef: { current: (() => void) | null };
}) => {
  const [filter, setFilter] = useState(destination);
  const [cursor, setCursor] = useState("");
  const { data, error, loading, refresh } = useApi(
    `notifications-${destination}-${cursor}`,
    () => notificationsApi.attempts(destination, cursor)
  );
  const nextCursor = data?.next_cursor ?? "";
  // Event-time latest callback: the header owns the button, this scope owns the
  // cursor and refresh identity. Assignment happens in the commit phase.
  useLayoutEffect(() => {
    refreshRef.current = () => {
      setCursor("");
      if (!cursor) {
        void refresh();
      }
    };
  }, [cursor, refresh, refreshRef]);
  return (
    <>
      <form
        className="mt-4 flex flex-wrap items-end gap-2"
        onSubmit={(e) => {
          e.preventDefault();
          setCursor("");
          setParams(filter.trim() ? { destination_id: filter.trim() } : {});
        }}
      >
        <div className="grid gap-1">
          <Label htmlFor="notification-filter">Destination ID filter</Label>
          <Input
            id="notification-filter"
            value={filter}
            onChange={(e) => setFilter(e.target.value)}
          />
        </div>
        <Button type="submit" variant="outline">
          Apply filter
        </Button>
        <Button
          variant="outline"
          type="button"
          onClick={() => {
            setFilter("");
            setCursor("");
            setParams({});
          }}
        >
          Clear filter
        </Button>
      </form>
      {loading && <p>Loading…</p>}
      {error && <ErrorBox error={error} />}
      {data && (
        <>
          <Table aria-label="Notification history">
            <TableHeader>
              <TableRow>
                <TableHead>Status</TableHead>
                <TableHead>Destination snapshot</TableHead>
                <TableHead>Title</TableHead>
                <TableHead>Reason</TableHead>
                <TableHead>Started</TableHead>
                <TableHead>Attempt</TableHead>
              </TableRow>
            </TableHeader>
            <TableBody>
              {data.items.length === 0 && (
                <EmptyRow
                  colSpan={6}
                  message="No notification attempts match."
                />
              )}
              {data.items.map((a) => (
                <TableRow key={a.id}>
                  <TableCell>
                    <StatusChip
                      status={a.status}
                      tone={attemptTone(a.status)}
                    />
                  </TableCell>
                  <TableCell>{a.destination_name}</TableCell>
                  <TableCell>{a.title || "—"}</TableCell>
                  <TableCell>{a.reason_code ?? "—"}</TableCell>
                  <TableCell className="text-caption font-mono">
                    {a.started_at}
                  </TableCell>
                  <TableCell>
                    <Link
                      className={linkClass}
                      to={`/notifications/${a.id}${destination ? `?destination_id=${encodeURIComponent(destination)}` : ""}`}
                    >
                      {a.id}
                    </Link>
                  </TableCell>
                </TableRow>
              ))}
            </TableBody>
          </Table>
          {nextCursor && (
            <Button
              variant="outline"
              disabled={loading}
              onClick={() => setCursor(nextCursor)}
            >
              Next page
            </Button>
          )}
        </>
      )}
    </>
  );
};

export default function NotificationsPage() {
  const { attemptId } = useParams();
  const [params, setParams] = useSearchParams();
  const destination = params.get("destination_id") ?? "";
  const baseVersion = useBaseUrlVersion();
  const refreshRef = useRef<(() => void) | null>(null);
  return (
    <div>
      <PageHeader
        title="Notification history"
        description="Manual and Automation attempts, newest first. History is independent of Automation retention; terminal attempts are retained for 30 days. All outcomes, including running and failed attempts, are listed."
      >
        <Link className={linkClass} to="/notification-destinations">
          Destinations and test send
        </Link>
        <Button
          variant="outline"
          size="default"
          onClick={() => {
            refreshRef.current?.();
          }}
        >
          Refresh history
        </Button>
      </PageHeader>
      {attemptId && <AttemptDetail key={attemptId} id={attemptId} />}
      <NotificationsHistory
        key={`${baseVersion}:${destination}`}
        destination={destination}
        setParams={setParams}
        refreshRef={refreshRef}
      />
    </div>
  );
}
