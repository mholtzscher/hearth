import { createContext, useCallback, useContext, useEffect, useRef, useState, type ReactNode } from "react";
import { useBlocker, useLocation, useNavigate } from "react-router-dom";
import { ApiError, getBaseUrl, setBaseUrl } from "../../api/client";
import { createDashboard, getDashboard, updateDashboard } from "../../api/dashboards";
import { createAutomation, getAutomation, replaceAutomation } from "../../api/automations";
import type { Automation } from "../../api/types";
import { fromSavedAutomation, newAutomationDraft, type AutomationDocument, type AutomationDraft } from "../automations/document";
import { readDefinition } from "../views/catalog/compatibility";
import { draftContext, editDraft, fromArtifact, fromDashboard, isUneditedGeneratedView, type Draft } from "../dashboards/draft";
import { automationDraftContext, editAutomationDraft, fromAutomationArtifact, isAutomationDraft, type CanvasDraft } from "./draft";
import type { PresentedArtifact } from "../conversation/runtime";
import type { ViewDefinition } from "../views/catalog";
import { Dialog } from "../dashboards/Dialog";

type WorkspaceValue = {
  draft: Draft | null; draftVersion: number; error: string; saving: boolean;
  canvasDraft: CanvasDraft | null; automationDraft: AutomationDraft | null;
  automationContext: ReturnType<typeof automationDraftContext> | undefined;
  newAutomation: () => void; selectAutomation: (automation: Automation, onSelected?: () => void) => void;
  editAutomation: (document: AutomationDocument) => void;
  editorValid: boolean; setEditorValid: (valid: boolean) => void; editorEpoch: number;
  selectArtifact: (artifact: PresentedArtifact, onSelected?: () => void) => void;
  receiveArtifact: (artifact: PresentedArtifact, expectedVersion: number) => void;
  openDashboard: (id: string) => Promise<void>;
  edit: (definition: ViewDefinition) => void;
  save: (asNew?: boolean) => Promise<boolean>;
  reload: () => Promise<void>;
  discard: () => Promise<void>;
  customize: () => void;
  closeDraft: () => void;
  composerFocusRequested: boolean;
  finishComposerFocus: () => void;
  navigateRetainingDraft: (path: string) => void;
  changeBackend: (url: string) => void;
  startFresh: (action: () => void) => void;
  context: ReturnType<typeof draftContext>;
};
const Context = createContext<WorkspaceValue | null>(null);
export function useOptionalWorkspace() { return useContext(Context); }
export function useWorkspace() { const value = useContext(Context); if (!value) throw new Error("Workspace is missing"); return value; }

