import {
  createContext,
  useCallback,
  useContext,
  useEffect,
  useLayoutEffect,
  useMemo,
  useRef,
  useState,
} from "react";
import type { ReactNode } from "react";
import { useBlocker, useLocation, useNavigate } from "react-router-dom";

import {
  automationSaveDiagnostics,
  createAutomation,
  getAutomation,
  replaceAutomation,
} from "../../api/automations";
import type { AutomationSaveDiagnostic } from "../../api/automations";
import { ApiError, getBaseUrl, setBaseUrl } from "../../api/client";
import {
  createDashboard,
  getDashboard,
  updateDashboard,
} from "../../api/dashboards";
import type { Automation } from "../../api/types";
import { Button } from "../../components/button";
import { DecisionDialog } from "../../components/decision-dialog";
import {
  fromSavedAutomation,
  newAutomationDraft,
} from "../automations/document";
import type {
  AutomationDocument,
  AutomationDraft,
} from "../automations/document";
import type { PresentedArtifact } from "../conversation/runtime";
import {
  draftContext,
  editDraft,
  fromArtifact,
  fromDashboard,
  isUneditedGeneratedView,
} from "../dashboards/draft";
import type { Draft } from "../dashboards/draft";
import type { ViewDefinition } from "../views/catalog";
import { readDefinition } from "../views/catalog/compatibility";
import {
  automationDraftContext,
  editAutomationDraft,
  fromAutomationArtifact,
  isAutomationDraft,
} from "./draft";
import type { CanvasDraft } from "./draft";
import { SaveDiagnostics } from "./save-diagnostics";

interface WorkspaceValue {
  draft: Draft | null;
  draftVersion: number;
  error: string;
  saving: boolean;
  canvasDraft: CanvasDraft | null;
  automationDraft: AutomationDraft | null;
  automationContext: ReturnType<typeof automationDraftContext> | undefined;
  newAutomation: () => void;
  selectAutomation: (automation: Automation, onSelected?: () => void) => void;
  editAutomation: (document: AutomationDocument) => void;
  diagnostics: AutomationSaveDiagnostic[];
  editorValid: boolean;
  setEditorValid: (valid: boolean) => void;
  editorEpoch: number;
  selectArtifact: (
    artifact: PresentedArtifact,
    onSelected?: () => void,
    transition?: (action: () => void) => void
  ) => void;
  receiveArtifact: (
    artifact: PresentedArtifact,
    expectedVersion: number
  ) => void;
  openDashboard: (id: string) => Promise<void>;
  edit: (definition: ViewDefinition) => void;
  save: (asNew?: boolean) => Promise<boolean>;
  reload: () => Promise<void>;
  discard: () => Promise<void>;
  customize: () => void;
  closeDraft: (transition?: (action: () => void) => void) => void;
  composerFocusRequested: boolean;
  finishComposerFocus: () => void;
  navigateRetainingDraft: (path: string) => void;
  changeBackend: (url: string) => void;
  startFresh: (action: () => void) => void;
  context: ReturnType<typeof draftContext>;
}

const saveAutomationDraft = (draft: AutomationDraft, asNew: boolean) => {
  if (!asNew && draft.mode === "replace") {
    return replaceAutomation(
      draft.base.automation_id,
      draft.base.revision,
      draft.document
    );
  }
  return createAutomation(draft.document);
};

const saveDashboardDraft = (
  draft: Draft,
  definition: ViewDefinition,
  asNew: boolean
) => {
  if (!asNew && draft.base) {
    return updateDashboard(
      draft.base.dashboard_id,
      draft.base.revision,
      definition
    );
  }
  return createDashboard(definition);
};

const saveErrorMessage = (
  error_: unknown,
  draft: CanvasDraft,
  asNew: boolean
) => {
  const noun = isAutomationDraft(draft) ? "Automation" : "dashboard";
  if (error_ instanceof ApiError && error_.status === 409) {
    return `This ${noun} changed elsewhere. Reload the saved version or save this draft as new.`;
  }
  if (error_ instanceof ApiError && error_.status === 404) {
    return `The saved ${noun} no longer exists. Save this draft as new.`;
  }
  const detail =
    error_ instanceof Error ? error_.message : `Could not save ${noun}.`;
  const mayHaveSucceeded =
    isAutomationDraft(draft) &&
    (draft.mode === "create" || asNew) &&
    !(error_ instanceof ApiError);
  return `${detail}${mayHaveSucceeded ? " Creation may have succeeded. Check the Automation library before trying again." : ""}`;
};

