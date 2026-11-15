import { Canvas } from "../features/dashboards/Canvas";
import { useWorkspace } from "../features/workspace/Workspace";
import { Button } from "../components/ui/button";

export default function AutomationDraftPage() {
  const workspace = useWorkspace();
  if (!workspace.automationDraft) return <div><h1>New Automation</h1><p>Build an Automation here, or ask Hearth to propose one.</p><Button size="touch" onClick={workspace.newAutomation}>Create Automation draft</Button></div>;
  return <Canvas onClose={workspace.closeDraft} />;
}
