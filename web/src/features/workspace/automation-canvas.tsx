import { Maximize2Icon, Minimize2Icon, XIcon } from "lucide-react";
import { useState } from "react";
import type { RefObject } from "react";

import { Button } from "../../components/button";
import { Notice, StatusNotice } from "../../components/notice";
import { cn } from "../../lib/utils";
import { AutomationCard } from "../automations/automation-card";
import { AutomationEditor } from "../automations/automation-editor";
import { automationDocumentIssues } from "../automations/document";
import type { AutomationDraft, DocumentIssue } from "../automations/document";
import {
  canvasHeader,
  canvasHeading,
  canvasInner,
  canvasSubtitle,
  canvasTitle,
  canvasToolbar,
  canvasToolbarGroup,
  canvasWindowActions,
  phoneSaveBar,
  phoneSaveInset,
} from "./canvas-styles";
import { SaveDiagnostics } from "./save-diagnostics";
import { useWorkspace } from "./workspace";

const saveButtonLabel = (
  saving: boolean,
  enabled: boolean,
  hasBase: boolean
) => {
  if (saving) {
    return "Saving…";
  }
  if (enabled) {
    return "Save and enable";
  }
  if (hasBase) {
    return "Save changes";
  }
  return "Save Automation";
};

const focusAutomationStep = (id: string) => {
  requestAnimationFrame(() => {
    const step = [
      ...document.querySelectorAll<HTMLElement>("[data-automation-step-id]"),
    ].find((element) => element.dataset.automationStepId === id);
    if (!step) {
      return;
    }
    for (
      let element: HTMLElement | null = step;
      element;
      element = element.parentElement
    ) {
      if (element instanceof HTMLDetailsElement) {
        element.open = true;
      }
    }
    step
      .querySelector<HTMLElement>("input, select, textarea, summary")
      ?.focus();
  });
};

const CanvasTitle = ({
  draft,
  unsaved,
}: {
  draft: AutomationDraft;
  unsaved: boolean;
}) => (
  <div className={canvasTitle}>
    <h2 className={canvasHeading}>{draft.document.definition.name}</h2>
    <p className={canvasSubtitle}>
      {draft.base
        ? `Automation · revision ${draft.base.revision}`
        : "New Automation"}{" "}
      <span className={unsaved ? "text-violet-text" : ""}>
        {unsaved ? "· Unsaved changes" : "· Saved"}
      </span>
    </p>
  </div>
);

const CanvasPrimaryActions = ({
  editor,
  setEditor,
  setExpanded,
  saving,
  invalid,
}: {
  editor: boolean;
  setEditor: (value: boolean) => void;
  setExpanded: (value: boolean) => void;
  saving: boolean;
  invalid: boolean;
}) => {
  const workspace = useWorkspace();
  return (
    <div className={canvasToolbarGroup}>
      <Button
        className="aria-expanded:bg-card aria-expanded:hover:bg-gray-3"
        variant="outline"
        size="touch"
        disabled={saving || !workspace.editorValid}
        aria-expanded={editor}
        onClick={() => setEditor(!editor)}
      >
        {editor ? "Done editing" : "Edit"}
      </Button>
      <Button
        variant="outline"
        size="touch"
        disabled={saving || invalid}
        onClick={() => {
          setExpanded(false);
          workspace.customize();
        }}
      >
        Ask Hearth to change
      </Button>
    </div>
  );
};

const CanvasSaveActions = ({
  draft,
  saving,
  unsaved,
  invalid,
}: {
  draft: AutomationDraft;
  saving: boolean;
  unsaved: boolean;
  invalid: boolean;
}) => {
  const workspace = useWorkspace();
  return (
    <div
      className={cn(canvasToolbarGroup, (unsaved || invalid) && phoneSaveBar)}
    >
      {(unsaved || invalid) && (
        <Button
          className="max-tablet:min-w-0 max-tablet:shrink max-tablet:whitespace-normal max-tablet:leading-[1.25]"
          variant="outline"
          size="touch"
          disabled={saving}
          onClick={() => {
            void workspace.discard();
          }}
        >
          Discard changes
        </Button>
      )}
      {draft.base && (
        <Button
          className="max-tablet:min-w-0 max-tablet:shrink max-tablet:whitespace-normal max-tablet:leading-[1.25]"
          variant="outline"
          size="touch"
          disabled={saving || invalid}
          onClick={() => {
            void workspace.save(true);
          }}
        >
          Save as new
        </Button>
      )}
      <Button
        className={cn(
          "max-tablet:min-w-0 max-tablet:shrink max-tablet:whitespace-normal max-tablet:leading-[1.25]",
          !(unsaved || invalid) && "max-tablet:hidden"
        )}
        size="touch"
        disabled={saving || invalid || !unsaved}
        onClick={() => {
          void workspace.save();
        }}
      >
        {saveButtonLabel(
          saving,
          draft.document.definition.enabled,
          !!draft.base
        )}
      </Button>
    </div>
  );
};

