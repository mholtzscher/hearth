import { Maximize2Icon, Minimize2Icon, XIcon } from "lucide-react";
import {
  useCallback,
  useEffect,
  useEffectEvent,
  useImperativeHandle,
  useMemo,
  useRef,
  useState,
} from "react";
import type { ReactNode, Ref, RefObject } from "react";
import { useParams } from "react-router-dom";

import { useBaseUrlVersion } from "../../api/client";
import { Button } from "../../components/button";
import { Card } from "../../components/card";
import { Notice, StatusNotice } from "../../components/notice";
import { pageHeadingClass } from "../../components/page-header";
import { TravelingView } from "../../lib/canvas-travel";
import { cn } from "../../lib/utils";
import type { ViewDefinition } from "../views/catalog";
import { readDefinition } from "../views/catalog/compatibility";
import type { ReadView } from "../views/catalog/compatibility";
import { ViewEditor } from "../views/editor/view-editor";
import { ViewRenderer } from "../views/renderer/view-renderer";
import { AutomationCanvasBody } from "../workspace/automation-canvas";
import {
  canvasHeader,
  canvasInner,
  canvasSubtitle,
  canvasTitle,
  canvasToolbar,
  canvasToolbarGroup,
  canvasWindowActions,
  phoneSaveBar,
  phoneSaveInset,
} from "../workspace/canvas-styles";
import type { Draft } from "./draft";
import { useModalFocus } from "./use-modal-focus";
import { useWorkspace } from "./workspace";

const surfaceClass = ({
  page,
  conversation,
  active,
  expanded,
  slide,
}: {
  page: boolean;
  conversation: boolean;
  active: boolean;
  expanded: boolean;
  slide?: boolean;
}): string =>
  cn(
    "bg-background selection:bg-violet/40 selection:text-foreground max-tablet:h-full min-h-0 min-w-0 [scrollbar-width:thin] [scrollbar-color:var(--gray-5)_transparent] overflow-auto",
    page && "flex-1",
    conversation &&
      slide !== false &&
      "animate-canvas-in max-tablet:animate-none",
    conversation && !active && "max-tablet:hidden",
    expanded && "fixed inset-0 z-50"
  );

const innerClass = (wide: boolean, saveVisible: boolean): string =>
  cn(canvasInner, wide && "max-w-[1440px]", saveVisible && phoneSaveInset);

const ViewCanvasContent = ({
  draft,
  active,
  conversation,
}: {
  draft: Draft;
  active: boolean;
  conversation: boolean;
}) => (
  <TravelingView
    artifactId={conversation ? draft.origin_artifact_id : undefined}
  >
    <div>
      <ViewRenderer
        definition={draft.definition}
        active={active}
        headingLevel={3}
      />
    </div>
  </TravelingView>
);

const CanvasSection = ({
  expanded,
  label,
  surface,
  canvas,
  children,
}: {
  expanded: boolean;
  label: string;
  surface: string;
  canvas: RefObject<HTMLElement | null>;
  children: ReactNode;
}) => (
  <section
    className={surface}
    role={expanded ? "dialog" : "region"}
    aria-modal={expanded || undefined}
    aria-label={label}
    ref={canvas}
    tabIndex={-1}
  >
    {children}
  </section>
);

const CanvasLoading = ({ page, error }: { page: boolean; error: string }) => {
  if (!page || error) {
    return null;
  }
  return (
    <output className="text-muted-foreground block py-10">
      Loading dashboard…
    </output>
  );
};

const CanvasInvalidNotice = ({ editorValid }: { editorValid: boolean }) => {
  if (editorValid) {
    return null;
  }
  return (
    <StatusNotice>
      Fix or discard the invalid editor values before saving or asking Hearth to
      change this view.
    </StatusNotice>
  );
};

const viewTitle = (view: ReadView | null): string => {
  if (view?.status === "supported") {
    return view.definition.title;
  }
  if (view?.status === "partial") {
    return view.title;
  }
  return "Unavailable view";
};

const CanvasSubtitle = ({ draft }: { draft: Draft }) => (
  <p className={canvasSubtitle}>
    {draft.base
      ? `Dashboard · revision ${draft.base.revision}`
      : "Generated view"}{" "}
    {draft.dirty ? (
      <span className="text-violet-text">· Unsaved changes</span>
    ) : (
      "· Saved"
    )}
  </p>
);

