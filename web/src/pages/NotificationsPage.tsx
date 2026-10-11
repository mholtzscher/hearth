import { useEffect, useState } from "react";
import { Link, useParams, useSearchParams } from "react-router-dom";
import { ApiError, useBaseUrlVersion } from "../api/client.ts";
import { useApi } from "../api/hooks.ts";
import { notificationsApi } from "../api/notifications.ts";
import { ErrorBox, Facts, StatusChip, linkClass } from "../components/common.tsx";
import { Button } from "../components/ui/button.tsx";
import { Input } from "../components/ui/input.tsx";
import { Label } from "../components/ui/label.tsx";
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "../components/ui/table.tsx";

function AttemptDetail({ id }: { id: string }) {
  const { data, error, loading, refresh } = useApi(`notification-${id}`, () => notificationsApi.attempt(id));
  const missing = error instanceof ApiError && error.status === 404;
  return <section className="mt-4 rounded-lg border p-3">
    <h2 className="font-semibold">Attempt {id}</h2>
    <Button variant="outline" size="sm" onClick={() => void refresh()}>Refresh attempt</Button>
    {loading && <p>Loading…</p>}
    {missing ? <p role="status">This notification attempt was not found or has been pruned after its 30-day retention. Any retained Automation outcome is unchanged.</p> : error && <ErrorBox error={error} />}
    {data && !error && <>
      <StatusChip status={data.status} tone={data.status === "accepted" ? "success" : data.status === "not_sent" ? "error" : undefined} />
      <p className="mt-2 text-sm">{data.status === "unknown" ? "Submission may have occurred; acceptance is unproven. Do not assume it is safe to resend." : data.status === "running" ? "Still in flight; no terminal outcome recorded yet." : data.status === "accepted" ? "Provider confirmed acceptance, not delivery to a recipient." : data.status === "not_sent" ? "No external submission occurred." : "Provider or adapter explicitly refused the request."}</p>
      <Facts rows={[
        ["Destination", <Link className={linkClass} to={`/notification-destinations/${data.destination_id}`}>{data.destination_name} ({data.destination_id})</Link>],
        ["Title", data.title || "—"], ["Message", <span className="whitespace-pre-wrap">{data.message}</span>],
        ["Started", data.started_at], ["Deadline", data.deadline], ["Completed", data.completed_at ?? "—"],
        ["Reason", data.reason_code ?? "—"], ["Provider receipt", data.receipt ?? "—"],
      ]} />
    </>}
  </section>;
}

export default function NotificationsPage() {
  const { attemptId } = useParams();
  const [params, setParams] = useSearchParams();
  const destination = params.get("destination_id") ?? "";
  const [filter, setFilter] = useState(destination);
  const [cursor, setCursor] = useState("");
  const baseVersion = useBaseUrlVersion();
  useEffect(() => { setCursor(""); setFilter(destination); }, [destination, baseVersion]);
  const { data, error, loading, refresh } = useApi(`notifications-${destination}-${cursor}`, () => notificationsApi.attempts(destination, cursor));
  return <div>
    <div className="flex flex-wrap items-center gap-3">
      <h1 className="text-lg font-semibold">Notification history</h1>
      <Link className={linkClass} to="/notification-destinations">Destinations and test send</Link>
      <Button variant="outline" size="sm" onClick={() => { setCursor(""); if (!cursor) void refresh(); }}>Refresh history</Button>
    </div>
    <p className="mt-2 text-sm text-muted-foreground">Manual and Automation attempts, newest first. History is independent of Automation retention; terminal attempts are retained for 30 days. All outcomes, including running and failed attempts, are listed.</p>
    {attemptId && <AttemptDetail key={attemptId} id={attemptId} />}
    <form className="mt-4 flex flex-wrap items-end gap-2" onSubmit={(e) => { e.preventDefault(); setCursor(""); setParams(filter.trim() ? { destination_id: filter.trim() } : {}); }}>
      <div className="grid gap-1"><Label htmlFor="notification-filter">Destination ID filter</Label><Input id="notification-filter" value={filter} onChange={(e) => setFilter(e.target.value)} /></div>
      <Button type="submit">Apply filter</Button>
      <Button variant="outline" type="button" onClick={() => { setFilter(""); setCursor(""); setParams({}); }}>Clear filter</Button>
    </form>
    {loading && <p>Loading…</p>}
    {error && <ErrorBox error={error} />}
    {data && <>
      <Table><TableHeader><TableRow><TableHead>Status</TableHead><TableHead>Destination snapshot</TableHead><TableHead>Title</TableHead><TableHead>Reason</TableHead><TableHead>Started</TableHead><TableHead>Attempt</TableHead></TableRow></TableHeader>
        <TableBody>{data.items.map((a) => <TableRow key={a.id}>
          <TableCell><StatusChip status={a.status} tone={a.status === "accepted" ? "success" : a.status === "not_sent" ? "error" : undefined} /></TableCell>
          <TableCell>{a.destination_name}</TableCell><TableCell>{a.title || "—"}</TableCell><TableCell>{a.reason_code ?? "—"}</TableCell><TableCell className="font-mono text-xs">{a.started_at}</TableCell>
          <TableCell><Link className={linkClass} to={`/notifications/${a.id}${destination ? `?destination_id=${encodeURIComponent(destination)}` : ""}`}>{a.id}</Link></TableCell>
        </TableRow>)}</TableBody></Table>
      {data.items.length === 0 && <p>No notification attempts match.</p>}
      {data.next_cursor && <Button disabled={loading} onClick={() => setCursor(data.next_cursor!)}>Next page</Button>}
    </>}
  </div>;
}
