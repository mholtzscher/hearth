import { useEffect, useMemo, useRef, useState } from "react";
import { useParams } from "react-router-dom";
import { useBaseUrlVersion } from "../../api/client";
import { readDefinition } from "../views/catalog/compatibility";
import { ViewEditor } from "../views/editor/ViewEditor";
import { ViewRenderer } from "../views/renderer/ViewRenderer";
import { useWorkspace } from "./Workspace";

export function Canvas({ dashboardPage = false, active = true }: { dashboardPage?: boolean; active?: boolean }) {
  const workspace = useWorkspace();
  const { draft, error, saving } = workspace;
  const { id } = useParams();
  const backendVersion = useBaseUrlVersion();
  const [editor, setEditor] = useState(false);
  const [expanded, setExpanded] = useState(false);
  const expandButton = useRef<HTMLButtonElement>(null);
  const canvas = useRef<HTMLElement>(null);
  useEffect(() => {
    if (dashboardPage && id) void workspace.openDashboard(id);
    // A route change, not a draft edit, selects a different saved dashboard.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [dashboardPage, id, backendVersion]);
  function toggleExpanded() {
    if (expanded) { setExpanded(false); requestAnimationFrame(() => expandButton.current?.focus()); }
    else { setExpanded(true); requestAnimationFrame(() => canvas.current?.focus()); }
  }
  const view = useMemo(() => draft ? readDefinition(draft.definition) : null, [draft?.definition]);
  return <section className={`hearth-canvas${expanded ? " expanded" : ""}`} aria-label="View canvas" ref={canvas} tabIndex={-1}>
    <div className="canvas-inner">
      <div className="canvas-bar"><span className="eyebrow">◫ &nbsp; {dashboardPage ? "Saved dashboard" : "Your canvas"}</span><div className="workspace-actions">
        <button ref={expandButton} onClick={toggleExpanded} aria-expanded={expanded}>{expanded ? "Close expanded canvas" : "Expand"}</button>
      </div></div>
      {error && <div className="workspace-notice" role="alert">{error}{draft?.base && <button disabled={saving} onClick={() => void workspace.reload()}>Reload saved version</button>}{view?.status === "supported" && <button disabled={saving || !workspace.editorValid} onClick={() => void workspace.save(true)}>Save as new</button>}</div>}
      {!draft ? <div className="canvas-empty"><div className="canvas-orbit" aria-hidden="true">◇</div><h2>A space for what comes next.</h2><p>Ask Hearth to make a view, then open it here.</p></div> : <>
        <div className="canvas-heading"><div><h2>{view?.status === "supported" ? view.definition.title : view?.status === "partial" ? view.title : "Unavailable view"}</h2><p>{draft.base ? `Dashboard · revision ${draft.base.revision}` : "Generated view"} {draft.dirty ? "· Unsaved changes" : "· Saved"}</p></div><span className="live-pill">● Live data</span></div>
        <div className="canvas-toolbar">
          <button disabled={saving || !workspace.editorValid || view?.status !== "supported"} onClick={() => setEditor(!editor)} aria-expanded={editor}>{editor ? "Done editing" : "Customize"}</button>
          <button disabled={saving || !workspace.editorValid || view?.status !== "supported"} onClick={workspace.customize}>Customize with Hearth</button>
          <button className="primary" disabled={saving || !workspace.editorValid || view?.status !== "supported" || (!!draft.base && !draft.dirty)} onClick={() => void workspace.save()}>{draft.base ? "Save changes" : "Save dashboard"}</button>
          {draft.base && <button disabled={saving || !workspace.editorValid || view?.status !== "supported"} onClick={() => void workspace.save(true)}>Save as new</button>}
          {(draft.dirty || !workspace.editorValid) && <button disabled={saving} onClick={() => void workspace.discard()}>Discard changes</button>}
        </div>
        {!workspace.editorValid && <p className="workspace-notice" role="status">Fix or discard the invalid editor values before saving or customizing with Hearth.</p>}
        {editor && view?.status === "supported" && <fieldset disabled={saving} className="editor-panel"><ViewEditor key={`${workspace.editorEpoch}:${draft.origin_artifact_id ?? ""}:${draft.base?.dashboard_id ?? ""}`} definition={view.definition} onChange={workspace.edit} onValidityChange={workspace.setEditorValid} /></fieldset>}
        <ViewRenderer definition={draft.definition} active={active || expanded} />
      </>}
    </div>
  </section>;
}
