import { useEffect, useState } from "react";
import { ArrowRightIcon, CheckIcon, WorkflowIcon } from "lucide-react";
import type { AutomationArtifact } from "../../api/agent";
import { automationJsonSource } from "../../api/automation-json";
import { getAutomation } from "../../api/automations";
import { ApiError, useBaseUrlVersion } from "../../api/client";
import { readAutomationDocument, type AutomationDocument } from "../automations/document";
import { AutomationCard } from "../automations/AutomationCard";
import { cn } from "../../lib/utils";
import { Button } from "../../components/ui/button";
import { Notice } from "../../components/ui/notice";
import { artifactAction, artifactFrame, artifactHeader, artifactIcon, artifactSummary, artifactTitle } from "./artifactStyles";

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
  return <div className={cn(artifactFrame, open && "border-violet/45")}>
    <div className={artifactHeader}><span className={artifactIcon}><WorkflowIcon className="size-5" aria-hidden="true" /></span><div><h3 className={artifactTitle}>{document?.definition.name ?? "Automation"}</h3><p className={artifactSummary}>{artifact.mode === "reference" ? "Saved reference · current definition" : artifact.mode === "replace" ? `Replacement proposal · based on revision ${artifact.expected_revision}` : "Unsaved proposal · not created"}</p></div><Button variant={open ? "ghost" : "outline"} size="touch" className={artifactAction(open)} onClick={onSelect}>{open ? <><CheckIcon className="size-4 shrink-0" aria-hidden="true" />Showing in canvas</> : <>Open in canvas<ArrowRightIcon className="size-4 shrink-0" aria-hidden="true" /></>}</Button></div>
    {error && <Notice role="alert">{error}</Notice>}
    {!open && document && <AutomationCard document={document} compact headingLevel={4} />}
    {!document && !error && active && <p role="status">Loading Automation…</p>}
  </div>;
}
