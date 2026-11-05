import { useEffect, useImperativeHandle, useMemo, useRef, useState, type Ref } from "react";
import { Maximize2Icon, Minimize2Icon, XIcon } from "lucide-react";
import { useParams } from "react-router-dom";
import { useBaseUrlVersion } from "../../api/client";
import { readDefinition } from "../views/catalog/compatibility";
import { ViewEditor } from "../views/editor/ViewEditor";
import { ViewRenderer } from "../views/renderer/ViewRenderer";
import { useWorkspace } from "./Workspace";
import { useModalFocus } from "./useModalFocus";

export function Canvas({ dashboardPage = false, active = true, onClose, ref }: { dashboardPage?: boolean; active?: boolean; onClose?: () => void; ref?: Ref<HTMLElement> }) {
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
  useEffect(() => { if (!draft) setExpanded(false); }, [draft]);
  const view = useMemo(() => draft ? readDefinition(draft.definition) : null, [draft?.definition]);
  const blocked = saving || !workspace.editorValid || view?.status !== "supported";
  return <section className={`hearth-canvas${expanded ? " expanded" : ""}`} role={expanded ? "dialog" : "region"} aria-modal={expanded || undefined} aria-label="View canvas" ref={canvas} tabIndex={-1}>
    <div className="canvas-inner">
      {error && <div className="workspace-notice" role="alert">{error}{draft?.base && <button disabled={saving} onClick={() => void workspace.reload()}>Reload saved version</button>}{view?.status === "supported" && <button disabled={saving || !workspace.editorValid} onClick={() => void workspace.save(true)}>Save as new</button>}</div>}
      {!draft ? dashboardPage && !error && <p className="canvas-loading" role="status">Loading dashboard…</p> : <>
        <header className="canvas-header">
          <div className="canvas-title"><h2>{view?.status === "supported" ? view.definition.title : view?.status === "partial" ? view.title : "Unavailable view"}</h2><p>{draft.base ? `Dashboard · revision ${draft.base.revision}` : "Generated view"} {draft.dirty ? <span className="unsaved">· Unsaved changes</span> : "· Saved"}</p></div>
          <div className="canvas-window-actions">
            <button ref={expandButton} className="icon-button" onClick={() => setExpanded(value => !value)} aria-expanded={expanded} aria-label={expanded ? "Close expanded canvas" : "Expand"} title={expanded ? "Close expanded canvas" : "Expand"}>{expanded ? <Minimize2Icon aria-hidden="true" /> : <Maximize2Icon aria-hidden="true" />}</button>
            {onClose && <button className="icon-button" disabled={saving} onClick={() => { setExpanded(false); onClose(); }} aria-label="Close view" title="Close view"><XIcon aria-hidden="true" /></button>}
          </div>
        </header>
        <div className="canvas-toolbar">
          <div className="canvas-toolbar-group">
            <button disabled={blocked} onClick={() => setEditor(!editor)} aria-expanded={editor}>{editor ? "Done editing" : "Edit"}</button>
            <button disabled={blocked} onClick={() => { setExpanded(false); workspace.customize(); }}>Ask Hearth to change</button>
          </div>
          <div className="canvas-toolbar-group">
            {(draft.dirty || !workspace.editorValid) && <button disabled={saving} onClick={() => void workspace.discard().then(() => canvas.current?.focus())}>Discard changes</button>}
            {draft.base && <button disabled={blocked} onClick={() => void workspace.save(true)}>Save as new</button>}
            <button className="primary" disabled={blocked || (!!draft.base && !draft.dirty)} onClick={() => void workspace.save()}>{draft.base ? "Save changes" : "Save dashboard"}</button>
          </div>
        </div>
        {!workspace.editorValid && <p className="workspace-notice" role="status">Fix or discard the invalid editor values before saving or asking Hearth to change this view.</p>}
        {editor && view?.status === "supported" && <fieldset disabled={saving} className="editor-panel"><ViewEditor key={`${workspace.editorEpoch}:${draft.origin_artifact_id ?? ""}:${draft.base?.dashboard_id ?? ""}`} definition={view.definition} onChange={workspace.edit} onValidityChange={workspace.setEditorValid} /></fieldset>}
        <ViewRenderer definition={draft.definition} active={active || expanded} />
      </>}
    </div>
  </section>;
}