const CanvasWindowActions = ({
  expanded,
  setExpanded,
  expandButton,
  onClose,
  saving,
}: {
  expanded: boolean;
  setExpanded: (value: boolean) => void;
  expandButton: RefObject<HTMLButtonElement | null>;
  onClose?: () => void;
  saving: boolean;
}) => (
  <div className={canvasWindowActions}>
    <Button
      ref={expandButton}
      variant="ghost"
      size="icon-touch"
      className={cn(
        "aria-expanded:text-muted-foreground aria-expanded:hover:bg-muted aria-expanded:bg-transparent",
        !expanded && "max-tablet:hidden"
      )}
      aria-label={expanded ? "Close expanded canvas" : "Expand"}
      aria-expanded={expanded}
      onClick={() => setExpanded(!expanded)}
    >
      {expanded ? (
        <Minimize2Icon aria-hidden="true" />
      ) : (
        <Maximize2Icon aria-hidden="true" />
      )}
    </Button>
    {onClose && (
      <Button
        disabled={saving}
        variant="ghost"
        size="icon-touch"
        aria-label="Close Automation"
        onClick={onClose}
      >
        <XIcon aria-hidden="true" />
      </Button>
    )}
  </div>
);

const CanvasHeader = ({
  draft,
  editor,
  setEditor,
  expanded,
  setExpanded,
  expandButton,
  onClose,
  saving,
  invalid,
  unsaved,
}: {
  draft: AutomationDraft;
  editor: boolean;
  setEditor: (value: boolean) => void;
  expanded: boolean;
  setExpanded: (value: boolean) => void;
  expandButton: RefObject<HTMLButtonElement | null>;
  onClose?: () => void;
  saving: boolean;
  invalid: boolean;
  unsaved: boolean;
}) => (
  <header className={canvasHeader}>
    <CanvasTitle draft={draft} unsaved={unsaved} />
    <div className={canvasToolbar}>
      <CanvasPrimaryActions
        editor={editor}
        setEditor={setEditor}
        setExpanded={setExpanded}
        saving={saving}
        invalid={invalid}
      />
      <CanvasSaveActions
        draft={draft}
        saving={saving}
        unsaved={unsaved}
        invalid={invalid}
      />
    </div>
    <CanvasWindowActions
      expanded={expanded}
      setExpanded={setExpanded}
      expandButton={expandButton}
      onClose={onClose}
      saving={saving}
    />
  </header>
);

const CanvasErrorNotice = ({
  error,
  draft,
  saving,
  invalid,
}: {
  error: string;
  draft: AutomationDraft;
  saving: boolean;
  invalid: boolean;
}) => {
  const workspace = useWorkspace();
  return (
    <Notice role="alert">
      {error}
      {draft.base && (
        <Button
          className="m-1.5"
          variant="outline"
          size="touch"
          disabled={saving}
          onClick={() => {
            void workspace.reload();
          }}
        >
          Reload saved version
        </Button>
      )}
      <Button
        className="m-1.5"
        variant="outline"
        size="touch"
        disabled={saving || invalid}
        onClick={() => {
          void workspace.save(true);
        }}
      >
        Save as new
      </Button>
    </Notice>
  );
};

const CanvasInvalidNotice = ({ issues }: { issues: DocumentIssue[] }) => (
  <StatusNotice>
    Fix or discard invalid editor values before saving.
    {issues.map((issue) => (
      <p key={`${issue.path}:${issue.message}`}>
        {issue.path}: {issue.message}
      </p>
    ))}
  </StatusNotice>
);

export const AutomationCanvasBody = ({
  wide,
  expanded,
  setExpanded,
  expandButton,
  onClose,
  initialEditor = false,
}: {
  wide: boolean;
  expanded: boolean;
  setExpanded: (value: boolean) => void;
  expandButton: RefObject<HTMLButtonElement | null>;
  onClose?: () => void;
  initialEditor?: boolean;
}) => {
  const workspace = useWorkspace();
  const draft = workspace.automationDraft;
  if (!draft) {
    throw new Error("Automation canvas requires a draft");
  }
  const [editor, setEditor] = useState(
    initialEditor || draft.dirty || draft.mode === "create"
  );
  const issues = automationDocumentIssues(draft.document);
  const invalid = !workspace.editorValid || issues.length > 0;
  const unsaved = draft.mode === "create" || draft.dirty;
  const { saving } = workspace;
  return (
    <div
      className={cn(
        canvasInner,
        wide && "max-w-[1440px]",
        (unsaved || invalid) && phoneSaveInset
      )}
    >
      <CanvasHeader
        draft={draft}
        editor={editor}
        setEditor={setEditor}
        expanded={expanded}
        setExpanded={setExpanded}
        expandButton={expandButton}
        onClose={onClose}
        saving={saving}
        invalid={invalid}
        unsaved={unsaved}
      />
      {workspace.error && (
        <CanvasErrorNotice
          error={workspace.error}
          draft={draft}
          saving={saving}
          invalid={invalid}
        />
      )}
      <SaveDiagnostics
        issues={workspace.diagnostics}
        onSelectStep={(id) => {
          setEditor(true);
          focusAutomationStep(id);
        }}
      />
      {invalid && <CanvasInvalidNotice issues={issues} />}
      {editor && (
        <fieldset className="m-0 mb-5 min-w-0 border-0 p-0" disabled={saving}>
          <AutomationEditor
            key={`${workspace.editorEpoch}:${draft.origin_artifact_id ?? ""}`}
            document={draft.document}
            onChange={(document) => workspace.editAutomation(document)}
            onValidityChange={(valid) => workspace.setEditorValid(valid)}
          />
        </fieldset>
      )}
      <AutomationCard
        document={draft.document}
        automationId={draft.base?.automation_id}
      />
      <p className="text-muted-foreground py-10">
        Draft changes do not execute. Run saved Automations from their detail
        page.
      </p>
    </div>
  );
};
