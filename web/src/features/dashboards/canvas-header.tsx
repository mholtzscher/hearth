import { Maximize2Icon, Minimize2Icon, XIcon } from "lucide-react";
import type { Ref } from "react";

import { Button } from "../../components/button";
import { pageHeadingClass } from "../../components/page-header";
import { cn } from "../../lib/utils";
import type { ReadView } from "../views/catalog/compatibility";
import {
  canvasHeader,
  canvasSubtitle,
  canvasTitle,
  canvasToolbar,
  canvasToolbarGroup,
  canvasWindowActions,
  phoneSaveBar,
} from "../workspace/canvas-styles";
import type { Draft } from "./draft";

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

/** The canvas title, view actions, and window controls. A wide canvas puts all three on one row. */
export const CanvasHeader = ({
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
