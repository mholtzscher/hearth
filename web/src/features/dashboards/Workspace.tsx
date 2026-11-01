import { createContext, useCallback, useContext, useEffect, useRef, useState, type ReactNode } from "react";
import { useBlocker, useLocation, useNavigate } from "react-router-dom";
import { ApiError, getBaseUrl, setBaseUrl } from "../../api/client";
import { createDashboard, getDashboard, updateDashboard } from "../../api/dashboards";
import { readDefinition } from "../views/catalog/compatibility";
import { draftContext, editDraft, fromArtifact, fromDashboard, type Draft } from "./draft";
import type { PresentedArtifact } from "../conversation/runtime";
import type { ViewDefinition } from "../views/catalog";
import { Dialog } from "./Dialog";

type WorkspaceValue = {
  draft: Draft | null; error: string; saving: boolean;
  editorValid: boolean; setEditorValid: (valid: boolean) => void; editorEpoch: number;
  selectArtifact: (artifact: PresentedArtifact, onSelected?: () => void) => void;
  receiveArtifact: (artifact: PresentedArtifact, expectedDefinition: unknown) => void;
  openDashboard: (id: string) => Promise<void>;
  edit: (definition: ViewDefinition) => void;
  save: (asNew?: boolean) => Promise<boolean>;
  reload: () => Promise<void>;
  discard: () => Promise<void>;
  customize: () => void;
  navigateRetainingDraft: (path: string) => void;
  changeBackend: (url: string) => void;
  startFresh: (action: () => void) => void;
  context: ReturnType<typeof draftContext>;
};
const Context = createContext<WorkspaceValue | null>(null);
export function useWorkspace() { const value = useContext(Context); if (!value) throw new Error("Workspace is missing"); return value; }