const CanvasTitleBlock = ({
  view,
  draft,
}: {
  view: ReadView | null;
  draft: Draft;
}) => (
  <div className={canvasTitle}>
    <h2 className={pageHeadingClass}>{viewTitle(view)}</h2>
    <CanvasSubtitle draft={draft} />
  </div>
);

const CanvasErrorNotice = ({
  error,
  draft,
  view,
  saving,
  editorValid,
  onReload,
  onSaveNew,
}: {
  error: string;
  draft: Draft | null;
  view: ReadView | null;
  saving: boolean;
  editorValid: boolean;
  onReload: () => void;
  onSaveNew: () => void;
}) => {
  if (!error) {
    return null;
  }
  return (
    <Notice role="alert">
      {error}
      {draft?.base && (
        <Button
          className="m-1.5"
          variant="outline"
          size="touch"
          disabled={saving}
          onClick={onReload}
        >
          Reload saved version
        </Button>
      )}
      {view?.status === "supported" && (
        <Button
          className="m-1.5"
          variant="outline"
          size="touch"
          disabled={saving || !editorValid}
          onClick={onSaveNew}
        >
          Save as new
        </Button>
      )}
    </Notice>
  );
};

const CanvasToolbar = ({
  draft,
  saving,
  blocked,
  unsaved,
  editorValid,
  editor,
  onToggleEditor,
  onCustomize,
  onDiscard,
  onSave,
  onSaveNew,
}: {
  draft: Draft;
  saving: boolean;
  blocked: boolean;
  unsaved: boolean;
  editorValid: boolean;
  editor: boolean;
  onToggleEditor: () => void;
  onCustomize: () => void;
  onDiscard: () => void;
  onSave: () => void;
  onSaveNew: () => void;
}) => {
  const saveVisible = unsaved || !editorValid;
  return (
    <div className={canvasToolbar}>
      <div className={canvasToolbarGroup}>
        <Button
          className="aria-expanded:bg-card aria-expanded:hover:bg-gray-3"
          variant="outline"
          size="touch"
          disabled={blocked}
          onClick={onToggleEditor}
          aria-expanded={editor}
        >
          {editor ? "Done editing" : "Edit"}
        </Button>
        <Button
          variant="outline"
          size="touch"
          disabled={blocked}
          onClick={onCustomize}
        >
          Ask Hearth to change
        </Button>
      </div>
      {/* On phones this group becomes a bar along the bottom edge while there is something to save. */}
      <div className={cn(canvasToolbarGroup, saveVisible && phoneSaveBar)}>
        {(draft.dirty || !editorValid) && (
          <Button
            className="max-tablet:min-w-0 max-tablet:shrink max-tablet:whitespace-normal max-tablet:leading-[1.25]"
            variant="outline"
            size="touch"
            disabled={saving}
            onClick={onDiscard}
          >
            Discard changes
          </Button>
        )}
        {draft.base && (
          <Button
            className="max-tablet:min-w-0 max-tablet:shrink max-tablet:whitespace-normal max-tablet:leading-[1.25]"
            variant="outline"
            size="touch"
            disabled={blocked}
            onClick={onSaveNew}
          >
            Save as new
          </Button>
        )}
        <Button
          className={cn(
            "max-tablet:min-w-0 max-tablet:shrink max-tablet:whitespace-normal max-tablet:leading-[1.25]",
            !saveVisible && "max-tablet:hidden"
          )}
          size="touch"
          disabled={blocked || !unsaved}
          onClick={onSave}
        >
          {draft.base ? "Save changes" : "Save dashboard"}
        </Button>
      </div>
    </div>
  );
};

