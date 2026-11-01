import { createContext, useCallback, useContext, useEffect, useRef, useState, type ReactNode } from "react";
import { useBlocker, useLocation, useNavigate } from "react-router-dom";
import { ApiError } from "../../api/client";
import { createDashboard, getDashboard, updateDashboard } from "../../api/dashboards";
import { readDefinition } from "../views/catalog/compatibility";
import { draftContext, editDraft, fromArtifact, fromDashboard, type Draft } from "./draft";
import type { PresentedArtifact } from "../conversation/runtime";
import type { ViewDefinition } from "../views/catalog";
import { Dialog } from "./Dialog";

type WorkspaceValue = {
  draft: Draft | null; error: string; saving: boolean;
  selectArtifact: (artifact: PresentedArtifact, onSelected?: () => void) => void;
  receiveArtifact: (artifact: PresentedArtifact, expectedDefinition: unknown) => void;
  openDashboard: (id: string) => Promise<void>;
  edit: (definition: ViewDefinition) => void;
  save: (asNew?: boolean) => Promise<boolean>;
  reload: () => Promise<void>;
  discard: () => Promise<void>;
  customize: () => void;
  navigateRetainingDraft: (path: string) => void;
  context: ReturnType<typeof draftContext>;
};
const Context = createContext<WorkspaceValue | null>(null);
export function useWorkspace() { const value = useContext(Context); if (!value) throw new Error("Workspace is missing"); return value; }

export function WorkspaceProvider({ children }: { children: ReactNode }) {
  const [draft, setDraft] = useState<Draft | null>(null);
  const [error, setError] = useState("");
  const [saving, setSaving] = useState(false);
  const [pending, setPending] = useState<(() => void) | null>(null);
  const allowNavigation = useRef(false);
  const navigate = useNavigate();
  const location = useLocation();
  const blocker = useBlocker(({ currentLocation, nextLocation }) => {
    if (allowNavigation.current) { allowNavigation.current = false; return false; }
    return !!draft?.dirty && currentLocation.pathname !== nextLocation.pathname;
  });
  useEffect(() => {
    if (!draft?.dirty) return;
    const beforeUnload = (event: BeforeUnloadEvent) => { event.preventDefault(); event.returnValue = ""; };
    window.addEventListener("beforeunload", beforeUnload);
    return () => window.removeEventListener("beforeunload", beforeUnload);
  }, [draft?.dirty]);

  const navigateRetainingDraft = useCallback((path: string) => {
    if (location.pathname === path) return;
    allowNavigation.current = true;
    navigate(path);
  }, [navigate, location.pathname]);
  function choose(action: () => void) {
    if (draft?.dirty) setPending(() => action);
    else action();
  }
  async function openDashboard(id: string) {
    try { setDraft(fromDashboard(await getDashboard(id))); setError(""); }
    catch (err) { setError(err instanceof Error ? err.message : "Could not load dashboard."); }
  }
  function selectArtifact(item: PresentedArtifact, onSelected?: () => void) {
    choose(() => { setDraft(fromArtifact(item, draft)); setError(""); onSelected?.(); });
  }
  function receiveArtifact(item: PresentedArtifact, expectedDefinition: unknown) {
    setDraft(current => JSON.stringify(current?.definition) === JSON.stringify(expectedDefinition) ? fromArtifact(item, current) : current);
  }
  async function save(asNew = false): Promise<boolean> {
    if (!draft) return false;
    const view = readDefinition(draft.definition);
    if (view.status !== "supported") { setError("This view contains unsupported content and cannot be saved."); return false; }
    setSaving(true); setError("");
    try {
      const result = !asNew && draft.base ? await updateDashboard(draft.base.dashboard_id, draft.base.revision, view.definition) : await createDashboard(view.definition);
      setDraft(fromDashboard(result));
      if (asNew && location.pathname.startsWith("/dashboards/")) navigateRetainingDraft(`/dashboards/${encodeURIComponent(result.id)}`);
      return true;
    } catch (err) {
      setError(err instanceof ApiError && err.status === 409 ? "This dashboard changed elsewhere. Reload the saved version or save this draft as new." : err instanceof ApiError && err.status === 404 ? "The saved dashboard no longer exists. Save this draft as new." : err instanceof Error ? err.message : "Could not save dashboard.");
      return false;
    } finally { setSaving(false); }
  }
  async function reload() {
    if (!draft?.base) return;
    await openDashboard(draft.base.dashboard_id);
  }
  async function discard() {
    if (draft?.base) {
      try { setDraft(fromDashboard(await getDashboard(draft.base.dashboard_id))); setError(""); }
      catch (err) { setError(err instanceof Error ? err.message : "Could not reload dashboard."); }
    } else { setDraft(null); setError(""); }
  }
  const blocked = blocker.state === "blocked";
  function proceed() {
    if (blocked) blocker.proceed();
    else if (pending) pending();
    setPending(null);
  }
  function stay() { if (blocked) blocker.reset(); setPending(null); }
  return <Context.Provider value={{ draft, error, saving, selectArtifact, receiveArtifact, openDashboard, edit: definition => setDraft(current => current ? editDraft(current, definition) : current), save, reload, discard, customize: () => navigateRetainingDraft("/"), navigateRetainingDraft, context: draftContext(draft) }}>
    {children}
    {(blocked || pending) && <Dialog title="Keep your changes?" onClose={stay}><p>This draft has unsaved changes. Save it, discard it, or stay here.</p>{error && <p role="alert">{error}</p>}<div className="workspace-actions"><button onClick={stay}>Stay</button><button onClick={() => { setDraft(null); proceed(); }}>Discard</button><button className="primary" disabled={saving || !draftContext(draft)} onClick={() => void save().then(ok => { if (ok) proceed(); })}>Save</button></div></Dialog>}
  </Context.Provider>;
}