export function WorkspaceProvider({ children }: { children: ReactNode }) {
  const [{ draft, version: draftVersion }, setDraftState] = useState<{ draft: CanvasDraft | null; version: number }>({ draft: null, version: 0 });
  const draftBackend = useRef(getBaseUrl());
  const draftVersionRef = useRef(draftVersion); draftVersionRef.current = draftVersion;
  // User changes invalidate a turn's canvas updates; agent replacements retain its version.
  function setDraft(next: CanvasDraft | null | ((current: CanvasDraft | null) => CanvasDraft | null)) {
    setDraftState(current => ({ draft: typeof next === "function" ? next(current.draft) : next, version: current.version + 1 }));
  }
  const [error, setError] = useState("");
  const [saving, setSaving] = useState(false);
  const [editorValid, setEditorValid] = useState(true);
  const [editorEpoch, setEditorEpoch] = useState(0);
  const [composerFocusRequested, setComposerFocusRequested] = useState(false);
  const finishComposerFocus = useCallback(() => setComposerFocusRequested(false), []);
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
  function clearDraft() { setDraft(null); resetEditor(); setError(""); }
  function closeDraft() {
    if (saving) return;
    if (draft && !isAutomationDraft(draft) && editorValid && isUneditedGeneratedView(draft)) clearDraft();
    else choose(clearDraft);
  }
  async function openDashboard(id: string) {
    const sequence = ++loadSequence.current;
    const path = location.pathname;
    const backend = getBaseUrl();
    if (path.startsWith("/dashboards/") && (isAutomationDraft(draft) || draft?.base?.dashboard_id !== id)) setDraft(null);
    try { const dashboard = await getDashboard(id); if (sequence === loadSequence.current && path === locationRef.current && backend === getBaseUrl()) { draftBackend.current = backend; setDraft(fromDashboard(dashboard)); resetEditor(); setError(""); } }
    catch (err) { if (sequence === loadSequence.current && path === locationRef.current) setError(err instanceof Error ? err.message : "Could not load dashboard."); }
  }
  function selectArtifact(item: PresentedArtifact, onSelected?: () => void) {
    if (item.artifact.kind === "unsupported") { setError("This artifact cannot be edited in this Hearth version. Its original tool result is retained in the conversation."); return; }
    choose(() => {
      const artifact = item.artifact;
      if (artifact.kind === "hearth.automation" && artifact.mode === "reference") {
        const sequence = ++loadSequence.current, backend = getBaseUrl(), selectedVersion = draftVersionRef.current;
        void getAutomation(artifact.automation_id).then(automation => {
          if (sequence !== loadSequence.current || backend !== getBaseUrl() || selectedVersion !== draftVersionRef.current) return;
          draftBackend.current = backend;
          setDraft({ ...fromSavedAutomation(automation), origin_artifact_id: artifact.artifact_id }); resetEditor(); setError(""); onSelected?.();
        }).catch(err => { if (sequence === loadSequence.current && backend === getBaseUrl() && selectedVersion === draftVersionRef.current) setError(err instanceof Error ? err.message : "Could not load Automation."); });
      } else {
        try {
          ++loadSequence.current; draftBackend.current = getBaseUrl();
          setDraft(artifact.kind === "hearth.automation" ? fromAutomationArtifact(artifact, isAutomationDraft(draft) ? draft : null) : fromArtifact(item, !isAutomationDraft(draft) ? draft : null));
          resetEditor(); setError(""); onSelected?.();
        } catch (err) { setError(err instanceof Error ? err.message : "Could not open artifact."); }
      }
    });
  }
  function receiveArtifact(item: PresentedArtifact, expectedVersion: number) {
    if (savingRef.current || !editorValidRef.current) return;
    setDraftState(current => {
      if (current.version !== expectedVersion || draftBackend.current !== getBaseUrl()) return current;
      if (item.artifact.kind === "hearth.automation") {
        if (!isAutomationDraft(current.draft) || item.artifact.mode === "reference") return current;
        if (item.artifact.mode !== current.draft.mode) return current;
        if (item.artifact.mode === "replace" && (current.draft.mode !== "replace" || current.draft.base.automation_id !== item.artifact.automation_id || current.draft.base.revision !== item.artifact.expected_revision)) return current;
        return { ...current, draft: fromAutomationArtifact(item.artifact, current.draft) };
      }
      if (item.artifact.kind !== "hearth.view" || isAutomationDraft(current.draft)) return current;
      return { ...current, draft: fromArtifact(item, current.draft) };
    });
  }
  async function save(asNew = false): Promise<boolean> {
    if (!draft || !editorValid || savingRef.current) return false;
    if (draftBackend.current !== getBaseUrl()) { setError("The server changed. Return to the original server before saving this draft."); return false; }
    const view = !isAutomationDraft(draft) ? readDefinition(draft.definition) : null;
    if (view && view.status !== "supported") { setError("This view contains unsupported content and cannot be saved."); return false; }
    setDraftState(current => ({ ...current, version: current.version + 1 }));
    savingRef.current = true; setSaving(true); setError("");
    try {
      if (isAutomationDraft(draft)) {
        const result = !asNew && draft.mode === "replace" ? await replaceAutomation(draft.base.automation_id, draft.base.revision, draft.document) : await createAutomation(draft.document);
        if (draftBackend.current !== getBaseUrl()) return false;
        setDraft(fromSavedAutomation(result)); resetEditor();
        if (!pending && blocker.state !== "blocked" && location.pathname.startsWith("/automations/")) navigateRetainingDraft(`/automations/${encodeURIComponent(result.id)}`);
      } else if (view?.status === "supported") {
        const result = !asNew && draft.base ? await updateDashboard(draft.base.dashboard_id, draft.base.revision, view.definition) : await createDashboard(view.definition);
        setDraft(fromDashboard(result));
        if (asNew && !pending && blocker.state !== "blocked" && location.pathname.startsWith("/dashboards/")) navigateRetainingDraft(`/dashboards/${encodeURIComponent(result.id)}`);
      }
      return true;
    } catch (err) {
      const noun = isAutomationDraft(draft) ? "Automation" : "dashboard";
      setError(err instanceof ApiError && err.status === 409 ? `This ${noun} changed elsewhere. Reload the saved version or save this draft as new.` : err instanceof ApiError && err.status === 404 ? `The saved ${noun} no longer exists. Save this draft as new.` : `${err instanceof Error ? err.message : `Could not save ${noun}.`}${isAutomationDraft(draft) && (draft.mode === "create" || asNew) && !(err instanceof ApiError) ? " Creation may have succeeded. Check the Automation library before trying again." : ""}`);
      return false;
    } finally { savingRef.current = false; setSaving(false); }
  }
  async function reload() {
    if (!draft?.base) return;
    setSaving(true);
    try {
      if (isAutomationDraft(draft)) { setDraft(fromSavedAutomation(await getAutomation(draft.base.automation_id))); resetEditor(); setError(""); }
      else await openDashboard(draft.base.dashboard_id);
    } catch (err) { setError(err instanceof Error ? err.message : "Could not reload saved definition."); } finally { setSaving(false); }
  }
  async function discard() {
    if (draft?.base) {
      setSaving(true);
      try { setDraft(isAutomationDraft(draft) ? fromSavedAutomation(await getAutomation(draft.base.automation_id)) : fromDashboard(await getDashboard(draft.base.dashboard_id))); resetEditor(); setError(""); }
      catch (err) { setError(err instanceof Error ? err.message : "Could not reload dashboard."); }
      finally { setSaving(false); }
    } else clearDraft();
  }
  const blocked = blocker.state === "blocked";
  function proceed() {
    if (blocked) blocker.proceed();
    else if (pending) pending();
    setPending(null);
  }
  function stay() { if (blocked) blocker.reset(); setPending(null); }
  const dashboardDraft = isAutomationDraft(draft) ? null : draft;
  const automationDraft = isAutomationDraft(draft) ? draft : null;
  const canSave = editorValid && (!!automationDraft || !!draftContext(dashboardDraft));
  return <Context.Provider value={{ draft: dashboardDraft, canvasDraft: draft, automationDraft, automationContext: editorValid && automationDraft ? automationDraftContext(automationDraft) : undefined, newAutomation: () => choose(() => { ++loadSequence.current; draftBackend.current = getBaseUrl(); setDraft(newAutomationDraft()); resetEditor(); setError(""); navigateRetainingDraft("/automations/new"); }), selectAutomation: (automation, onSelected) => choose(() => { ++loadSequence.current; draftBackend.current = getBaseUrl(); setDraft(fromSavedAutomation(automation)); resetEditor(); setError(""); onSelected?.(); }), editAutomation: document => setDraft(current => isAutomationDraft(current) ? editAutomationDraft(current, document) : current), draftVersion, error, saving, editorValid, setEditorValid, editorEpoch, selectArtifact, receiveArtifact, openDashboard, edit: definition => setDraft(current => current && !isAutomationDraft(current) ? editDraft(current, definition) : current), save, reload, discard, closeDraft, composerFocusRequested, finishComposerFocus, customize: () => { if (editorValid) { setComposerFocusRequested(true); navigateRetainingDraft("/"); } }, navigateRetainingDraft, changeBackend: url => { const next = url.trim().replace(/\/$/, ""); if (next !== getBaseUrl()) choose(() => { ++loadSequence.current; clearDraft(); setBaseUrl(next); }); }, startFresh: action => choose(() => { ++loadSequence.current; clearDraft(); action(); }), context: editorValid ? draftContext(dashboardDraft) : undefined }}>
    {children}
    {(blocked || pending) && <Dialog title="Keep your changes?" onClose={() => { if (!saving) stay(); }}><p>This draft has unsaved changes. Save it, discard it, or stay here.</p>{!editorValid && <p role="status">Fix or discard the invalid editor values before saving.</p>}{error && <p role="alert">{error}</p>}<div className="workspace-actions"><button disabled={saving} onClick={stay}>Stay</button><button disabled={saving} onClick={() => { ++loadSequence.current; setDraft(null); resetEditor(); proceed(); }}>Discard</button>{error && draft?.base && <><button disabled={saving} onClick={() => { void reload(); stay(); }}>Reload saved version</button><button disabled={saving || !canSave} onClick={() => void save(true).then(ok => { if (ok) proceed(); })}>Save as new</button></>}<button className="primary" disabled={saving || !canSave} onClick={() => void save().then(ok => { if (ok) proceed(); })}>{automationDraft?.document.definition.enabled ? "Save and enable" : "Save"}</button></div></Dialog>}
  </Context.Provider>;
}
