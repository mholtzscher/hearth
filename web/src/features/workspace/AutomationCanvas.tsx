import { useState, type RefObject } from "react";
import { Maximize2Icon, Minimize2Icon, XIcon } from "lucide-react";
import { AutomationCard } from "../automations/AutomationCard";
import { AutomationEditor } from "../automations/AutomationEditor";
import { automationDocumentIssues } from "../automations/document";
import { useWorkspace } from "./Workspace";

export function AutomationCanvasBody({ expanded, setExpanded, expandButton, onClose }: { expanded: boolean; setExpanded: (value: boolean) => void; expandButton: RefObject<HTMLButtonElement | null>; onClose?: () => void }) {
  const workspace = useWorkspace();
  const draft = workspace.automationDraft!;
  const [editor, setEditor] = useState(draft.dirty || draft.mode === "create");
  const issues = automationDocumentIssues(draft.document);
  const invalid = !workspace.editorValid || !!issues.length;
  const unsaved = draft.mode === "create" || draft.dirty;
  const saving = workspace.saving;
  return <div className="canvas-inner">
    <header className="canvas-header"><div className="canvas-title"><h2>{draft.document.definition.name}</h2><p>{draft.base ? `Automation · revision ${draft.base.revision}` : "New Automation"} <span className={unsaved ? "unsaved" : ""}>{unsaved ? "· Unsaved changes" : "· Saved"}</span></p></div>
      <div className="canvas-toolbar"><div className="canvas-toolbar-group"><button disabled={saving || !workspace.editorValid} aria-expanded={editor} onClick={() => setEditor(!editor)}>{editor ? "Done editing" : "Edit"}</button><button disabled={saving || invalid} onClick={() => { setExpanded(false); workspace.customize(); }}>Ask Hearth to change</button></div>
        <div className="canvas-toolbar-group canvas-save" data-pending={unsaved || invalid || undefined}>{(unsaved || invalid) && <button disabled={saving} onClick={() => void workspace.discard()}>Discard changes</button>}{draft.base && <button disabled={saving || invalid} onClick={() => void workspace.save(true)}>Save as new</button>}<button className="primary" disabled={saving || invalid || !unsaved} onClick={() => void workspace.save()}>{saving ? "Saving…" : draft.document.definition.enabled ? "Save and enable" : draft.base ? "Save changes" : "Save Automation"}</button></div>
      </div><div className="canvas-window-actions"><button ref={expandButton} className="icon-button canvas-expand" aria-label={expanded ? "Close expanded canvas" : "Expand"} aria-expanded={expanded} onClick={() => setExpanded(!expanded)}>{expanded ? <Minimize2Icon aria-hidden="true" /> : <Maximize2Icon aria-hidden="true" />}</button>{onClose && <button disabled={saving} className="icon-button" aria-label="Close Automation" onClick={onClose}><XIcon aria-hidden="true" /></button>}</div>
    </header>
    {workspace.error && <div className="workspace-notice" role="alert">{workspace.error}{draft.base && <button disabled={saving} onClick={() => void workspace.reload()}>Reload saved version</button>}<button disabled={saving || invalid} onClick={() => void workspace.save(true)}>Save as new</button></div>}
    {invalid && <div className="workspace-notice" role="status">Fix or discard invalid editor values before saving.{issues.map(issue => <p key={`${issue.path}:${issue.message}`}>{issue.path}: {issue.message}</p>)}</div>}
    {editor && <fieldset className="editor-panel" disabled={saving}><AutomationEditor key={`${workspace.editorEpoch}:${draft.origin_artifact_id ?? ""}`} document={draft.document} onChange={workspace.editAutomation} onValidityChange={workspace.setEditorValid} /></fieldset>}
    <AutomationCard document={draft.document} automationId={draft.base?.automation_id} />
    <p className="canvas-loading">Draft changes do not execute. Run saved Automations from their detail page.</p>
  </div>;
}
