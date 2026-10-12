import { useEffect, useState } from "react";
import { Link } from "react-router-dom";
import { useBaseUrlVersion } from "../api/client.ts";
import { useApi } from "../api/hooks.ts";
import { notificationsApi } from "../api/notifications.ts";
import { ErrorBox, StatusChip, linkClass } from "../components/common.tsx";
import { Button } from "../components/ui/button.tsx";
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "../components/ui/table.tsx";

export default function NotificationDestinationsPage() {
  const [cursor, setCursor] = useState("");
  const baseVersion = useBaseUrlVersion();
  useEffect(() => setCursor(""), [baseVersion]);
  const { data, error, loading, refresh } = useApi(`notification-destinations-${cursor}`, () => notificationsApi.destinations(cursor));
  return <div>
    <div className="flex flex-wrap items-center gap-3">
      <h1 className="text-lg font-semibold">Notification destinations</h1>
      <Button variant="outline" size="sm" onClick={() => { setCursor(""); if (!cursor) void refresh(); }}>Refresh destinations</Button>
      <Link to="/notifications" className={linkClass}>Notification history</Link>
    </div>
    <p className="mt-2 text-sm text-muted-foreground">Select an adapter-registered destination to send a test message. Availability is current Core routing evidence, not a provider connection guarantee.</p>
    {loading && <p>Loading…</p>}
    {error && <ErrorBox error={error} />}
    {data && <>
      <Table><TableHeader><TableRow><TableHead>Name</TableHead><TableHead>Availability</TableHead><TableHead>ID</TableHead></TableRow></TableHeader>
        <TableBody>{data.items.map((d) => <TableRow key={d.id}>
          <TableCell><Link className={linkClass} to={`/notification-destinations/${d.id}`}>{d.name}</Link></TableCell>
          <TableCell><StatusChip status={d.available ? "available" : "unavailable"} /></TableCell>
          <TableCell className="font-mono text-xs">{d.id}</TableCell>
        </TableRow>)}</TableBody></Table>
      {data.items.length === 0 && <p>No notification destinations registered.</p>}
      {data.next_cursor && <Button disabled={loading} onClick={() => setCursor(data.next_cursor!)}>Next page</Button>}
    </>}
  </div>;
}
