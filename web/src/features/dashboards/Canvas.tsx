import { useEffect, useImperativeHandle, useMemo, useRef, useState, type Ref } from "react";
import { Maximize2Icon, Minimize2Icon, XIcon } from "lucide-react";
import { useParams } from "react-router-dom";
import { useBaseUrlVersion } from "../../api/client";
import { readDefinition } from "../views/catalog/compatibility";
import { ViewEditor } from "../views/editor/ViewEditor";
import { ViewRenderer } from "../views/renderer/ViewRenderer";
import { useWorkspace } from "./Workspace";
import { useModalFocus } from "./useModalFocus";
import { AutomationCanvasBody } from "../workspace/AutomationCanvas";
import { Button } from "../../components/button";
import { Notice } from "../../components/notice";
import { cn } from "../../lib/utils";
import { canvasHeader, canvasHeading, canvasInner, canvasSubtitle, canvasTitle, canvasToolbar, canvasToolbarGroup, canvasWindowActions, phoneSaveBar, phoneSaveInset } from "../workspace/canvasStyles";

export function Canvas({ dashboardPage = false, active = true, placement = "nested", automationEditing = false, onClose, ref }: { dashboardPage?: boolean; active?: boolean; placement?: "page" | "conversation" | "nested"; automationEditing?: boolean; onClose?: () => void; ref?: Ref<HTMLElement> }) {
  const workspace = useWorkspace();
  const { draft, error, saving } = workspace;
  const { id } = useParams();
  const backendVersion = useBaseUrlVersion();
  const [editor, setEditor] = useState(false);
  const [expanded, setExpanded] = useState(false);
  const expandButton = useRef<HTMLButtonElement>(null);
  const canvas = useRef<HTMLElement>(null);
  useImperativeHandle(ref, () => canvas.current!, []);
  useModalFocus(canvas, expanded, () => setExpanded(false), true, expandButton);
  useEffect(() => {
    if (dashboardPage && id) void workspace.openDashboard(id);
    // A route change, not a draft edit, selects a different saved dashboard.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [dashboardPage, id, backendVersion]);
  useEffect(() => { if (!workspace.canvasDraft) setExpanded(false); }, [workspace.canvasDraft]);
  const view = useMemo(() => draft ? readDefinition(draft.definition) : null, [draft?.definition]);
  const blocked = saving || !workspace.editorValid || view?.status !== "supported";
  // A generated view is unsaved until it becomes a dashboard; a dashboard, until its edits are saved.
  const unsaved = !!draft && (!draft.base || draft.dirty);
  const wide = expanded || dashboardPage || placement === "page";
  const surface = cn("min-h-0 min-w-0 overflow-auto bg-background [scrollbar-color:var(--gray-5)_transparent] [scrollbar-width:thin] selection:bg-violet/40 selection:text-foreground max-tablet:h-full", (dashboardPage || placement === "page") && "flex-1", placement === "conversation" && "animate-canvas-in max-tablet:animate-none", placement === "conversation" && !active && "max-tablet:hidden", expanded && "fixed inset-0 z-50");
  if (workspace.automationDraft) return <section className={surface} role={expanded ? "dialog" : "region"} aria-modal={expanded || undefined} aria-label="Automation canvas" ref={canvas} tabIndex={-1}><AutomationCanvasBody wide={wide} expanded={expanded} setExpanded={setExpanded} expandButton={expandButton} onClose={onClose} initialEditor={automationEditing} /></section>;
  return <section className={surface} role={expanded ? "dialog" : "region"} aria-modal={expanded || undefined} aria-label="View canvas" ref={canvas} tabIndex={-1}>
    <div className={cn(canvasInner, wide && "max-w-[1440px]", (unsaved || !workspace.editorValid) && phoneSaveInset)}>
      {error && <Notice role="alert">{error}{draft?.base && <Button className="m-1.5" variant="outline" size="touch" disabled={saving} onClick={() => void workspace.reload()}>Reload saved version</Button>}{view?.status === "supported" && <Button className="m-1.5" variant="outline" size="touch" disabled={saving || !workspace.editorValid} onClick={() => void workspace.save(true)}>Save as new</Button>}</Notice>}
      {!draft ? dashboardPage && !error && <p className="py-10 text-muted-foreground" role="status">Loading dashboard…</p> : <>
        {/* Title, view actions, then window controls. A wide canvas puts all three on one row. */}
        <header className={canvasHeader}>
          <div className={canvasTitle}><h2 className={canvasHeading}>{view?.status === "supported" ? view.definition.title : view?.status === "partial" ? view.title : "Unavailable view"}</h2><p className={canvasSubtitle}>{draft.base ? `Dashboard · revision ${draft.base.revision}` : "Generated view"} {draft.dirty ? <span className="text-violet-text">· Unsaved changes</span> : "· Saved"}</p></div>
          <div className={canvasToolbar}>
            <div className={canvasToolbarGroup}>
              <Button className="aria-expanded:bg-card aria-expanded:hover:bg-gray-3" variant="outline" size="touch" disabled={blocked} onClick={() => setEditor(!editor)} aria-expanded={editor}>{editor ? "Done editing" : "Edit"}</Button>
              <Button variant="outline" size="touch" disabled={blocked} onClick={() => { setExpanded(false); workspace.customize(); }}>Ask Hearth to change</Button>
            </div>
            {/* On phones this group becomes a bar along the bottom edge while there is something to save. */}
            <div className={cn(canvasToolbarGroup, (unsaved || !workspace.editorValid) && phoneSaveBar)}>
              {(draft.dirty || !workspace.editorValid) && <Button className="max-tablet:min-w-0 max-tablet:shrink max-tablet:whitespace-normal max-tablet:leading-[1.25]" variant="outline" size="touch" disabled={saving} onClick={() => void workspace.discard().then(() => canvas.current?.focus())}>Discard changes</Button>}
              {draft.base && <Button className="max-tablet:min-w-0 max-tablet:shrink max-tablet:whitespace-normal max-tablet:leading-[1.25]" variant="outline" size="touch" disabled={blocked} onClick={() => void workspace.save(true)}>Save as new</Button>}
              <Button className={cn("max-tablet:min-w-0 max-tablet:shrink max-tablet:whitespace-normal max-tablet:leading-[1.25]", !(unsaved || !workspace.editorValid) && "max-tablet:hidden")} size="touch" disabled={blocked || !unsaved} onClick={() => void workspace.save()}>{draft.base ? "Save changes" : "Save dashboard"}</Button>
            </div>
          </div>
          <div className={canvasWindowActions}>
            <Button ref={expandButton} variant="ghost" size="icon-touch" className={cn("aria-expanded:bg-transparent aria-expanded:text-muted-foreground aria-expanded:hover:bg-muted", !expanded && "max-tablet:hidden")} onClick={() => setExpanded(value => !value)} aria-expanded={expanded} aria-label={expanded ? "Close expanded canvas" : "Expand"} title={expanded ? "Close expanded canvas" : "Expand"}>{expanded ? <Minimize2Icon aria-hidden="true" /> : <Maximize2Icon aria-hidden="true" />}</Button>
            {onClose && <Button variant="ghost" size="icon-touch" disabled={saving} onClick={() => { setExpanded(false); onClose(); }} aria-label="Close view" title="Close view"><XIcon aria-hidden="true" /></Button>}
          </div>
        </header>
        {!workspace.editorValid && <Notice role="status">Fix or discard the invalid editor values before saving or asking Hearth to change this view.</Notice>}
        {editor && view?.status === "supported" && <fieldset disabled={saving} className="mb-5 rounded-lg border border-border bg-card p-4"><ViewEditor key={`${workspace.editorEpoch}:${draft.origin_artifact_id ?? ""}:${draft.base?.dashboard_id ?? ""}`} definition={view.definition} onChange={workspace.edit} onValidityChange={workspace.setEditorValid} /></fieldset>}
        <ViewRenderer definition={draft.definition} active={active || expanded} headingLevel={3} />
      </>}
    </div>
  </section>;
}