const CanvasWindowActions = ({
  expanded,
  expandButton,
  saving,
  onToggleExpanded,
  onClose,
}: {
  expanded: boolean;
  expandButton: Ref<HTMLButtonElement>;
  saving: boolean;
  onToggleExpanded: () => void;
  onClose: (() => void) | undefined;
}) => {
  const expandLabel = expanded ? "Close expanded canvas" : "Expand";
  return (
    <div className={canvasWindowActions}>
      <Button
        ref={expandButton}
        variant="ghost"
        size="icon-touch"
        className={cn(
          "aria-expanded:text-muted-foreground aria-expanded:hover:bg-muted aria-expanded:bg-transparent",
          !expanded && "max-tablet:hidden"
        )}
        onClick={onToggleExpanded}
        aria-expanded={expanded}
        aria-label={expandLabel}
        title={expandLabel}
      >
        {expanded ? (
          <Minimize2Icon aria-hidden="true" />
        ) : (
          <Maximize2Icon aria-hidden="true" />
        )}
      </Button>
      {onClose && (
        <Button
          variant="ghost"
          size="icon-touch"
          disabled={saving}
          onClick={onClose}
          aria-label="Close view"
          title="Close view"
        >
          <XIcon aria-hidden="true" />
        </Button>
      )}
    </div>
  );
};

const CanvasEditor = ({
  editor,
  view,
  draft,
  editorEpoch,
  saving,
  editDefinition,
  validateEditor,
}: {
  editor: boolean;
  view: ReadView | null;
  draft: Draft;
  editorEpoch: number;
  saving: boolean;
  editDefinition: (definition: ViewDefinition) => void;
  validateEditor: (valid: boolean) => void;
}) => {
  const handleDefinitionChange = useCallback(
    (definition: ViewDefinition) => {
      editDefinition(definition);
    },
    [editDefinition]
  );
  const handleValidityChange = useCallback(
    (valid: boolean) => {
      validateEditor(valid);
    },
    [validateEditor]
  );
  if (!editor || view?.status !== "supported") {
    return null;
  }
  return (
    <Card asChild className="mb-5">
      <fieldset disabled={saving}>
        <ViewEditor
          key={`${editorEpoch}:${draft.origin_artifact_id ?? ""}:${draft.base?.dashboard_id ?? ""}`}
          definition={view.definition}
          onChange={handleDefinitionChange}
          onValidityChange={handleValidityChange}
        />
      </fieldset>
    </Card>
  );
};

const CanvasHeader = ({
  view,
  draft,
  editor,
  expanded,
  expandButton,
  saving,
  blocked,
  unsaved,
  editorValid,
  onToggleEditor,
  onCustomize,
  onDiscard,
  onSave,
  onSaveNew,
  onToggleExpanded,
  onClose,
}: {
  view: ReadView | null;
  draft: Draft;
  editor: boolean;
  expanded: boolean;
  expandButton: Ref<HTMLButtonElement>;
  saving: boolean;
  blocked: boolean;
  unsaved: boolean;
  editorValid: boolean;
  onToggleEditor: () => void;
  onCustomize: () => void;
  onDiscard: () => void;
  onSave: () => void;
  onSaveNew: () => void;
  onToggleExpanded: () => void;
  onClose: (() => void) | undefined;
}) => (
  <header className={canvasHeader}>
    <CanvasTitleBlock view={view} draft={draft} />
    <CanvasToolbar
      draft={draft}
      saving={saving}
      blocked={blocked}
      unsaved={unsaved}
      editorValid={editorValid}
      editor={editor}
      onToggleEditor={onToggleEditor}
      onCustomize={onCustomize}
      onDiscard={onDiscard}
      onSave={onSave}
      onSaveNew={onSaveNew}
    />
    <CanvasWindowActions
      expanded={expanded}
      expandButton={expandButton}
      saving={saving}
      onToggleExpanded={onToggleExpanded}
      onClose={onClose}
    />
  </header>
);

