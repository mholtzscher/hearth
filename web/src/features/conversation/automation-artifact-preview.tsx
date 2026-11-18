import { ArrowRightIcon, CheckIcon, WorkflowIcon } from "lucide-react";
import { useEffect, useMemo, useState } from "react";

import type { AutomationArtifact } from "../../api/agent";
import { automationJsonSource } from "../../api/automation-json";
import { getAutomation } from "../../api/automations";
import { ApiError, useBaseUrlVersion } from "../../api/client";
import { Button } from "../../components/button";
import { Card } from "../../components/card";
import { Notice } from "../../components/notice";
import { cn } from "../../lib/utils";
import { AutomationCard } from "../automations/automation-card";
import { readAutomationDocument } from "../automations/document";
import type { AutomationDocument } from "../automations/document";
import {
  artifactAction,
  artifactFrame,
  artifactHeader,
  artifactIcon,
  artifactSummary,
  artifactTitle,
} from "./artifact-styles";

interface PreviewRequest {
  artifact: AutomationArtifact;
  version: number;
}
interface PreviewState {
  request: PreviewRequest;
  started: boolean;
  document: AutomationDocument | null;
  error: string;
}

const artifactSummaryLabel = (artifact: AutomationArtifact) => {
  if (artifact.mode === "reference") {
    return "Saved reference · current definition";
  }
  if (artifact.mode === "replace") {
    return `Replacement proposal · based on revision ${artifact.expected_revision}`;
  }
  return "Unsaved proposal · not created";
};

const parseProposal = (
  artifact: Exclude<AutomationArtifact, { mode: "reference" }>
): { document: AutomationDocument | null; error: string } => {
  try {
    return {
      document: readAutomationDocument(artifact.definition_json),
      error: "",
    };
  } catch (error_) {
    return {
      document: null,
      error:
        error_ instanceof Error
          ? error_.message
          : "Invalid Automation proposal.",
    };
  }
};

const previewReset = (
  state: PreviewState,
  request: PreviewRequest,
  active: boolean
): PreviewState | null => {
  if (active && (!state.started || state.request !== request)) {
    const proposal =
      request.artifact.mode === "reference"
        ? null
        : parseProposal(request.artifact);
    return {
      request,
      started: true,
      document: proposal?.document ?? state.document,
      error: proposal?.error ?? "",
    };
  }
  if (!active && state.started) {
    return { ...state, started: false };
  }
  return null;
};

export const AutomationArtifactPreview = ({
  artifact,
  open,
  active,
  onSelect,
}: {
  artifact: AutomationArtifact;
  open: boolean;
  active: boolean;
  onSelect: () => void;
}) => {
  const version = useBaseUrlVersion();
  const request = useMemo(() => ({ artifact, version }), [artifact, version]);
  const [state, setState] = useState<PreviewState>(() => ({
    request,
    started: false,
    document: null,
    error: "",
  }));
  const reset = previewReset(state, request, active);
  if (reset) {
    setState(reset);
  }

  useEffect(() => {
    if (!active || request.artifact.mode !== "reference") {
      return;
    }
    const { automation_id } = request.artifact;
    const controller = new AbortController();
    const load = async () => {
      try {
        const saved = await getAutomation(automation_id, controller.signal);
        if (!controller.signal.aborted) {
          const document = readAutomationDocument(
            automationJsonSource(saved.definition)
          );
          setState((previous) => ({
            ...previous,
            document,
          }));
        }
      } catch (error_) {
        if (!controller.signal.aborted) {
          setState((previous) => ({
            ...previous,
            error:
              error_ instanceof ApiError && error_.status === 404
                ? "This Automation no longer exists. Its earlier tool result is retained below."
                : "Could not load this Automation. Open it in the canvas to try again.",
          }));
        }
      }
    };
    void load();
    return () => controller.abort();
  }, [request, active]);

  const { document, error } = state;
  return (
    <Card className={cn(artifactFrame, open && "border-violet/45")}>
      <div className={artifactHeader}>
        <span className={artifactIcon}>
          <WorkflowIcon className="size-5" aria-hidden="true" />
        </span>
        <div>
          <h3 className={artifactTitle}>
            {document?.definition.name ?? "Automation"}
          </h3>
          <p className={artifactSummary}>{artifactSummaryLabel(artifact)}</p>
        </div>
        <Button
          variant={open ? "ghost" : "outline"}
          size="touch"
          className={artifactAction(open)}
          onClick={onSelect}
        >
          {open ? (
            <>
              <CheckIcon aria-hidden="true" />
              Showing in canvas
            </>
          ) : (
            <>
              Open in canvas
              <ArrowRightIcon aria-hidden="true" />
            </>
          )}
        </Button>
      </div>
      {error && <Notice role="alert">{error}</Notice>}
      {!open && document && (
        <AutomationCard document={document} compact headingLevel={4} />
      )}
      {!document && !error && active && (
        <output className="block">Loading Automation…</output>
      )}
    </Card>
  );
};
