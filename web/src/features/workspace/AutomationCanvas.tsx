import { useState, type RefObject } from "react";
import { Maximize2Icon, Minimize2Icon, XIcon } from "lucide-react";
import { AutomationCard } from "../automations/AutomationCard";
import { AutomationEditor } from "../automations/AutomationEditor";
import { automationDocumentIssues } from "../automations/document";
import { useWorkspace } from "./Workspace";
import { SaveDiagnostics } from "./SaveDiagnostics";
import { Button } from "../../components/ui/button";
import { Notice } from "../../components/ui/notice";
import { cn } from "../../lib/utils";
import { canvasHeader, canvasHeading, canvasSubtitle, canvasTitle, canvasToolbar, canvasToolbarGroup, canvasWindowActions, phoneSaveBar, phoneSaveInset } from "./canvasStyles";

export function AutomationCanvasBody({ expanded, setExpanded, expandButton, onClose, initialEditor = false }: { expanded: boolean; setExpanded: (value: boolean) => void; expandButton: RefObject<HTMLButtonElement | null>; onClose?: () => void; initialEditor?: boolean }) {
  const workspace = useWorkspace();
  const draft = workspace.automationDraft!;
  const [editor, setEditor] = useState(initialEditor || draft.dirty || draft.mode === "create");
  const issues = automationDocumentIssues(draft.document);
  const invalid = !workspace.editorValid || !!issues.length;
  const unsaved = draft.mode === "create" || draft.dirty;
  const saving = workspace.saving;
  return <div className={cn("canvas-inner @container/canvas", (unsaved || invalid) && phoneSaveInset)}>
    <header className={canvasHeader}><div className={canvasTitle}><h2 className={canvasHeading}>{draft.document.definition.name}</h2><p className={canvasSubtitle}>{draft.base ? `Automation · revision ${draft.base.revision}` : "New Automation"} <span className={unsaved ? "text-violet-text" : ""}>{unsaved ? "· Unsaved changes" : "· Saved"}</span></p></div>
      <div className={canvasToolbar}><div className={canvasToolbarGroup}><Button className="aria-expanded:bg-card aria-expanded:hover:bg-gray-3" variant="outline" size="touch" disabled={saving || !workspace.editorValid} aria-expanded={editor} onClick={() => setEditor(!editor)}>{editor ? "Done editing" : "Edit"}</Button><Button variant="outline" size="touch" disabled={saving || invalid} onClick={() => { setExpanded(false); workspace.customize(); }}>Ask Hearth to change</Button></div>
        <div className={cn(canvasToolbarGroup, (unsaved || invalid) && phoneSaveBar)}>{(unsaved || invalid) && <Button className="max-tablet:min-w-0 max-tablet:shrink max-tablet:whitespace-normal max-tablet:leading-[1.25]" variant="outline" size="touch" disabled={saving} onClick={() => void workspace.discard()}>Discard changes</Button>}{draft.base && <Button className="max-tablet:min-w-0 max-tablet:shrink max-tablet:whitespace-normal max-tablet:leading-[1.25]" variant="outline" size="touch" disabled={saving || invalid} onClick={() => void workspace.save(true)}>Save as new</Button>}<Button className={cn("max-tablet:min-w-0 max-tablet:shrink max-tablet:whitespace-normal max-tablet:leading-[1.25]", !(unsaved || invalid) && "max-tablet:hidden")} size="touch" disabled={saving || invalid || !unsaved} onClick={() => void workspace.save()}>{saving ? "Saving…" : draft.document.definition.enabled ? "Save and enable" : draft.base ? "Save changes" : "Save Automation"}</Button></div>
      </div><div className={canvasWindowActions}><Button ref={expandButton} variant="ghost" size="icon-touch" className={cn("aria-expanded:bg-transparent aria-expanded:text-muted-foreground aria-expanded:hover:bg-muted", !expanded && "max-tablet:hidden")} aria-label={expanded ? "Close expanded canvas" : "Expand"} aria-expanded={expanded} onClick={() => setExpanded(!expanded)}>{expanded ? <Minimize2Icon aria-hidden="true" /> : <Maximize2Icon aria-hidden="true" />}</Button>{onClose && <Button disabled={saving} variant="ghost" size="icon-touch" aria-label="Close Automation" onClick={onClose}><XIcon aria-hidden="true" /></Button>}</div>
    </header>
    {workspace.error && <Notice role="alert">{workspace.error}{draft.base && <Button className="m-1.5" variant="outline" size="touch" disabled={saving} onClick={() => void workspace.reload()}>Reload saved version</Button>}<Button className="m-1.5" variant="outline" size="touch" disabled={saving || invalid} onClick={() => void workspace.save(true)}>Save as new</Button></Notice>}
    <SaveDiagnostics issues={workspace.diagnostics} onSelectStep={id => {
      setEditor(true);
      requestAnimationFrame(() => {
        const step = [...document.querySelectorAll<HTMLElement>("[data-automation-step-id]")].find(element => element.dataset.automationStepId === id);
        if (!step) return;
        for (let element: HTMLElement | null = step; element; element = element.parentElement) if (element instanceof HTMLDetailsElement) element.open = true;
        step.querySelector<HTMLElement>("input, select, textarea, summary")?.focus();
      });
    }} />
    {invalid && <Notice role="status">Fix or discard invalid editor values before saving.{issues.map(issue => <p key={`${issue.path}:${issue.message}`}>{issue.path}: {issue.message}</p>)}</Notice>}
    {editor && <fieldset className="m-0 mb-5 min-w-0 border-0 p-0" disabled={saving}><AutomationEditor key={`${workspace.editorEpoch}:${draft.origin_artifact_id ?? ""}`} document={draft.document} onChange={workspace.editAutomation} onValidityChange={workspace.setEditorValid} /></fieldset>}
    <AutomationCard document={draft.document} automationId={draft.base?.automation_id} />
    <p className="py-10 text-muted-foreground">Draft changes do not execute. Run saved Automations from their detail page.</p>
  </div>;
}