export function WorkspaceProvider({ children }: { children: ReactNode }) {
  const [draft, setDraft] = useState<Draft | null>(null);
  const [error, setError] = useState("");
  const [saving, setSaving] = useState(false);
  const [editorValid, setEditorValid] = useState(true);
  const [editorEpoch, setEditorEpoch] = useState(0);
  const editorValidRef = useRef(true);
  editorValidRef.current = editorValid;
  const savingRef = useRef(false);
  savingRef.current = saving;
  const [pending, setPending] = useState<(() => void) | null>(null);
  const allowNavigation = useRef(false);
  const loadSequence = useRef(0);
  const navigate = useNavigate();
  const location = useLocation();
  const locationRef = useRef(location.pathname);
  locationRef.current = location.pathname;
  const blocker = useBlocker(({ currentLocation, nextLocation }) => {
    if (allowNavigation.current) { allowNavigation.current = false; return false; }
    return (!!draft?.dirty || !editorValid || saving) && currentLocation.pathname !== nextLocation.pathname;
  });
  useEffect(() => {
    if (!draft?.dirty && editorValid && !saving) return;
    const beforeUnload = (event: BeforeUnloadEvent) => { event.preventDefault(); event.returnValue = ""; };
    window.addEventListener("beforeunload", beforeUnload);
    return () => window.removeEventListener("beforeunload", beforeUnload);
  }, [draft?.dirty, editorValid, saving]);

  const navigateRetainingDraft = useCallback((path: string) => {
    if (location.pathname === path) return;
    allowNavigation.current = true;
    navigate(path);
  }, [navigate, location.pathname]);
  function choose(action: () => void) {
    if (saving) return;
    if (draft?.dirty || !editorValid) setPending(() => action);
    else action();
  }
  function resetEditor() { setEditorValid(true); setEditorEpoch(epoch => epoch + 1); }
  async function openDashboard(id: string) {
    const sequence = ++loadSequence.current;
    const path = location.pathname;
    if (path.startsWith("/dashboards/") && draft?.base?.dashboard_id !== id) setDraft(null);
    try { const dashboard = await getDashboard(id); if (sequence === loadSequence.current && path === locationRef.current) { setDraft(fromDashboard(dashboard)); resetEditor(); setError(""); } }
    catch (err) { if (sequence === loadSequence.current && path === locationRef.current) setError(err instanceof Error ? err.message : "Could not load dashboard."); }
  }
  function selectArtifact(item: PresentedArtifact, onSelected?: () => void) {
    choose(() => { setDraft(fromArtifact(item, draft)); resetEditor(); setError(""); onSelected?.(); });
  }
  function receiveArtifact(item: PresentedArtifact, expectedDefinition: unknown) {
    if (savingRef.current || !editorValidRef.current) return;
    setDraft(current => JSON.stringify(current?.definition) === JSON.stringify(expectedDefinition) ? fromArtifact(item, current) : current);
  }
  async function save(asNew = false): Promise<boolean> {
    if (!draft || !editorValid) return false;
    const view = readDefinition(draft.definition);
    if (view.status !== "supported") { setError("This view contains unsupported content and cannot be saved."); return false; }
    setSaving(true); setError("");
    try {
      const result = !asNew && draft.base ? await updateDashboard(draft.base.dashboard_id, draft.base.revision, view.definition) : await createDashboard(view.definition);
      setDraft(fromDashboard(result));
      if (asNew && !pending && blocker.state !== "blocked" && location.pathname.startsWith("/dashboards/")) navigateRetainingDraft(`/dashboards/${encodeURIComponent(result.id)}`);
      return true;
    } catch (err) {
      setError(err instanceof ApiError && err.status === 409 ? "This dashboard changed elsewhere. Reload the saved version or save this draft as new." : err instanceof ApiError && err.status === 404 ? "The saved dashboard no longer exists. Save this draft as new." : err instanceof Error ? err.message : "Could not save dashboard.");
      return false;
    } finally { setSaving(false); }
  }
  async function reload() {
    if (!draft?.base) return;
    setSaving(true);
    try { await openDashboard(draft.base.dashboard_id); } finally { setSaving(false); }
  }
  async function discard() {
    if (draft?.base) {
      setSaving(true);
      try { setDraft(fromDashboard(await getDashboard(draft.base.dashboard_id))); resetEditor(); setError(""); }
      catch (err) { setError(err instanceof Error ? err.message : "Could not reload dashboard."); }
      finally { setSaving(false); }
    } else { setDraft(null); resetEditor(); setError(""); }
  }
  const blocked = blocker.state === "blocked";
  function proceed() {
    if (blocked) blocker.proceed();
    else if (pending) pending();
    setPending(null);
  }
  function stay() { if (blocked) blocker.reset(); setPending(null); }
  return <Context.Provider value={{ draft, error, saving, editorValid, setEditorValid, editorEpoch, selectArtifact, receiveArtifact, openDashboard, edit: definition => setDraft(current => current ? editDraft(current, definition) : current), save, reload, discard, customize: () => { if (editorValid) navigateRetainingDraft("/"); }, navigateRetainingDraft, changeBackend: url => { const next = url.trim().replace(/\/$/, ""); if (next !== getBaseUrl()) choose(() => { setDraft(null); resetEditor(); setError(""); setBaseUrl(next); }); }, startFresh: action => choose(() => { setDraft(null); resetEditor(); setError(""); action(); }), context: editorValid ? draftContext(draft) : undefined }}>
    {children}
    {(blocked || pending) && <Dialog title="Keep your changes?" onClose={() => { if (!saving) stay(); }}><p>This draft has unsaved changes. Save it, discard it, or stay here.</p>{!editorValid && <p role="status">Fix or discard the invalid editor values before saving.</p>}{error && <p role="alert">{error}</p>}<div className="workspace-actions"><button disabled={saving} onClick={stay}>Stay</button><button disabled={saving} onClick={() => { setDraft(null); resetEditor(); proceed(); }}>Discard</button>{error && draft?.base && <><button disabled={saving} onClick={() => { void reload(); stay(); }}>Reload saved version</button><button disabled={saving || !editorValid || !draftContext(draft)} onClick={() => void save(true).then(ok => { if (ok) proceed(); })}>Save as new</button></>}<button className="primary" disabled={saving || !editorValid || !draftContext(draft)} onClick={() => void save().then(ok => { if (ok) proceed(); })}>Save</button></div></Dialog>}
  </Context.Provider>;
}
