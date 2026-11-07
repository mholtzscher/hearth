import { useEffect, useState } from "react";
import { ArrowRightIcon, CheckIcon, WorkflowIcon } from "lucide-react";
import type { AutomationArtifact } from "../../api/agent";
import { automationJsonSource } from "../../api/automation-json";
import { getAutomation } from "../../api/automations";
import { ApiError, useBaseUrlVersion } from "../../api/client";
import { readAutomationDocument, type AutomationDocument } from "../automations/document";
import { AutomationCard } from "../automations/AutomationCard";

export function AutomationArtifactPreview({ artifact, open, active, onSelect }: { artifact: AutomationArtifact; open: boolean; active: boolean; onSelect: () => void }) {
  const version = useBaseUrlVersion();
  const [document, setDocument] = useState<AutomationDocument | null>(null);
  const [error, setError] = useState("");
  useEffect(() => {
    const controller = new AbortController();
    if (!active) return () => controller.abort();
    setError("");
    if (artifact.mode !== "reference") {
      try { setDocument(readAutomationDocument(artifact.definition_json)); } catch (err) { setError(err instanceof Error ? err.message : "Invalid Automation proposal."); }
    } else {
      void getAutomation(artifact.automation_id, controller.signal).then(saved => {
        if (!controller.signal.aborted) setDocument(readAutomationDocument(automationJsonSource(saved.definition)));
      }).catch(err => { if (!controller.signal.aborted) setError(err instanceof ApiError && err.status === 404 ? "This Automation no longer exists. Its earlier tool result is retained below." : "Could not load this Automation. Open it in the canvas to try again."); });
    }
    return () => controller.abort();
  }, [artifact, version, active]);
  return <div className={`artifact-preview${open ? " open" : ""}`}>
    <div className="artifact-header"><span className="artifact-icon"><WorkflowIcon aria-hidden="true" /></span><div className="artifact-text"><h3>{document?.definition.name ?? "Automation"}</h3><p>{artifact.mode === "reference" ? "Saved reference · current definition" : artifact.mode === "replace" ? `Replacement proposal · based on revision ${artifact.expected_revision}` : "Unsaved proposal · not created"}</p></div><button onClick={onSelect}>{open ? <><CheckIcon aria-hidden="true" />Showing in canvas</> : <>Open in canvas<ArrowRightIcon aria-hidden="true" /></>}</button></div>
    {error && <p className="workspace-notice" role="alert">{error}</p>}
    {!open && document && <AutomationCard document={document} compact headingLevel={4} />}
    {!document && !error && active && <p role="status">Loading Automation…</p>}
  </div>;
}
