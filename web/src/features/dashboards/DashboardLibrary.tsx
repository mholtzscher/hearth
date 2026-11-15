import { useEffect, useState } from "react";
import { Link } from "react-router-dom";
import { deleteDashboard, listDashboards, type DashboardSummary } from "../../api/dashboards";
import { useBaseUrlVersion } from "../../api/client";
import { Button } from "../../components/ui/button";
import { Notice } from "../../components/ui/notice";
import { DecisionDialog } from "../../components/DecisionDialog";

export function DashboardLibrary() {
  const backendVersion = useBaseUrlVersion();
  return <ConnectedLibrary key={backendVersion} />;
}

function ConnectedLibrary() {
  const [items, setItems] = useState<DashboardSummary[]>([]);
  const [cursor, setCursor] = useState<string | null>(null);
  const [error, setError] = useState("");
  const [confirming, setConfirming] = useState<DashboardSummary | null>(null);
  const [loading, setLoading] = useState(true);
  async function load(next?: string) {
    setLoading(true); setError("");
    try { const page = await listDashboards(next); setItems(previous => next ? [...previous, ...page.items] : page.items); setCursor(page.next_cursor ?? null); }
    catch (err) { setError(err instanceof Error ? err.message : "Could not load dashboards."); }
    finally { setLoading(false); }
  }
  useEffect(() => { void load(); }, []);
  async function remove(item: DashboardSummary) {
    try { await deleteDashboard(item.id, item.revision); setConfirming(null); await load(); }
    catch (err) { setError(err instanceof Error ? `${err.message} Reload the library before trying again.` : "Could not delete dashboard."); setConfirming(null); }
  }
  return <section className="flex-1 overflow-auto p-[34px] max-tablet:px-5 max-tablet:py-6"><p className="text-[10px] font-[650] tracking-[.13em] uppercase text-muted-foreground">Your dashboards</p><h1 className="text-headline leading-[1.3] font-medium tracking-[-.03em] text-balance [overflow-wrap:anywhere]">Made for you. Here to stay.</h1><p className="mt-2 mb-[25px] text-muted-foreground">Useful views, kept close and connected to your home.</p>
    {error && <Notice role="alert">{error} <Button variant="outline" size="touch" className="m-1.5" onClick={() => void load()}>Reload</Button></Notice>}
    {!loading && items.length === 0 && <div className="rounded-card border border-border bg-card p-5"><h2 className="mt-3.5 mb-1 text-label">Your first dashboard starts with a question.</h2><p className="mb-4 text-caption text-muted-foreground">Generate a view in conversation, customize it, and save it here.</p><Link className="text-link" to="/">Start a conversation →</Link></div>}
    <div className="grid grid-cols-[repeat(auto-fill,minmax(250px,1fr))] gap-3.5">{items.map(item => <article className="rounded-card border border-border bg-card p-5" key={item.id}><span aria-hidden="true">◫</span><h2 className="mt-3.5 mb-1 text-label">{item.title}</h2><p className="mb-4 text-caption text-muted-foreground">Revision {item.revision} · Updated {new Date(item.updated_at).toLocaleDateString()}</p><div className="flex flex-wrap items-center gap-2"><Button asChild variant="outline" size="touch"><Link to={`/dashboards/${encodeURIComponent(item.id)}`}>Open dashboard →</Link></Button><Button variant="outline" size="touch" aria-label={`Delete ${item.title}`} onClick={() => setConfirming(item)}>Delete</Button></div></article>)}</div>
    {cursor && <Button variant="outline" size="touch" disabled={loading} onClick={() => void load(cursor)}>Load more</Button>}{loading && <p role="status">Loading dashboards…</p>}
    {confirming && <DecisionDialog title="Remove this dashboard?" onClose={() => setConfirming(null)}><p className="mt-[14px] mb-5 text-muted-foreground">{confirming.title} will be deleted. Your home data and conversation remain unchanged.</p><div className="flex flex-wrap justify-end gap-2"><Button variant="outline" size="touch" onClick={() => setConfirming(null)}>Cancel</Button><Button size="touch" onClick={() => void remove(confirming)}>Delete dashboard</Button></div></DecisionDialog>}
  </section>;
}