export const Canvas = ({
  dashboardPage = false,
  active = true,
  placement = "nested",
  automationEditing = false,
  slide,
  onClose,
  ref,
}: {
  dashboardPage?: boolean;
  active?: boolean;
  placement?: "page" | "conversation" | "nested";
  automationEditing?: boolean;
  /** A shared view traveling in from the conversation does not also slide. */
  slide?: boolean;
  onClose?: () => void;
  ref?: Ref<HTMLElement>;
}) => {
  const workspace = useWorkspace();
  const { draft, error, saving } = workspace;
  const { id } = useParams();
  const backendVersion = useBaseUrlVersion();
  const [editor, setEditor] = useState(false);
  const [expanded, setExpanded] = useState(false);
  const hasCanvasDraft = !!workspace.canvasDraft;
  const [hadCanvasDraft, setHadCanvasDraft] = useState(hasCanvasDraft);
  if (hadCanvasDraft !== hasCanvasDraft) {
    setHadCanvasDraft(hasCanvasDraft);
    if (!hasCanvasDraft) {
      setExpanded(false);
    }
  }
  const expandButton = useRef<HTMLButtonElement>(null);
  const canvas = useRef<HTMLElement>(null);
  useImperativeHandle(ref, () => {
    if (!canvas.current) {
      throw new Error("Canvas element is not mounted");
    }
    return canvas.current;
  }, []);
  useModalFocus(canvas, expanded, () => setExpanded(false), true, expandButton);
  const openDashboard = useEffectEvent(
    (dashboardId: string, _backendVersion: number) => {
      workspace.openDashboard(dashboardId);
    }
  );
  useEffect(() => {
    if (dashboardPage && id) {
      // A route or backend change, not a draft edit, selects a different saved dashboard.
      openDashboard(id, backendVersion);
    }
  }, [dashboardPage, id, backendVersion]);
  const definition = draft?.definition;
  const view = useMemo(
    () => (definition ? readDefinition(definition) : null),
    [definition]
  );
  const page = dashboardPage || placement === "page";
  const conversation = placement === "conversation";
  const blocked =
    saving || !workspace.editorValid || view?.status !== "supported";
  // A generated view is unsaved until it becomes a dashboard; a dashboard, until its edits are saved.
  const unsaved = !!draft && (!draft.base || draft.dirty);
  const saveVisible = unsaved || !workspace.editorValid;
  const wide = expanded || page;
  const surface = surfaceClass({ page, conversation, active, expanded, slide });
  const handleReload = () => {
    workspace.reload();
  };
  const handleSave = () => {
    workspace.save();
  };
  const handleSaveNew = () => {
    workspace.save(true);
  };
  const handleDiscard = async () => {
    await workspace.discard();
    canvas.current?.focus();
  };
  const handleCustomize = () => {
    setExpanded(false);
    workspace.customize();
  };
  const handleToggleExpanded = () => {
    setExpanded((value) => !value);
  };
  const handleClose = () => {
    setExpanded(false);
    onClose?.();
  };
  if (workspace.automationDraft) {
    return (
      <CanvasSection
        expanded={expanded}
        label="Automation canvas"
        surface={surface}
        canvas={canvas}
      >
        <AutomationCanvasBody
          wide={wide}
          expanded={expanded}
          setExpanded={setExpanded}
          expandButton={expandButton}
          onClose={onClose}
          initialEditor={automationEditing}
        />
      </CanvasSection>
    );
  }
  return (
    <CanvasSection
      expanded={expanded}
      label="View canvas"
      surface={surface}
      canvas={canvas}
    >
      <div className={innerClass(wide, saveVisible)}>
        <CanvasErrorNotice
          error={error}
          draft={draft}
          view={view}
          saving={saving}
          editorValid={workspace.editorValid}
          onReload={handleReload}
          onSaveNew={handleSaveNew}
        />
        {draft ? (
          <>
            {/* Title, view actions, then window controls. A wide canvas puts all three on one row. */}
            <CanvasHeader
              view={view}
              draft={draft}
              editor={editor}
              expanded={expanded}
              expandButton={expandButton}
              saving={saving}
              blocked={blocked}
              unsaved={unsaved}
              editorValid={workspace.editorValid}
              onToggleEditor={() => setEditor(!editor)}
              onCustomize={handleCustomize}
              onDiscard={handleDiscard}
              onSave={handleSave}
              onSaveNew={handleSaveNew}
              onToggleExpanded={handleToggleExpanded}
              onClose={onClose ? handleClose : undefined}
            />
            <CanvasInvalidNotice editorValid={workspace.editorValid} />
            <CanvasEditor
              editor={editor}
              view={view}
              draft={draft}
              editorEpoch={workspace.editorEpoch}
              saving={saving}
              editDefinition={workspace.edit}
              validateEditor={workspace.setEditorValid}
            />
            <ViewCanvasContent
              draft={draft}
              active={active || expanded}
              conversation={conversation}
            />
          </>
        ) : (
          <CanvasLoading page={page} error={error} />
        )}
      </div>
    </CanvasSection>
  );
};