const Context = createContext<WorkspaceValue | null>(null);
export const useOptionalWorkspace = () => useContext(Context);
export const useWorkspace = () => {
  const value = useContext(Context);
  if (!value) {
    throw new Error("Workspace is missing");
  }
  return value;
};

export const WorkspaceProvider = ({ children }: { children: ReactNode }) => {
  const [draftState, setDraftState] = useState<{
    draft: CanvasDraft | null;
    version: number;
  }>({ draft: null, version: 0 });
  const { draft, version: draftVersion } = draftState;
  const draftBackend = useRef(getBaseUrl());
  const draftVersionRef = useRef(draftVersion);
  useLayoutEffect(() => {
    draftVersionRef.current = draftVersion;
  }, [draftVersion]);
  // User changes invalidate a turn's canvas updates; agent replacements retain its version.
  const setDraft = useCallback(
    (
      next:
        | CanvasDraft
        | null
        | ((current: CanvasDraft | null) => CanvasDraft | null)
    ) => {
      setDraftState((current) => ({
        draft: typeof next === "function" ? next(current.draft) : next,
        version: current.version + 1,
      }));
    },
    []
  );
  const [error, setError] = useState("");
  const [diagnostics, setDiagnostics] = useState<AutomationSaveDiagnostic[]>(
    []
  );
  const [saving, setSaving] = useState(false);
  const [editorValid, setEditorValid] = useState(true);
  const [editorEpoch, setEditorEpoch] = useState(0);
  const [composerFocusRequested, setComposerFocusRequested] = useState(false);
  const finishComposerFocus = useCallback(
    () => setComposerFocusRequested(false),
    []
  );
  const editorValidRef = useRef(true);
  const updateEditorValid = useCallback((valid: boolean) => {
    if (!valid && editorValidRef.current) {
      setDraftState((current) => ({
        ...current,
        version: current.version + 1,
      }));
    }
    editorValidRef.current = valid;
    setEditorValid(valid);
  }, []);
  const savingRef = useRef(false);
  useLayoutEffect(() => {
    savingRef.current = saving;
  }, [saving]);
  const [pending, setPending] = useState<(() => void) | null>(null);
  const allowNavigation = useRef(false);
  const loadSequence = useRef(0);
  const navigate = useNavigate();
  const location = useLocation();
  const locationRef = useRef(location.pathname);
  useLayoutEffect(() => {
    locationRef.current = location.pathname;
  }, [location.pathname]);
  const blocker = useBlocker(({ currentLocation, nextLocation }) => {
    if (allowNavigation.current) {
      allowNavigation.current = false;
      return false;
    }
    return (
      (!!draft?.dirty || !editorValid || saving) &&
      currentLocation.pathname !== nextLocation.pathname
    );
  });
  const blocked = blocker.state === "blocked";
  useEffect(() => {
    if (!draft?.dirty && editorValid && !saving) {
      return;
    }
    const beforeUnload = (event: BeforeUnloadEvent) => {
      event.preventDefault();
      event.returnValue = "";
    };
    window.addEventListener("beforeunload", beforeUnload);
    return () => window.removeEventListener("beforeunload", beforeUnload);
  }, [draft?.dirty, editorValid, saving]);

  const navigateRetainingDraft = useCallback(
    (path: string) => {
      if (location.pathname === path) {
        return;
      }
      allowNavigation.current = true;
      navigate(path);
    },
    [navigate, location.pathname]
  );
  const choose = useCallback(
    (action: () => void) => {
      if (saving) {
        return;
      }
      if (draft?.dirty || !editorValid) {
        setPending(() => action);
      } else {
        action();
      }
    },
    [draft?.dirty, editorValid, saving]
  );
  const resetEditor = useCallback(() => {
    updateEditorValid(true);
    setEditorEpoch((epoch) => epoch + 1);
    setDiagnostics([]);
  }, [updateEditorValid]);
  const clearDraft = useCallback(() => {
    setDraft(null);
    resetEditor();
    setError("");
  }, [resetEditor, setDraft]);
  const closeDraft = useCallback(
    (transition?: (action: () => void) => void) => {
      if (saving) {
        return;
      }
      const close = transition ? () => transition(clearDraft) : clearDraft;
      if (
        draft &&
        !isAutomationDraft(draft) &&
        editorValid &&
        isUneditedGeneratedView(draft)
      ) {
        close();
      } else {
        choose(close);
      }
    },
    [choose, clearDraft, draft, editorValid, saving]
  );
  const openDashboard = useCallback(
    async (id: string) => {
      loadSequence.current += 1;
      const sequence = loadSequence.current;
      const path = location.pathname;
      const backend = getBaseUrl();
      if (
        path.startsWith("/dashboards/") &&
        (isAutomationDraft(draft) || draft?.base?.dashboard_id !== id)
      ) {
        setDraft(null);
      }
      try {
        const dashboard = await getDashboard(id);
        if (
          sequence === loadSequence.current &&
          path === locationRef.current &&
          backend === getBaseUrl()
        ) {
          draftBackend.current = backend;
          setDraft(fromDashboard(dashboard));
          resetEditor();
          setError("");
        }
      } catch (error_) {
        if (sequence === loadSequence.current && path === locationRef.current) {
          setError(
            error_ instanceof Error
              ? error_.message
              : "Could not load dashboard."
          );
        }
      }
    },
    [draft, location.pathname, resetEditor, setDraft]
  );
  const selectArtifact = useCallback(
    (
      item: PresentedArtifact,
      onSelected?: () => void,
      transition?: (action: () => void) => void
    ) => {
      if (item.artifact.kind === "unsupported") {
        setError(
          "This artifact cannot be edited in this Hearth version. Its original tool result is retained in the conversation."
        );
        return;
      }
      choose(async () => {
        const { artifact } = item;
        if (
          artifact.kind === "hearth.automation" &&
          artifact.mode === "reference"
        ) {
          loadSequence.current += 1;
          const sequence = loadSequence.current;
          const backend = getBaseUrl();
          const selectedVersion = draftVersionRef.current;
          try {
            const automation = await getAutomation(artifact.automation_id);
            if (
              sequence !== loadSequence.current ||
              backend !== getBaseUrl() ||
              selectedVersion !== draftVersionRef.current ||
              !editorValidRef.current
            ) {
              return;
            }
            draftBackend.current = backend;
            setDraft({
              ...fromSavedAutomation(automation),
              origin_artifact_id: artifact.artifact_id,
            });
            resetEditor();
            setError("");
            onSelected?.();
          } catch (error_) {
            if (
              sequence === loadSequence.current &&
              backend === getBaseUrl() &&
              selectedVersion === draftVersionRef.current
            ) {
              setError(
                error_ instanceof Error
                  ? error_.message
                  : "Could not load Automation."
              );
            }
          }
          return;
        }
        try {
          loadSequence.current += 1;
          draftBackend.current = getBaseUrl();
          const next =
            artifact.kind === "hearth.automation"
              ? fromAutomationArtifact(
                  artifact,
                  isAutomationDraft(draft) ? draft : null
                )
              : fromArtifact(item, isAutomationDraft(draft) ? null : draft);
          const apply = () => {
            setDraft(next);
            resetEditor();
            setError("");
            onSelected?.();
          };
          if (transition) {
            transition(apply);
          } else {
            apply();
          }
        } catch (error_) {
          setError(
            error_ instanceof Error
              ? error_.message
              : "Could not open artifact."
          );
        }
      });
    },
    [choose, draft, resetEditor, setDraft]
  );
  const receiveArtifact = useCallback(
    (item: PresentedArtifact, expectedVersion: number) => {
      if (savingRef.current || !editorValidRef.current) {
        return;
      }
      setDraftState((current) => {
        if (
          current.version !== expectedVersion ||
          draftBackend.current !== getBaseUrl()
        ) {
          return current;
        }
        if (item.artifact.kind === "hearth.automation") {
          if (
            !isAutomationDraft(current.draft) ||
            item.artifact.mode === "reference"
          ) {
            return current;
          }
          if (item.artifact.mode !== current.draft.mode) {
            return current;
          }
          if (
            item.artifact.mode === "replace" &&
            (current.draft.mode !== "replace" ||
              current.draft.base.automation_id !==
                item.artifact.automation_id ||
              current.draft.base.revision !== item.artifact.expected_revision)
          ) {
            return current;
          }
          return {
            ...current,
            draft: fromAutomationArtifact(item.artifact, current.draft),
          };
        }
        if (
          item.artifact.kind !== "hearth.view" ||
          isAutomationDraft(current.draft)
        ) {
          return current;
        }
        return { ...current, draft: fromArtifact(item, current.draft) };
      });
    },
    []
  );
  const navigateAfterAutomationSave = useCallback(
    (id: string) => {
      if (
        pending ||
        blocker.state === "blocked" ||
        !location.pathname.startsWith("/automations/")
      ) {
        return;
      }
      navigateRetainingDraft(`/automations/${encodeURIComponent(id)}`);
    },
    [blocker.state, location.pathname, navigateRetainingDraft, pending]
  );
  const navigateAfterDashboardSave = useCallback(
    (id: string, asNew: boolean) => {
      if (
        !asNew ||
        pending ||
        blocker.state === "blocked" ||
        !location.pathname.startsWith("/dashboards/")
      ) {
        return;
      }
      navigateRetainingDraft(`/dashboards/${encodeURIComponent(id)}`);
    },
    [blocker.state, location.pathname, navigateRetainingDraft, pending]
  );
  const save = useCallback(
    async (asNew = false): Promise<boolean> => {
      if (!draft || !editorValid || savingRef.current) {
        return false;
      }
      if (draftBackend.current !== getBaseUrl()) {
        setError(
          "The server changed. Return to the original server before saving this draft."
        );
        return false;
      }
      const view = isAutomationDraft(draft)
        ? null
        : readDefinition(draft.definition);
      if (view && view.status !== "supported") {
        setError("This view contains unsupported content and cannot be saved.");
        return false;
      }
      setDraftState((current) => ({
        ...current,
        version: current.version + 1,
      }));
      savingRef.current = true;
      setSaving(true);
      setError("");
      setDiagnostics([]);
      let saved = false;
      try {
        if (isAutomationDraft(draft)) {
          const result = await saveAutomationDraft(draft, asNew);
          if (draftBackend.current === getBaseUrl()) {
            setDraft(fromSavedAutomation(result));
            resetEditor();
            navigateAfterAutomationSave(result.id);
            saved = true;
          }
        } else if (view?.status === "supported") {
          const result = await saveDashboardDraft(
            draft,
            view.definition,
            asNew
          );
          setDraft(fromDashboard(result));
          navigateAfterDashboardSave(result.id, asNew);
          saved = true;
        }
      } catch (error_) {
        if (isAutomationDraft(draft)) {
          setDiagnostics(automationSaveDiagnostics(error_));
        }
        setError(saveErrorMessage(error_, draft, asNew));
      }
      savingRef.current = false;
      setSaving(false);
      return saved;
    },
    [
      draft,
      editorValid,
      navigateAfterAutomationSave,
      navigateAfterDashboardSave,
      resetEditor,
      setDraft,
    ]
  );
  const reload = useCallback(async () => {
    if (!draft?.base) {
      return;
    }
    setSaving(true);
    try {
      if (isAutomationDraft(draft)) {
        setDraft(
          fromSavedAutomation(await getAutomation(draft.base.automation_id))
        );
        resetEditor();
        setError("");
      } else {
        await openDashboard(draft.base.dashboard_id);
      }
    } catch (error_) {
      setError(
        error_ instanceof Error
          ? error_.message
          : "Could not reload saved definition."
      );
    }
    setSaving(false);
  }, [draft, openDashboard, resetEditor, setDraft]);
  const discard = useCallback(async () => {
    if (!draft?.base) {
      clearDraft();
      return;
    }
    setSaving(true);
    try {
      setDraft(
        isAutomationDraft(draft)
          ? fromSavedAutomation(await getAutomation(draft.base.automation_id))
          : fromDashboard(await getDashboard(draft.base.dashboard_id))
      );
      resetEditor();
      setError("");
    } catch (error_) {
      setError(
        error_ instanceof Error ? error_.message : "Could not reload dashboard."
      );
    }
    setSaving(false);
  }, [clearDraft, draft, resetEditor, setDraft]);
  const proceed = useCallback(() => {
    if (blocked) {
      blocker.proceed();
    } else if (pending) {
      pending();
    }
    setPending(null);
  }, [blocked, blocker, pending]);
  const stay = useCallback(() => {
    if (blocked) {
      blocker.reset();
    }
    setPending(null);
  }, [blocked, blocker]);
  const newAutomation = useCallback(() => {
    choose(() => {
      loadSequence.current += 1;
      draftBackend.current = getBaseUrl();
      setDraft(newAutomationDraft());
      resetEditor();
      setError("");
      navigateRetainingDraft("/automations/new");
    });
  }, [choose, navigateRetainingDraft, resetEditor, setDraft]);
  const selectAutomation = useCallback(
    (automation: Automation, onSelected?: () => void) => {
      choose(() => {
        loadSequence.current += 1;
        draftBackend.current = getBaseUrl();
        setDraft(fromSavedAutomation(automation));
        resetEditor();
        setError("");
        onSelected?.();
      });
    },
    [choose, resetEditor, setDraft]
  );
  const editAutomation = useCallback(
    (document: AutomationDocument) => {
      setDraft((current) =>
        isAutomationDraft(current)
          ? editAutomationDraft(current, document)
          : current
      );
    },
    [setDraft]
  );
  const edit = useCallback(
    (nextDefinition: ViewDefinition) => {
      setDraft((current) =>
        current && !isAutomationDraft(current)
          ? editDraft(current, nextDefinition)
          : current
      );
    },
    [setDraft]
  );
  const customize = useCallback(() => {
    if (editorValid) {
      setComposerFocusRequested(true);
      navigateRetainingDraft("/");
    }
  }, [editorValid, navigateRetainingDraft]);
  const changeBackend = useCallback(
    (url: string) => {
      const next = url.trim().replace(/\/$/u, "");
      if (next === getBaseUrl()) {
        return;
      }
      choose(() => {
        loadSequence.current += 1;
        clearDraft();
        setBaseUrl(next);
      });
    },
    [choose, clearDraft]
  );
  const startFresh = useCallback(
    (action: () => void) => {
      choose(() => {
        loadSequence.current += 1;
        clearDraft();
        action();
      });
    },
    [choose, clearDraft]
  );
  const dashboardDraft = isAutomationDraft(draft) ? null : draft;
  const automationDraft = isAutomationDraft(draft) ? draft : null;
  const canSave =
    editorValid && (!!automationDraft || !!draftContext(dashboardDraft));
  const value = useMemo(
    () => ({
      draft: dashboardDraft,
      canvasDraft: draft,
      automationDraft,
      diagnostics,
      automationContext:
        editorValid && automationDraft
          ? automationDraftContext(automationDraft)
          : undefined,
      newAutomation,
      selectAutomation,
      editAutomation,
      draftVersion,
      error,
      saving,
      editorValid,
      setEditorValid: updateEditorValid,
      editorEpoch,
      selectArtifact,
      receiveArtifact,
      openDashboard,
      edit,
      save,
      reload,
      discard,
      closeDraft,
      composerFocusRequested,
      finishComposerFocus,
      customize,
      navigateRetainingDraft,
      changeBackend,
      startFresh,
      context: editorValid ? draftContext(dashboardDraft) : undefined,
    }),
    [
      automationDraft,
      changeBackend,
      closeDraft,
      composerFocusRequested,
      customize,
      dashboardDraft,
      diagnostics,
      discard,
      draft,
      draftVersion,
      edit,
      editAutomation,
      editorEpoch,
      editorValid,
      error,
      finishComposerFocus,
      navigateRetainingDraft,
      newAutomation,
      openDashboard,
      receiveArtifact,
      reload,
      save,
      saving,
      selectArtifact,
      selectAutomation,
      startFresh,
      updateEditorValid,
    ]
  );
  return (
    <Context.Provider value={value}>
      {children}
      {(blocked || pending) && (
        <DecisionDialog
          title="Keep your changes?"
          onClose={() => {
            if (!saving) {
              stay();
            }
          }}
        >
          <p className="text-muted-foreground mt-[14px] mb-5">
            This draft has unsaved changes. Save it, discard it, or stay here.
          </p>
          {!editorValid && (
            <output className="text-muted-foreground mt-[14px] mb-5 block">
              Fix or discard the invalid editor values before saving.
            </output>
          )}
          {error && (
            <p className="text-muted-foreground mt-[14px] mb-5" role="alert">
              {error}
            </p>
          )}
          <SaveDiagnostics issues={diagnostics} />
          <div className="flex flex-wrap justify-end gap-2">
            <Button
              variant="outline"
              size="touch"
              disabled={saving}
              onClick={stay}
            >
              Stay
            </Button>
            <Button
              variant="outline"
              size="touch"
              disabled={saving}
              onClick={() => {
                loadSequence.current += 1;
                setDraft(null);
                resetEditor();
                proceed();
              }}
            >
              Discard
            </Button>
            {error && draft?.base && (
              <>
                <Button
                  variant="outline"
                  size="touch"
                  disabled={saving}
                  onClick={() => {
                    reload().catch(() => null);
                    stay();
                  }}
                >
                  Reload saved version
                </Button>
                <Button
                  variant="outline"
                  size="touch"
                  disabled={saving || !canSave}
                  onClick={async () => {
                    const ok = await save(true);
                    if (ok) {
                      proceed();
                    }
                  }}
                >
                  Save as new
                </Button>
              </>
            )}
            <Button
              size="touch"
              disabled={saving || !canSave}
              onClick={async () => {
                const ok = await save();
                if (ok) {
                  proceed();
                }
              }}
            >
              {automationDraft?.document.definition.enabled
                ? "Save and enable"
                : "Save"}
            </Button>
          </div>
        </DecisionDialog>
      )}
    </Context.Provider>
  );
};
