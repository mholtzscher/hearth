import { useEffect, useState } from "react";
import { Link } from "react-router-dom";
import { deleteDashboard, listDashboards, type DashboardSummary } from "../../api/dashboards";
import { useBaseUrlVersion } from "../../api/client";
import { Dialog } from "./Dialog";

export function DashboardLibrary() {
  const [items, setItems] = useState<DashboardSummary[]>([]);
  const [cursor, setCursor] = useState<string | null>(null);
  const [error, setError] = useState("");
  const [confirming, setConfirming] = useState<DashboardSummary | null>(null);
  const [loading, setLoading] = useState(true);
  const backendVersion = useBaseUrlVersion();
  async function load(next?: string) {
    setLoading(true); setError("");
    try { const page = await listDashboards(next); setItems(previous => next ? [...previous, ...page.items] : page.items); setCursor(page.next_cursor ?? null); }
    catch (err) { setError(err instanceof Error ? err.message : "Could not load dashboards."); }
    finally { setLoading(false); }
  }
  useEffect(() => { void load(); }, [backendVersion]);
  async function remove(item: DashboardSummary) {
    try { await deleteDashboard(item.id, item.revision); setConfirming(null); await load(); }
    catch (err) { setError(err instanceof Error ? `${err.message} Reload the library before trying again.` : "Could not delete dashboard."); setConfirming(null); }
  }
  return <section className="library"><p className="eyebrow">Your dashboards</p><h1>Made for you. Here to stay.</h1><p>Useful views, kept close and connected to your home.</p>
    {error && <div className="workspace-notice" role="alert">{error} <button onClick={() => void load()}>Reload</button></div>}
    {!loading && items.length === 0 && <div className="library-empty"><h2>Your first dashboard starts with a question.</h2><p>Generate a view in conversation, customize it, and save it here.</p><Link to="/">Start a conversation →</Link></div>}
    <div className="library-grid">{items.map(item => <article className="library-card" key={item.id}><span className="artifact-icon">◫</span><h2>{item.title}</h2><p>Revision {item.revision} · Updated {new Date(item.updated_at).toLocaleDateString()}</p><div className="workspace-actions"><Link to={`/dashboards/${encodeURIComponent(item.id)}`}>Open dashboard →</Link><button aria-label={`Delete ${item.title}`} onClick={() => setConfirming(item)}>Delete</button></div></article>)}</div>
    {cursor && <button disabled={loading} onClick={() => void load(cursor)}>Load more</button>}{loading && <p role="status">Loading dashboards…</p>}
    {confirming && <Dialog title="Remove this dashboard?" onClose={() => setConfirming(null)}><p>{confirming.title} will be deleted. Your home data and conversation remain unchanged.</p><div className="workspace-actions"><button onClick={() => setConfirming(null)}>Cancel</button><button className="primary" onClick={() => void remove(confirming)}>Delete dashboard</button></div></Dialog>}
  </section>;
}
