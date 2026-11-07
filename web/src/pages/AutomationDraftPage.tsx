import { Canvas } from "../features/dashboards/Canvas";
import { useWorkspace } from "../features/workspace/Workspace";

export default function AutomationDraftPage() {
  const workspace = useWorkspace();
  if (!workspace.automationDraft) return <div className="dashboard-library"><h1>New Automation</h1><p>Build an Automation here, or ask Hearth to propose one.</p><button className="primary" onClick={workspace.newAutomation}>Create Automation draft</button></div>;
  return <Canvas onClose={workspace.closeDraft} />;
}
